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
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/settings"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
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
	requestService := requests.New(store, tmdbClient, embyClient)
	timeLocation, err := cfg.TimeLocation()
	if err != nil {
		return closeOnError(err)
	}
	settingsService := settings.New(store, timeLocation)
	if err := settingsService.Ensure(ctx); err != nil {
		return closeOnError(fmt.Errorf("seed settings: %w", err))
	}
	webServer, err := web.New(authService, portalService, accountService, inviteService, paymentService, settingsService, requestService, tmdbClient, cfg.APIKey, cfg.CookieSecure, cfg.SessionTTL, timeLocation)
	if err != nil {
		return closeOnError(fmt.Errorf("configure web server: %w", err))
	}
	return &Application{store: store, handler: webServer.Handler(), Emby: embyClient, TMDB: tmdbClient, expiry: expiry.New(store, embyClient), accounts: accountService, payments: paymentService, requests: requestService}, nil
}

func (a *Application) Handler() http.Handler               { return a.handler }
func (a *Application) RunExpiry(ctx context.Context) error { return a.expiry.RunOnce(ctx) }
func (a *Application) RunProvisioningRecovery(ctx context.Context) error {
	return a.accounts.RecoverAccountCreates(ctx)
}
func (a *Application) RunPayments(ctx context.Context) error { return a.payments.Reconcile(ctx) }
func (a *Application) Close() error                          { return a.store.Close() }
