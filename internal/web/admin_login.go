package web

import (
	"net/http"

	"github.com/Rst307/emby-service-portal/internal/auth"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

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

var commonTimeZones = []string{
	"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Taipei", "Asia/Tokyo", "Asia/Seoul",
	"Asia/Singapore", "Asia/Kuala_Lumpur", "Asia/Jakarta", "Asia/Bangkok", "Asia/Ho_Chi_Minh",
	"Asia/Manila", "Asia/Kolkata", "Asia/Dubai", "Asia/Jerusalem",
	"Europe/London", "Europe/Paris", "Europe/Berlin", "Europe/Moscow",
	"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles", "America/Sao_Paulo",
	"Australia/Sydney", "Pacific/Auckland", "UTC",
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
