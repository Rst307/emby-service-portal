package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/auth"
	"github.com/emby-user-manager/emby-user-manager/internal/invites"
	"github.com/emby-user-manager/emby-user-manager/internal/payments"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
	"github.com/emby-user-manager/emby-user-manager/internal/web/admin"
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

func (s *Server) planList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderPlans(w, r, http.StatusOK, "", r.URL.Query().Get("message"))
}

func (s *Server) orderList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		page = 1
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	status := normalizePaymentOrderStatus(r.URL.Query().Get("status"))
	kind := normalizePaymentOrderKind(r.URL.Query().Get("kind"))
	result, err := s.payments.ListOrders(r.Context(), sqlite.PaymentOrderFilter{Query: query, Status: status, Kind: kind, Page: page, PageSize: 20})
	if err != nil {
		http.Error(w, "load payment orders", http.StatusInternalServerError)
		return
	}
	filterValues := url.Values{}
	if query != "" {
		filterValues.Set("q", query)
	}
	if status != "" {
		filterValues.Set("status", status)
	}
	if kind != "" {
		filterValues.Set("kind", kind)
	}
	data := admin.ViewData{
		CSRFToken: csrfFromRequest(r), Message: r.URL.Query().Get("message"),
		PaymentOrders: result.Orders, OrderTotal: result.Total, OrderPaidCount: result.PaidCount, OrderPaidFen: result.PaidAmountFen,
		OrderPage: result.Page, OrderPageSize: result.PageSize, OrderTotalPages: result.TotalPages,
		OrderQuery: query, OrderFilterQuery: filterValues.Encode(), OrderStatus: status, OrderKind: kind,
	}
	s.templates.Render(w, "orders", data)
}

func normalizePaymentOrderStatus(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "pending", "paid", "expired", "canceled", "failed":
		return value
	default:
		return ""
	}
}

func normalizePaymentOrderKind(value string) string {
	value = strings.TrimSpace(value)
	if value == payments.KindActivation || value == payments.KindRenewal {
		return value
	}
	return ""
}

func (s *Server) renderPlans(w http.ResponseWriter, r *http.Request, status int, errorMessage, message string) {
	activation, activationErr := s.payments.ListPlans(r.Context(), payments.KindActivation, false)
	renewal, renewalErr := s.payments.ListPlans(r.Context(), payments.KindRenewal, false)
	if activationErr != nil || renewalErr != nil {
		http.Error(w, "load payment plans", http.StatusInternalServerError)
		return
	}
	s.templates.RenderStatus(w, "plans", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, Message: message, ActivationPlans: activation, RenewalPlans: renewal}, status)
}

func (s *Server) planCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	price, err := parsePriceFen(r.Form.Get("price"))
	days, daysErr := strconv.Atoi(strings.TrimSpace(r.Form.Get("duration_days")))
	if err == nil {
		err = daysErr
	}
	if err == nil {
		_, err = s.payments.CreatePlan(r.Context(), sqlite.CreatePaymentPlanInput{Kind: r.Form.Get("kind"), Name: r.Form.Get("name"), DurationDays: days, PriceFen: price, Note: r.Form.Get("note"), SortOrder: 0})
	}
	if err != nil {
		s.renderPlans(w, r, http.StatusBadRequest, "创建套餐失败："+err.Error(), "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("套餐已添加"), http.StatusSeeOther)
}

func planEditData(plan sqlite.PaymentPlan) admin.PlanEditData {
	return admin.PlanEditData{
		Plan:         plan,
		Name:         plan.Name,
		DurationDays: strconv.Itoa(plan.DurationDays),
		Price:        fmt.Sprintf("%d.%02d", plan.PriceFen/100, plan.PriceFen%100),
		Note:         plan.Note,
	}
}

func (s *Server) renderPlanEdit(w http.ResponseWriter, r *http.Request, status int, errorMessage string, data admin.PlanEditData) {
	s.templates.RenderStatus(w, "plan-edit", admin.ViewData{CSRFToken: csrfFromRequest(r), Error: errorMessage, PlanEdit: data}, status)
}

func (s *Server) planEdit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := accountID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	plan, err := s.payments.FindPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load payment plan", http.StatusInternalServerError)
		return
	}
	s.renderPlanEdit(w, r, http.StatusOK, "", planEditData(plan))
}

