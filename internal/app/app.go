// Package app composes the application modules into a runnable HTTP handler.
package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Rst307/emby-service-portal/internal/accounts"
	"github.com/Rst307/emby-service-portal/internal/auth"
	"github.com/Rst307/emby-service-portal/internal/config"
	"github.com/Rst307/emby-service-portal/internal/credentials"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/expiry"
	"github.com/Rst307/emby-service-portal/internal/invites"
	"github.com/Rst307/emby-service-portal/internal/paymentcenter"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/portal"
	"github.com/Rst307/emby-service-portal/internal/recent"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/settings"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
	"github.com/Rst307/emby-service-portal/internal/update"
	"github.com/Rst307/emby-service-portal/internal/web"
)

type Application struct {
	store    *sqlite.Store
	handler  http.Handler
	Emby     emby.Client
	TMDB     *tmdb.Client
	expiry   *expiry.Runner
	accounts *accounts.Service
	payments *payments.Service
	requests *requests.Service
	recent   *recent.Service
	Updater  *update.Service
	// restart is buffered so exactly one pending restart request is kept
	// regardless of whether the web layer or the background worker asks.
	restart chan struct{}
}

func New(ctx context.Context, cfg config.Config) (*Application, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*Application, error) { _ = store.Close(); return nil, err }
	authService := auth.New(store, cfg.SessionTTL)
	if err := authService.BootstrapAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return closeOnError(err)
	}
	embyClient, err := emby.NewHTTPClient(cfg.EmbyBaseURL, cfg.EmbyAPIKey)
	if err != nil {
		return closeOnError(fmt.Errorf("configure Emby client: %w", err))
	}
	vault := credentials.New(cfg.CredentialMasterKey, cfg.CredentialPreviousKey, cfg.APIKey)
	accountService := accounts.New(store, embyClient, vault)
	inviteService := invites.New(store, accountService)
	paymentService := payments.New(store, accountService, vault, paymentcenter.NewClient(nil))
	portalService := portal.New(store, embyClient, cfg.SessionTTL)
	tmdbClient := tmdb.NewClient(cfg.TmdbAPIKey)
	if cfg.TmdbBaseURL != "" {
		tmdbClient.SetBaseURL(cfg.TmdbBaseURL)
	}
	if cfg.TmdbHTTPProxy != "" {
		if err := tmdbClient.SetProxy(cfg.TmdbHTTPProxy); err != nil {
			return closeOnError(fmt.Errorf("configure TMDB proxy: %w", err))
		}
	}
	tmdbClient.SetTimeout(cfg.TmdbTimeout)
	if cfg.TmdbImageBaseURL != "" {
		tmdb.SetPosterBaseURL(cfg.TmdbImageBaseURL)
	}
	requestService := requests.New(store, tmdbClient, embyClient)
	recentService := recent.New(store, embyClient)
	timeLocation, err := cfg.TimeLocation()
	if err != nil {
		return closeOnError(err)
	}
	settingsService := settings.New(store, timeLocation)
	if err := settingsService.Ensure(ctx); err != nil {
		return closeOnError(fmt.Errorf("seed settings: %w", err))
	}
	updateService := update.New(store, update.Options{
		APIBase:      cfg.UpdateAPIBase,
		DownloadBase: cfg.UpdateDownloadBase,
		AutoDefault:  cfg.UpdateAuto,
		Interval:     cfg.UpdateInterval,
		Proxy:        cfg.UpdateHTTPProxy,
	})
	if err := updateService.Ensure(ctx); err != nil {
		return closeOnError(fmt.Errorf("seed update settings: %w", err))
	}
	if err := updateService.Cleanup(); err != nil {
		return closeOnError(fmt.Errorf("clean update artifacts: %w", err))
	}
	webServer, err := web.New(authService, portalService, accountService, inviteService, paymentService, settingsService, requestService, recentService, tmdbClient, updateService, cfg.APIKey, cfg.CookieSecure, cfg.SessionTTL, timeLocation)
	if err != nil {
		return closeOnError(fmt.Errorf("configure web server: %w", err))
	}
	application := &Application{store: store, handler: webServer.Handler(), Emby: embyClient, TMDB: tmdbClient, expiry: expiry.New(store, embyClient), accounts: accountService, payments: paymentService, requests: requestService, recent: recentService, Updater: updateService, restart: make(chan struct{}, 1)}
	webServer.SetRestartNotifier(application.requestRestart)
	return application, nil
}

// requestRestart asks main to shut down and exit with RestartExitCode so the
// service manager relaunches the (new) binary.
func (a *Application) requestRestart() {
	select {
	case a.restart <- struct{}{}:
	default:
	}
}

// RestartRequested is the channel main selects on after an update is applied.
func (a *Application) RestartRequested() <-chan struct{} { return a.restart }

func (a *Application) Handler() http.Handler               { return a.handler }
func (a *Application) RunExpiry(ctx context.Context) error { return a.expiry.RunOnce(ctx) }
func (a *Application) RunProvisioningRecovery(ctx context.Context) error {
	return a.accounts.RecoverAccountCreates(ctx)
}
func (a *Application) RunPayments(ctx context.Context) error { return a.payments.Reconcile(ctx) }

// RunUpdateTick runs the periodic self-update check. It returns true when an
// automatic update was applied and the process should restart.
func (a *Application) RunUpdateTick(ctx context.Context) (bool, error) {
	return a.Updater.BackgroundTick(ctx)
}

// RunLibraryWatch scans Emby for newly added items: it records the 最近更新
// feed and auto-fulfills pending media requests whose TMDB title matches a
// new addition.
func (a *Application) RunLibraryWatch(ctx context.Context) error {
	return a.recent.ScanOnce(ctx)
}

// RequestRestart asks main to shut down and exit with RestartExitCode.
func (a *Application) RequestRestart() { a.requestRestart() }
func (a *Application) Close() error    { return a.store.Close() }
