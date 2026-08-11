package web

import (
	"net/http"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
	"github.com/emby-user-manager/emby-user-manager/internal/web/admin"
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

// commonTimeZones lists the zones offered on the settings page, most likely
// ones first. Any other valid IANA zone can still be entered as a custom value.
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
	s.renderRenewPage(w, r, http.StatusOK, "")
}

func (s *Server) renderRenewPage(w http.ResponseWriter, r *http.Request, status int, errorMessage string) {
	plans, _ := s.payments.ListPlans(r.Context(), "renewal", true)
	account, _ := s.portalAccount(r)
	s.templates.RenderStatus(w, "renew", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, RenewalPlans: plans, Account: account}, status)
}
func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	portalAccount, authenticated := s.portalAccount(r)
	username := r.Form.Get("username")
	password := r.Form.Get("password")
	if authenticated {
		username = portalAccount.Username
		password = ""
	}
	if !s.allowAttempt(w, r, s.publicLimit, username) {
		return
	}
	var account sqlite.Account
	var err error
	if authenticated {
		account, err = s.invites.RenewForAccount(r.Context(), r.Form.Get("code"), portalAccount.ID)
	} else {
		account, err = s.invites.Renew(r.Context(), r.Form.Get("code"), username, password)
	}
	if err != nil {
		s.renderRenewPage(w, r, http.StatusBadRequest, "续费失败：邀请码不可用或账号不存在")
		return
	}
	_, location := s.displayZone(r)
	s.templates.Render(w, "result", admin.ViewData{Message: "续费成功，新的到期时间：" + account.ExpiresAt.In(location).Format("2006-01-02 15:04") + " " + location.String()})
}
