// Package web provides the HTTP delivery layer.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/auth"
	"github.com/emby-user-manager/emby-user-manager/internal/invites"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
	"github.com/emby-user-manager/emby-user-manager/internal/portal"
	"github.com/emby-user-manager/emby-user-manager/internal/ratelimit"
	"github.com/emby-user-manager/emby-user-manager/internal/web/admin"
)

const sessionCookie = "eum_admin_session"
const userSessionCookie = "eum_user_session"
const csrfCookie = "eum_csrf"

type Server struct {
	auth         *auth.Service
	portal       *portal.Service
	accounts     *accounts.Service
	invites      *invites.Service
	apiKey       string
	templates    *admin.Templates
	cookieSecure bool
	sessionTTL   time.Duration
	timeLocation *time.Location
	loginLimit   *ratelimit.Limiter
	publicLimit  *ratelimit.Limiter
}

func New(authService *auth.Service, portalService *portal.Service, accountService *accounts.Service, inviteService *invites.Service, apiKey string, cookieSecure bool, sessionTTL time.Duration, timeLocation *time.Location) (*Server, error) {
	if timeLocation == nil {
		timeLocation = time.UTC
	}
	templates, err := admin.NewTemplates(timeLocation)
	if err != nil {
		return nil, err
	}
	return &Server{
		auth: authService, portal: portalService, accounts: accountService, invites: inviteService,
		apiKey: apiKey, templates: templates, cookieSecure: cookieSecure, sessionTTL: sessionTTL, timeLocation: timeLocation,
		loginLimit: ratelimit.New(10, time.Minute), publicLimit: ratelimit.New(20, time.Minute),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.homePage)
	mux.HandleFunc("GET /static/app.css", s.stylesheet)
	mux.HandleFunc("GET /static/app.js", s.script)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/health", s.apiHealth)
	mux.HandleFunc("GET /api/v1/accounts", s.apiAccounts)
	mux.HandleFunc("POST /api/v1/accounts", s.apiCreateAccount)
	mux.HandleFunc("POST /api/v1/accounts/restrict-media", s.apiRestrictAccountMedia)
	mux.HandleFunc("PATCH /api/v1/accounts/{id}", s.apiUpdateAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/enable", s.apiEnableAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/disable", s.apiDisableAccount)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", s.apiDeleteAccount)
	mux.HandleFunc("GET /api/v1/invites", s.apiInvites)
	mux.HandleFunc("POST /api/v1/invites", s.apiCreateInvite)
	mux.HandleFunc("PATCH /api/v1/invites/{id}", s.apiUpdateInvite)
	mux.HandleFunc("DELETE /api/v1/invites/{id}", s.apiDeleteInvite)
	mux.HandleFunc("POST /api/v1/register", s.apiRegister)
	mux.HandleFunc("POST /api/v1/renew", s.apiRenew)
	mux.HandleFunc("GET /admin/login", s.loginPage)
	mux.HandleFunc("POST /admin/login", s.login)
	mux.HandleFunc("GET /admin/", s.dashboard)
	mux.HandleFunc("GET /admin/accounts", s.accountList)
	mux.HandleFunc("POST /admin/accounts", s.accountCreate)
	mux.HandleFunc("POST /admin/accounts/sync", s.accountSync)
	mux.HandleFunc("POST /admin/accounts/{id}/update", s.accountUpdate)
	mux.HandleFunc("POST /admin/accounts/batch", s.accountBatch)
	mux.HandleFunc("POST /admin/accounts/{id}/enable", s.accountEnable)
	mux.HandleFunc("POST /admin/accounts/{id}/disable", s.accountDisable)
	mux.HandleFunc("POST /admin/accounts/{id}/delete", s.accountDelete)
	mux.HandleFunc("POST /admin/accounts/{id}/delete/confirm", s.accountDeleteConfirm)
	mux.HandleFunc("GET /admin/invites", s.inviteList)
	mux.HandleFunc("POST /admin/invites", s.inviteCreate)
	mux.HandleFunc("POST /admin/invites/{id}/toggle", s.inviteToggle)
	mux.HandleFunc("POST /admin/invites/{id}/delete", s.inviteDelete)
	mux.HandleFunc("GET /portal/login", s.portalLoginPage)
	mux.HandleFunc("POST /portal/login", s.portalLogin)
	mux.HandleFunc("GET /portal/", s.portalDashboard)
	mux.HandleFunc("POST /portal/logout", s.portalLogout)
	mux.HandleFunc("GET /login", s.portalLoginPage)
	mux.HandleFunc("POST /login", s.portalLogin)
	mux.HandleFunc("GET /user/", s.portalDashboard)
	mux.HandleFunc("POST /user/logout", s.portalLogout)
	mux.HandleFunc("GET /register", s.registerPage)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /renew", s.renewPage)
	mux.HandleFunc("POST /renew", s.renew)
	mux.HandleFunc("POST /admin/logout", s.logout)
	return securityHeaders(s.limitBody(s.ensureCSRF(mux)))
}

