// Package app composes the application modules into a runnable HTTP handler.
package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/auth"
	"github.com/emby-user-manager/emby-user-manager/internal/config"
	"github.com/emby-user-manager/emby-user-manager/internal/credentials"
	"github.com/emby-user-manager/emby-user-manager/internal/emby"
	"github.com/emby-user-manager/emby-user-manager/internal/expiry"
	"github.com/emby-user-manager/emby-user-manager/internal/invites"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
	"github.com/emby-user-manager/emby-user-manager/internal/portal"
	"github.com/emby-user-manager/emby-user-manager/internal/web"
)

type Application struct {
	store    *sqlite.Store
	handler  http.Handler
	Emby     emby.Client
	expiry   *expiry.Runner
	accounts *accounts.Service
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
	accountService := accounts.New(store, embyClient, credentials.New(cfg.CredentialMasterKey, cfg.CredentialPreviousKey, cfg.APIKey))
	inviteService := invites.New(store, accountService)
	portalService := portal.New(store, embyClient, cfg.SessionTTL)
	webServer, err := web.New(authService, portalService, accountService, inviteService, cfg.APIKey, cfg.CookieSecure, cfg.SessionTTL)
	if err != nil {
		return closeOnError(fmt.Errorf("configure web server: %w", err))
	}
	return &Application{store: store, handler: webServer.Handler(), Emby: embyClient, expiry: expiry.New(store, embyClient), accounts: accountService}, nil
}

func (a *Application) Handler() http.Handler               { return a.handler }
func (a *Application) RunExpiry(ctx context.Context) error { return a.expiry.RunOnce(ctx) }
func (a *Application) RunProvisioningRecovery(ctx context.Context) error {
	return a.accounts.RecoverAccountCreates(ctx)
}
func (a *Application) Close() error { return a.store.Close() }