func (s *Server) planUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	plan, err := s.payments.FindPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load payment plan", http.StatusInternalServerError)
		return
	}
	form := admin.PlanEditData{
		Plan:         plan,
		Name:         r.Form.Get("name"),
		DurationDays: strings.TrimSpace(r.Form.Get("duration_days")),
		Price:        strings.TrimSpace(r.Form.Get("price")),
		Note:         r.Form.Get("note"),
		Submitted:    true,
	}
	price, priceErr := parsePriceFen(form.Price)
	days, daysErr := strconv.Atoi(form.DurationDays)
	if priceErr != nil {
		err = priceErr
	} else if daysErr != nil {
		err = daysErr
	}
	if err == nil {
		_, err = s.payments.UpdatePlan(r.Context(), id, sqlite.UpdatePaymentPlanInput{Name: form.Name, DurationDays: days, PriceFen: price, Note: form.Note, SortOrder: plan.SortOrder})
	}
	if err != nil {
		s.renderPlanEdit(w, r, http.StatusBadRequest, "保存方案失败："+err.Error(), form)
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("方案已保存"), http.StatusSeeOther)
}

func (s *Server) planToggle(w http.ResponseWriter, r *http.Request) {
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
		err = s.payments.SetPlanEnabled(r.Context(), id, enabled)
	}
	if err != nil {
		s.renderPlans(w, r, http.StatusBadRequest, "更新套餐状态失败", "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("套餐状态已更新"), http.StatusSeeOther)
}

func (s *Server) planDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.payments.DeletePlan(r.Context(), id)
	}
	if err != nil {
		message := "删除方案失败"
		if errors.Is(err, sqlite.ErrPaymentPlanInUse) {
			message = "该方案已有支付订单，不能删除；如不再销售，请使用下架"
		} else if errors.Is(err, sql.ErrNoRows) {
			message = "方案不存在或已经删除"
		}
		s.renderPlans(w, r, http.StatusBadRequest, message, "")
		return
	}
	http.Redirect(w, r, "/admin/plans?message="+url.QueryEscape("方案已删除"), http.StatusSeeOther)
}

func parsePriceFen(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return 0, errors.New("价格必须大于 0")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("价格格式无效")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 || (fraction != "" && strings.Trim(fraction, "0123456789") != "") || strings.Trim(parts[0], "0123456789") != "" {
		return 0, errors.New("价格最多保留两位小数")
	}
	if len(fraction) == 0 {
		fraction = "00"
	} else if len(fraction) == 1 {
		fraction += "0"
	}
	whole, err := strconv.Atoi(parts[0])
	if err != nil || whole < 0 {
		return 0, errors.New("价格格式无效")
	}
	cents, err := strconv.Atoi(fraction)
	if err != nil {
		return 0, errors.New("价格格式无效")
	}
	price := whole*100 + cents
	if price < 1 {
		return 0, errors.New("价格必须大于 0")
	}
	return price, nil
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	data := admin.ViewData{CSRFToken: csrfFromRequest(r)}
	if accounts, err := s.accounts.List(r.Context()); err == nil {
		data.AccountCount = len(accounts)
		for _, account := range accounts {
			switch account.Status {
			case "active":
				data.ActiveCount++
			case "disabled":
				data.DisabledCount++
			case "expired":
				data.ExpiredCount++
			}
		}
	}
	if invites, err := s.invites.List(r.Context()); err == nil {
		data.InviteCount = len(invites)
	}
	if orders, err := s.payments.ListOrders(r.Context(), sqlite.PaymentOrderFilter{Page: 1, PageSize: 1}); err == nil {
		data.PaymentOrderCount = orders.Total
		data.PaymentPaidCount = orders.PaidCount
		data.PaymentRevenueFen = orders.PaidAmountFen
	}
	s.templates.Render(w, "dashboard", data)
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
	expiresAt, err := s.parseDateTime(r, r.Form.Get("expires_at"))
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
	expiresAt, err := s.parseDateTime(r, r.Form.Get("expires_at"))
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
	expiresAt, parseErr := s.parseAccountDateTime(r, r.Form.Get("expires_at"), r.Form.Get("expires_at_original"))
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
			input.ExpiresAt, err = s.parseDateTime(r, r.Form.Get("expires_at"))
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
