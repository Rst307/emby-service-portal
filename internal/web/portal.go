package web

import (
	"net/http"

	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

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