func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	s.templates.Render(w, "home", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
func (s *Server) apiHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "dev"})
}

func (s *Server) apiAuthorized(r *http.Request) bool {
	key := r.Header.Get("X-API-Key")
	return key != "" && len(key) == len(s.apiKey) && subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) == 1
}
func (s *Server) requireAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if s.apiAuthorized(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
	return false
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key is required"})
		return "", false
	}
	return key, true
}
func (s *Server) apiAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	accounts, err := s.accounts.List(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "load accounts"})
		return
	}
	writeJSON(w, http.StatusOK, accountJSONList(accounts))
}
func (s *Server) apiCreateAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var input struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		ExpiresAt string `json:"expires_at"`
		Note      string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err == nil {
		account, createErr := s.accounts.CreateIdempotent(r.Context(), idempotencyKey, accounts.CreateInput{Username: input.Username, Password: input.Password, ExpiresAt: expiresAt, Note: input.Note})
		err = createErr
		if err == nil {
			writeJSON(w, http.StatusCreated, accountJSONFrom(account))
			return
		}
	}
	status := http.StatusBadRequest
	if errors.Is(err, accounts.ErrIdempotencyKeyConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": accountError(err)})
}
func (s *Server) apiRestrictAccountMedia(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	count, err := s.accounts.RestrictAllMediaFeatures(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "restrict account media features"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated_accounts": count})
}
func (s *Server) apiUpdateAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		ExpiresAt string `json:"expires_at"`
		Note      string `json:"note"`
		Version   int64  `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	expiresAt, parseErr := time.Parse(time.RFC3339, input.ExpiresAt)
	if err == nil {
		err = parseErr
	}
	if err == nil {
		account, updateErr := s.accounts.Update(r.Context(), id, accounts.UpdateInput{ExpiresAt: expiresAt, Note: input.Note, Version: input.Version})
		err = updateErr
		if err == nil {
			writeJSON(w, http.StatusOK, accountJSONFrom(account))
			return
		}
	}
	status := http.StatusBadRequest
	if errors.Is(err, accounts.ErrConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": accountError(err)})
}
func (s *Server) apiEnableAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Version int64 `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil {
		err = s.accounts.Enable(r.Context(), id, input.Version)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiDisableAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Version int64 `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil {
		err = s.accounts.Disable(r.Context(), id, input.Version)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.accounts.Delete(r.Context(), id)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiActionResult(w http.ResponseWriter, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, accounts.ErrConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": accountError(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) apiInvites(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	codes, err := s.invites.List(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "load invites"})
		return
	}
	output := make([]inviteJSON, 0, len(codes))
	for _, code := range codes {
		output = append(output, inviteJSON{ID: code.ID, CodePrefix: code.CodePrefix, DurationDays: code.DurationDays, DurationMinutes: code.DurationMinutes, MaxUses: code.MaxUses, UsedCount: code.UsedCount, Enabled: code.Enabled, Note: code.Note})
	}
	writeJSON(w, 200, output)
}

type inviteJSON struct {
	ID              int64  `json:"id"`
	CodePrefix      string `json:"code_prefix"`
	DurationDays    int    `json:"duration_days"` // deprecated; use duration_minutes
	DurationMinutes int    `json:"duration_minutes"`
	MaxUses         int    `json:"max_uses"`
	UsedCount       int    `json:"used_count"`
	Enabled         bool   `json:"enabled"`
	Note            string `json:"note"`
}

func (s *Server) apiCreateInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	var input struct {
		DurationDays    int    `json:"duration_days"` // backwards-compatible input
		DurationMinutes int    `json:"duration_minutes"`
		MaxUses         int    `json:"max_uses"`
		Note            string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := s.invites.Create(r.Context(), invites.CreateInput{DurationDays: input.DurationDays, DurationMinutes: input.DurationMinutes, MaxUses: input.MaxUses, Note: input.Note})
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": inviteError(err)})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": created.Invite.ID, "code": created.Code})
}
func (s *Server) apiUpdateInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil && input.Enabled == nil {
		err = errors.New("enabled is required")
	}
	if err == nil {
		err = s.invites.SetEnabled(r.Context(), id, *input.Enabled)
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "cannot update invite"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) apiDeleteInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.invites.Delete(r.Context(), id)
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "cannot delete invite"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) apiRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, err := s.invites.RegisterIdempotent(r.Context(), idempotencyKey, input.Code, input.Username, input.Password)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, accounts.ErrIdempotencyKeyConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": "registration failed"})
		return
	}
	writeJSON(w, http.StatusCreated, accountJSONFrom(account))
}
func (s *Server) apiRenew(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, err := s.invites.Renew(r.Context(), input.Code, input.Username, input.Password)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "renewal failed"})
		return
	}
	writeJSON(w, http.StatusOK, accountJSONFrom(account))
}

type accountJSON struct {
	ID         int64      `json:"id"`
	Version    int64      `json:"version"`
	EmbyUserID string     `json:"emby_user_id"`
	Username   string     `json:"username"`
	Status     string     `json:"status"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

func accountJSONFrom(account sqlite.Account) accountJSON {
	return accountJSON{
		ID: account.ID, Version: account.Version, EmbyUserID: account.EmbyUserID, Username: account.Username,
		Status: account.Status, ExpiresAt: account.ExpiresAt, Note: account.Note,
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, DisabledAt: account.DisabledAt,
	}
}

func accountJSONList(accounts []sqlite.Account) []accountJSON {
	output := make([]accountJSON, 0, len(accounts))
	for _, account := range accounts {
		output = append(output, accountJSONFrom(account))
	}
	return output
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON request"})
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) parseDateTime(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02T15:04", value, s.timeLocation)
}

func (s *Server) parseAccountDateTime(value, original string) (time.Time, error) {
	if originalTime, err := time.Parse(time.RFC3339Nano, original); err == nil && originalTime.In(s.timeLocation).Format("2006-01-02T15:04") == value {
		return originalTime, nil
	}
	return s.parseDateTime(value)
}

func accountID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid account ID")
	}
	return id, nil
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.loggedIn(r) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	s.templates.Render(w, "login", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !s.allowAttempt(w, r, s.loginLimit, r.Form.Get("username")) {
		return
	}
	token, err := s.auth.Login(r.Context(), r.Form.Get("username"), r.Form.Get("password"))
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			s.templates.RenderStatus(w, "login", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: "用户名或密码错误"}, http.StatusUnauthorized)
			return
		}
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, token)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}
func (s *Server) portalLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.portalAccount(r); ok {
		http.Redirect(w, r, "/portal/", http.StatusSeeOther)
		return
	}
	s.templates.Render(w, "portal-login", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) portalLogin(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.allowAttempt(w, r, s.loginLimit, r.Form.Get("username")) {
		return
	}
	token, err := s.portal.Login(r.Context(), r.Form.Get("username"), r.Form.Get("password"))
	if err != nil {
		s.templates.RenderStatus(w, "portal-login", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: "用户名或密码错误，或该账号不可使用用户中心"}, http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: userSessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.sessionTTL.Seconds())})
	http.Redirect(w, r, "/portal/", http.StatusSeeOther)
}
func (s *Server) portalDashboard(w http.ResponseWriter, r *http.Request) {
	account, ok := s.portalAccount(r)
	if !ok {
		http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
		return
	}
	s.templates.Render(w, "portal-dashboard", admin.ViewData{CSRFToken: csrfFromRequest(r), Account: account})
}
func (s *Server) portalLogout(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(userSessionCookie); err == nil {
		_ = s.portal.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: userSessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
}
func (s *Server) portalAccount(r *http.Request) (sqlite.Account, bool) {
	cookie, err := r.Cookie(userSessionCookie)
	if err != nil {
		return sqlite.Account{}, false
	}
	return s.portal.Account(r.Context(), cookie.Value)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.templates.Render(w, "dashboard", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) accountList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderAccounts(w, r, http.StatusOK, "", r.URL.Query().Get("message"))
}
func (s *Server) accountCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	expiresAt, err := s.parseDateTime(r.Form.Get("expires_at"))
	if err == nil {
		_, err = s.accounts.Create(r.Context(), accounts.CreateInput{Username: r.Form.Get("username"), Password: r.Form.Get("password"), ExpiresAt: expiresAt, Note: r.Form.Get("note")})
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	expiresAt, err := s.parseDateTime(r.Form.Get("expires_at"))
	if err == nil {
		_, err = s.accounts.SyncFromEmby(r.Context(), accounts.SyncInput{ExpiresAt: expiresAt, Note: r.Form.Get("note")})
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, "同步失败：请检查 Emby 连接和到期时间", "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	expiresAt, parseErr := s.parseAccountDateTime(r.Form.Get("expires_at"), r.Form.Get("expires_at_original"))
	if err == nil && id > 0 && parseErr == nil {
		version, versionErr := accountVersion(r)
		if versionErr != nil {
			err = versionErr
		} else {
			_, err = s.accounts.Update(r.Context(), id, accounts.UpdateInput{ExpiresAt: expiresAt, Note: r.Form.Get("note"), Version: version})
		}
	} else if err == nil {
		err = parseErr
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}
func (s *Server) accountBatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	ids, versions, err := accountSelections(r.Form["account_id"])
	input := accounts.BatchInput{AccountIDs: ids, Versions: versions, Action: accounts.BatchAction(r.Form.Get("action"))}
	if err == nil {
		switch input.Action {
		case accounts.BatchSetExpiry:
			input.ExpiresAt, err = s.parseDateTime(r.Form.Get("expires_at"))
		case accounts.BatchExtend, accounts.BatchReduce:
			input.Duration, err = batchDuration(r.Form.Get("duration"), r.Form.Get("duration_unit"))
		}
	}
	completed := 0
	if err == nil {
		completed, err = s.accounts.Batch(r.Context(), input)
	}
	if err != nil {
		message := "批量操作失败，请检查所选账号和输入后重试"
		if completed > 0 {
			message = fmt.Sprintf("批量操作中断，已完成 %d 个账号；请刷新后检查其余账号", completed)
		}
		s.renderAccounts(w, r, http.StatusBadRequest, message, "")
		return
	}
	http.Redirect(w, r, "/admin/accounts?message="+url.QueryEscape(fmt.Sprintf("已完成 %d 个账号的批量操作", completed)), http.StatusSeeOther)
}

func accountSelections(values []string) ([]int64, map[int64]int64, error) {
	ids := make([]int64, 0, len(values))
	versions := make(map[int64]int64, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return nil, nil, errors.New("invalid account selection")
		}
		id, idErr := strconv.ParseInt(parts[0], 10, 64)
		version, versionErr := strconv.ParseInt(parts[1], 10, 64)
		if idErr != nil || versionErr != nil || id < 1 || version < 1 {
			return nil, nil, errors.New("invalid account selection")
		}
		if _, duplicate := versions[id]; duplicate {
			continue
		}
		ids = append(ids, id)
		versions[id] = version
	}
	return ids, versions, nil
}

func batchDuration(raw, unit string) (time.Duration, error) {
	amount, err := strconv.Atoi(raw)
	if err != nil || amount < 1 || amount > 36500 {
		return 0, errors.New("invalid batch duration")
	}
	multipliers := map[string]time.Duration{"minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, errors.New("invalid batch duration unit")
	}
	return time.Duration(amount) * multiplier, nil
}

func (s *Server) accountEnable(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, version int64) error { return s.accounts.Enable(r.Context(), id, version) })
}
func (s *Server) accountDisable(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, version int64) error { return s.accounts.Disable(r.Context(), id, version) })
}
func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	account, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	s.templates.Render(w, "account-delete-confirm", admin.ViewData{CSRFToken: csrfFromRequest(r), Account: account})
}

