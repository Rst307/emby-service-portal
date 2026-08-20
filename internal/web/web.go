// Package web provides the HTTP delivery layer.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/accounts"
	"github.com/Rst307/emby-service-portal/internal/auth"
	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/invites"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/portal"
	"github.com/Rst307/emby-service-portal/internal/ratelimit"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/settings"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

const sessionCookie = "eum_admin_session"
const userSessionCookie = "eum_user_session"
const csrfCookie = "eum_csrf"

type Server struct {
	auth         *auth.Service
	portal       *portal.Service
	accounts     *accounts.Service
	invites      *invites.Service
	payments     *payments.Service
	requests     *requests.Service
	tmdb         *tmdb.Client
	settings     *settings.Service
	apiKey       string
	templates    *admin.Templates
	cookieSecure bool
	sessionTTL   time.Duration
	loginLimit   *ratelimit.Limiter
	publicLimit  *ratelimit.Limiter
	requestLimit *ratelimit.Limiter
}

func New(authService *auth.Service, portalService *portal.Service, accountService *accounts.Service, inviteService *invites.Service, paymentService *payments.Service, settingsService *settings.Service, requestService *requests.Service, tmdbClient *tmdb.Client, apiKey string, cookieSecure bool, sessionTTL time.Duration, timeLocation *time.Location) (*Server, error) {
	if timeLocation == nil {
		timeLocation, _ = time.LoadLocation("Asia/Shanghai")
	}
	templates, err := admin.NewTemplates(timeLocation)
	if err != nil {
		return nil, err
	}
	return &Server{
		auth: authService, portal: portalService, accounts: accountService, invites: inviteService, payments: paymentService,
		requests: requestService, tmdb: tmdbClient, settings: settingsService, apiKey: apiKey, templates: templates, cookieSecure: cookieSecure, sessionTTL: sessionTTL,
		loginLimit: ratelimit.New(10, time.Minute), publicLimit: ratelimit.New(20, time.Minute), requestLimit: ratelimit.New(20, time.Hour),
	}, nil
}

// displayZone returns the runtime-configured display time zone name and location.
func (s *Server) displayZone(r *http.Request) (string, *time.Location) {
	return s.settings.DisplayTimeZone(r.Context())
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
	mux.HandleFunc("GET /admin/plans", s.planList)
	mux.HandleFunc("GET /admin/orders", s.orderList)
	mux.HandleFunc("GET /admin/requests", s.adminRequestList)
	mux.HandleFunc("POST /admin/requests/{id}/fulfill", s.adminRequestSetStatus(domain.MediaRequestFulfilled))
	mux.HandleFunc("POST /admin/requests/{id}/reject", s.adminRequestSetStatus(domain.MediaRequestRejected))
	mux.HandleFunc("POST /admin/requests/{id}/delete", s.adminRequestDelete)
	mux.HandleFunc("POST /admin/plans", s.planCreate)
	mux.HandleFunc("GET /admin/plans/{id}/edit", s.planEdit)
	mux.HandleFunc("POST /admin/plans/{id}/update", s.planUpdate)
	mux.HandleFunc("POST /admin/plans/{id}/toggle", s.planToggle)
	mux.HandleFunc("POST /admin/plans/{id}/delete", s.planDelete)
	mux.HandleFunc("GET /admin/settings", s.adminSettingsPage)
	mux.HandleFunc("POST /admin/settings", s.adminSettingsUpdate)
	mux.HandleFunc("POST /admin/settings/payment", s.adminPaymentSettingsUpdate)
	mux.HandleFunc("GET /portal/login", s.portalLoginPage)
	mux.HandleFunc("POST /portal/login", s.portalLogin)
	mux.HandleFunc("GET /portal/", s.portalDashboard)
	mux.HandleFunc("GET /portal/request", s.portalRequestPage)
	mux.HandleFunc("POST /portal/request", s.portalRequestCreate)
	mux.HandleFunc("POST /portal/logout", s.portalLogout)
	mux.HandleFunc("GET /login", s.portalLoginPage)
	mux.HandleFunc("POST /login", s.portalLogin)
	mux.HandleFunc("GET /user/", s.portalDashboard)
	mux.HandleFunc("POST /user/logout", s.portalLogout)
	mux.HandleFunc("GET /purchase", s.purchasePage)
	mux.HandleFunc("POST /purchase", s.purchaseCreate)
	mux.HandleFunc("GET /payment/{token}", s.paymentPage)
	mux.HandleFunc("GET /payment/{token}/status", s.paymentStatus)
	mux.HandleFunc("POST /webhooks/wxpay-payment-center", s.paymentWebhook)
	mux.HandleFunc("GET /register", s.registerPage)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /renew", s.renewPage)
	mux.HandleFunc("POST /renew", s.renew)
	mux.HandleFunc("POST /renew/payment", s.renewPaymentCreate)
	mux.HandleFunc("POST /admin/logout", s.logout)
	return securityHeaders(s.limitBody(s.ensureCSRF(mux)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
func (s *Server) apiHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "dev"})
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
func (s *Server) parseDateTime(r *http.Request, value string) (time.Time, error) {
	_, location := s.displayZone(r)
	return time.ParseInLocation("2006-01-02T15:04", value, location)
}

func (s *Server) parseAccountDateTime(r *http.Request, value, original string) (time.Time, error) {
	_, location := s.displayZone(r)
	return parseAccountDateTimeIn(value, original, location)
}

// parseAccountDateTimeIn preserves the original instant when the edited value
// matches the original rendered in the display time zone (round trip across a
// DST fall-back would otherwise shift the instant).
func parseAccountDateTimeIn(value, original string, location *time.Location) (time.Time, error) {
	if originalTime, err := time.Parse(time.RFC3339Nano, original); err == nil && originalTime.In(location).Format("2006-01-02T15:04") == value {
		return originalTime, nil
	}
	return time.ParseInLocation("2006-01-02T15:04", value, location)
}

func accountID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid account ID")
	}
	return id, nil
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
		if r.URL.Path == "/webhooks/wxpay-payment-center" {
			next.ServeHTTP(w, r)
			return
		}
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

func newCSPNonce() (string, error) {
	bytes := make([]byte, 16)
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
		nonce, err := newCSPNonce()
		if err != nil {
			http.Error(w, "security policy unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"' https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline'; connect-src 'self' https://cloudflareinsights.com; img-src 'self' data: https://image.tmdb.org")
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
