package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/web/admin"
)

func timeZoneOption(name string) admin.TimeZoneOption {
	label := name
	if location, err := time.LoadLocation(name); err == nil {
		label = name + "（" + time.Now().In(location).Format("UTC-07:00") + "）"
	}
	return admin.TimeZoneOption{Name: name, Label: label}
}

// timeZoneOptions returns the curated zone list plus the current zone when it
// is not part of the list, so a previously custom zone stays selectable.
func timeZoneOptions(current string) []admin.TimeZoneOption {
	options := make([]admin.TimeZoneOption, 0, len(commonTimeZones)+1)
	seen := false
	for _, name := range commonTimeZones {
		options = append(options, timeZoneOption(name))
		if name == current {
			seen = true
		}
	}
	if current != "" && !seen {
		options = append(options, timeZoneOption(current))
	}
	return options
}
func (s *Server) adminSettingsPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name, location := s.displayZone(r)
	paymentSettings, paymentErr := s.payments.Settings(r.Context())
	errorMessage := ""
	if paymentErr != nil {
		errorMessage = "读取支付设置失败：" + paymentErr.Error()
	}
	s.templates.Render(w, "settings", admin.ViewData{
		CSRFToken:       csrfFromRequest(r),
		Error:           errorMessage,
		Message:         r.URL.Query().Get("message"),
		TimeZone:        name,
		TimeZoneNow:     time.Now().In(location).Format("2006-01-02 15:04:05"),
		TimeZoneOptions: timeZoneOptions(name),
		PaymentSettings: paymentSettings,
	})
}

func (s *Server) adminSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.Form.Get("time_zone"))
	if name == "__custom__" {
		name = strings.TrimSpace(r.Form.Get("custom_time_zone"))
	}
	if err := s.settings.SetDisplayTimeZone(r.Context(), name); err != nil {
		current, location := s.displayZone(r)
		paymentSettings, _ := s.payments.Settings(r.Context())
		s.templates.RenderStatus(w, "settings", admin.ViewData{
			CSRFToken:       csrfFromRequest(r),
			Error:           "保存失败：" + err.Error(),
			TimeZone:        current,
			TimeZoneNow:     time.Now().In(location).Format("2006-01-02 15:04:05"),
			TimeZoneOptions: timeZoneOptions(current),
			PaymentSettings: paymentSettings,
		}, http.StatusBadRequest)
		return
	}
	if _, location := s.displayZone(r); location != nil {
		s.templates.SetLocation(location)
	}
	http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape("显示时区已更新为 "+name), http.StatusSeeOther)
}

func (s *Server) adminPaymentSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	ttl, err := strconv.Atoi(strings.TrimSpace(r.Form.Get("order_ttl_minutes")))
	if err != nil {
		ttl = 0
	}
	err = s.payments.UpdateSettings(r.Context(), payments.UpdatePaymentSettingsInput{
		BaseURL: r.Form.Get("base_url"), AppID: r.Form.Get("app_id"), AppSecret: r.Form.Get("app_secret"),
		CallbackURL: r.Form.Get("callback_url"), ReturnURL: r.Form.Get("return_url"), OrderTTLMinutes: ttl,
	})
	if err != nil {
		name, location := s.displayZone(r)
		paymentSettings, _ := s.payments.Settings(r.Context())
		s.templates.RenderStatus(w, "settings", admin.ViewData{
			CSRFToken: csrfFromRequest(r), Error: "支付设置保存失败：" + err.Error(), TimeZone: name,
			TimeZoneNow: time.Now().In(location).Format("2006-01-02 15:04:05"), TimeZoneOptions: timeZoneOptions(name), PaymentSettings: paymentSettings,
		}, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/settings?message="+url.QueryEscape("支付中心设置已保存"), http.StatusSeeOther)
}