func (s *Server) accountDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	s.accountAction(w, r, func(id, _ int64) error { return s.accounts.Delete(r.Context(), id) })
}
func (s *Server) accountAction(w http.ResponseWriter, r *http.Request, action func(int64, int64) error) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	version, versionErr := accountVersion(r)
	if err == nil && versionErr != nil {
		err = versionErr
	}
	if err == nil && id > 0 {
		err = action(id, version)
	}
	if err != nil {
		s.renderAccounts(w, r, http.StatusBadRequest, accountError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}

func accountVersion(r *http.Request) (int64, error) {
	raw := r.Form.Get("version")
	if raw == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("invalid account version")
	}
	return version, nil
}
func (s *Server) renderAccounts(w http.ResponseWriter, r *http.Request, status int, errorMessage, message string) {
	list, err := s.accounts.List(r.Context())
	if err != nil {
		http.Error(w, "load accounts", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "accounts", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, Accounts: list}, status)
}
func accountError(err error) string {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		return "账号不存在"
	case errors.Is(err, accounts.ErrInvalidUsername):
		return "请输入用户名"
	case errors.Is(err, accounts.ErrInvalidPassword):
		return "密码至少需要 8 个字符"
	case errors.Is(err, accounts.ErrExpiredAccount):
		return "已到期账号不能启用；请先续费"
	case errors.Is(err, accounts.ErrConflict):
		return "账号已被其他操作更新，请刷新后重试"
	default:
		return "操作失败，请检查 Emby 连接及输入后重试"
	}
}

