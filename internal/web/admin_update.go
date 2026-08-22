package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// restartNoticeHTML is served while the process exits for a restart. A plain
// redirect would be followed by the browser during the restart window and hit
// a down backend (nginx 502); this page self-refreshes once the service is
// back up.
const restartNoticeHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta http-equiv="refresh" content="5;url=/admin/settings">
<title>更新已应用 · Emby Service Portal</title>
<style>body{font-family:system-ui,'PingFang SC','Microsoft YaHei',sans-serif;background:#0f1220;color:#e8eaf6;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.box{max-width:28rem;padding:2.5rem;border:1px solid rgba(255,255,255,.08);border-radius:16px;background:#171a2e}
h1{font-size:1.15rem;margin:0 0 .7rem}.line{margin:.3rem 0;color:#a9adc9;font-size:.9rem;line-height:1.7}a{color:#93c5fd}</style></head>
<body><div class="box"><h1>更新已应用，服务正在重启…</h1>
<p class="line">新版本已下载并完成 SHA-256 校验，进程即将重启（约 5 秒）。本页会自动刷新到系统设置。</p>
<p class="line">如果 10 秒后仍未恢复，请手动刷新页面。</p>
<p class="line"><a href="/admin/settings">立即前往系统设置</a></p></div></body></html>`

// adminUpdateApply downloads and installs the newest release, then asks the
// process to restart. The success page is flushed before the restart is
// requested so the admin always receives it (a redirect could hit the down
// backend during the restart window).
func (s *Server) adminUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	// The install must not be tied to the browser request: a proxy timeout or
	// tab close must not abort a download/install that is already underway.
	applyContext, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	restart, err := s.updater.Apply(applyContext)
	if err != nil {
		s.templates.RenderStatus(w, "settings", s.updateViewData(r, admin.ViewData{Error: "更新失败：" + err.Error()}), http.StatusInternalServerError)
		return
	}
	if !restart {
		http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape("更新已完成"), http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(restartNoticeHTML))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	time.Sleep(300 * time.Millisecond)
	if s.notifyRestart != nil {
		s.notifyRestart()
	}
}
