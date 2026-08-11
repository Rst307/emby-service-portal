// Package web public pages: the anonymous-facing portal surface (landing,
// registration, renewal and result pages).
package web

import (
	"net/http"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	s.templates.Render(w, "home", admin.ViewData{CSRFToken: csrfFromRequest(r)})
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
	var account domain.Account
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
