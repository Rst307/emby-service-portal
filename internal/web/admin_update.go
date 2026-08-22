package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

// updateViewData returns the settings page data with the update panel filled.
func (s *Server) updateViewData(r *http.Request, data admin.ViewData) admin.ViewData {
	data.Update = s.updater.Snapshot(r.Context())
	if data.CSRFToken == "" {
		data.CSRFToken = csrfFromRequest(r)
	}
	return data
}

// adminUpdateSettingsUpdate persists the auto-update flag from the 系统设置
// page.
func (s *Server) adminUpdateSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	enabled := r.Form.Get("auto_update") == "1" || strings.EqualFold(r.Form.Get("auto_update"), "on")
	if err := s.updater.SetAuto(r.Context(), enabled); err != nil {
		s.templates.RenderStatus(w, "settings", s.updateViewData(r, admin.ViewData{Error: err.Error()}), http.StatusBadRequest)
		return
	}
	message := "自动更新已关闭"
	if enabled {
		message = "自动更新已开启：后台每 " + s.updater.Interval().String() + " 检测一次，发现新版本将自动安装并重启"
	}
	http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape(message), http.StatusSeeOther)
}

// adminUpdateCheck triggers an immediate release check and reports the result.
func (s *Server) adminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := s.updater.Check(r.Context()); err != nil {
		s.templates.RenderStatus(w, "settings", s.updateViewData(r, admin.ViewData{Error: "检查更新失败：" + err.Error()}), http.StatusBadGateway)
		return
	}
	state := s.updater.Snapshot(r.Context())
	message := "已是最新版本 " + state.CurrentVersion
	if state.Latest != nil && state.Latest.Version != state.CurrentVersion {
		message = "发现新版本 " + state.Latest.Version
		if state.CheckError != "" {
			message += "（" + state.CheckError + "）"
		}
	}
	http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape(message), http.StatusSeeOther)
}

// adminUpdateApply downloads and installs the newest release, then asks the
// process to restart. The response is flushed before the restart is requested
// so the admin gets the confirmation page.
func (s *Server) adminUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	_, err := s.updater.Apply(r.Context())
	if err != nil {
		s.templates.RenderStatus(w, "settings", s.updateViewData(r, admin.ViewData{Error: "更新失败：" + err.Error()}), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape("更新已应用，服务正在重启…请稍后刷新页面"), http.StatusSeeOther)
	if s.notifyRestart != nil {
		s.notifyRestart()
	}
}
