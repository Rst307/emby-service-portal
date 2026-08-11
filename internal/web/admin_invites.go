package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Rst307/emby-service-portal/internal/invites"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func (s *Server) inviteList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderInvites(w, r, http.StatusOK, "", "", "")
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
		s.renderInvites(w, r, http.StatusBadRequest, inviteError(err), "", "")
		return
	}
	s.renderInvites(w, r, http.StatusCreated, "", "", created.Code)
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
		s.renderInvites(w, r, http.StatusBadRequest, inviteError(err), "", "")
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
		s.renderInvites(w, r, http.StatusBadRequest, "邀请码已被使用时请改为禁用", "", "")
		return
	}
	http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
}
func (s *Server) renderInvites(w http.ResponseWriter, r *http.Request, status int, errorMessage, message, newCode string) {
	list, err := s.invites.List(r.Context())
	if err != nil {
		http.Error(w, "load invites", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "invites", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, Invites: list, NewInviteCode: newCode}, status)
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