func (s *Server) inviteList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderInvites(w, r, http.StatusOK, "", "")
}
func (s *Server) inviteCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	duration, durationErr := strconv.Atoi(r.Form.Get("duration"))
	maxUses, usesErr := strconv.Atoi(r.Form.Get("max_uses"))
	multipliers := map[string]int{"minute": 1, "hour": 60, "day": 24 * 60}
	multiplier, unitValid := multipliers[r.Form.Get("duration_unit")]
	created, err := s.invites.Create(r.Context(), invites.CreateInput{DurationMinutes: duration * multiplier, MaxUses: maxUses, Note: r.Form.Get("note")})
	if durationErr != nil || usesErr != nil || !unitValid {
		err = invites.ErrInvalidDuration
	}
	if err != nil {
		s.renderInvites(w, r, http.StatusBadRequest, inviteError(err), "")
		return
	}
	s.renderInvites(w, r, http.StatusCreated, "", created.Code)
}
func (s *Server) inviteToggle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	enabled := r.Form.Get("enabled") == "true"
	if err == nil {
		err = s.invites.SetEnabled(r.Context(), id, enabled)
	}
	if err != nil {
		s.renderInvites(w, r, http.StatusBadRequest, inviteError(err), "")
		return
	}
	http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
}
func (s *Server) inviteDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.invites.Delete(r.Context(), id)
	}
	if err != nil {
		s.renderInvites(w, r, http.StatusBadRequest, "邀请码已被使用时请改为禁用", "")
		return
	}
	http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
}
func (s *Server) renderInvites(w http.ResponseWriter, r *http.Request, status int, errorMessage, message string) {
	list, err := s.invites.List(r.Context())
	if err != nil {
		http.Error(w, "load invites", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "invites", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, Invites: list}, status)
}
func inviteError(err error) string {
	if errors.Is(err, invites.ErrInvalidDuration) {
		return "时长必须在 1 到 3650 天之间"
	}
	if errors.Is(err, invites.ErrInvalidMaxUses) {
		return "使用次数不能小于 0"
	}
	return "操作失败"
}
func (s *Server) registerPage(w http.ResponseWriter, r *http.Request) {
	s.templates.Render(w, "register", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.allowAttempt(w, r, s.publicLimit, r.Form.Get("username")) {
		return
	}
	account, err := s.invites.Register(r.Context(), r.Form.Get("code"), r.Form.Get("username"), r.Form.Get("password"))
	if err != nil {
		s.templates.Render(w, "register", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: "注册失败：邀请码不可用或账号信息无效"})
		return
	}
	s.templates.Render(w, "result", admin.ViewData{Message: "账号 " + account.Username + " 注册成功"})
}
func (s *Server) renewPage(w http.ResponseWriter, r *http.Request) {
	s.templates.Render(w, "renew", admin.ViewData{CSRFToken: csrfFromRequest(r)})
}
func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.allowAttempt(w, r, s.publicLimit, r.Form.Get("username")) {
		return
	}
	account, err := s.invites.Renew(r.Context(), r.Form.Get("code"), r.Form.Get("username"), r.Form.Get("password"))
	if err != nil {
		s.templates.Render(w, "renew", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: "续费失败：邀请码不可用或账号不存在"})
		return
	}
	s.templates.Render(w, "result", admin.ViewData{Message: "续费成功，新的到期时间：" + account.ExpiresAt.In(s.timeLocation).Format("2006-01-02 15:04") + " " + s.timeLocation.String()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.loggedIn(r) {
		return true
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	return false
}
func (s *Server) loggedIn(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	return err == nil && s.auth.Authenticated(r.Context(), cookie.Value)
}
func (s *Server) allowAttempt(w http.ResponseWriter, r *http.Request, limiter *ratelimit.Limiter, subject string) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remote = r.RemoteAddr
	}
	allowed, retryAfter := limiter.Allow(remote + "\x00" + strings.ToLower(strings.TrimSpace(subject)))
	if allowed {
		return true
	}
	seconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Error(w, "too many attempts; retry later", http.StatusTooManyRequests)
	return false
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.sessionTTL.Seconds())})
}

func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) ensureCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(csrfCookie); err != nil {
			token, tokenErr := newCSRFToken()
			if tokenErr != nil {
				http.Error(w, "security token unavailable", http.StatusInternalServerError)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int((24 * time.Hour).Seconds())})
			r.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
		}
		next.ServeHTTP(w, r)
	})
}
func newCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func csrfFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func validCSRF(r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	form := r.Form.Get("csrf_token")
	return len(form) == len(cookie.Value) && subtle.ConstantTimeCompare([]byte(form), []byte(cookie.Value)) == 1
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTML pages contain session-scoped state, CSRF tokens, or account data.
		// Static asset handlers deliberately override this with their own cache policy.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

// RequestContext exists to reserve a stable seam for request-scoped metadata.
func RequestContext(r *http.Request) context.Context { return r.Context() }

// LocalURL protects future redirect callers from accepting cross-origin destinations.
func LocalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return raw
}
