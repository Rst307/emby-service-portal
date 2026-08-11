package admin

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

func TestRenderDashboardShowsAccountStats(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "dashboard", ViewData{AccountCount: 5, ActiveCount: 2, DisabledCount: 1, ExpiredCount: 2, InviteCount: 3})
	page := response.Body.String()
	for _, marker := range []string{"业务账号", ">5<", "活跃", ">2<", "已禁用", "已过期", "邀请码", ">3<", "退出登录"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("dashboard missing %q: %s", marker, page)
		}
	}
}

func TestRenderAccountsUsesChineseStatusLabels(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "accounts", ViewData{Accounts: []sqlite.Account{
		{ID: 1, Version: 2, Username: "alice", Status: "active", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Note: ""},
		{ID: 2, Version: 1, Username: "bob", Status: "disabled", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: 3, Version: 1, Username: "carol", Status: "expired", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
	}})
	page := response.Body.String()
	for _, marker := range []string{"badge active\">活跃", "badge disabled\">已禁用", "badge expired\">已过期", "已到期 · 需先续费", "data-edit-account", "edit-account-dialog", "data-expires-at=\"2030-01-01T00:00\""} {
		if !strings.Contains(page, marker) {
			t.Fatalf("accounts page missing %q: %s", marker, page)
		}
	}
	if strings.Contains(page, "badge active\">active") {
		t.Fatalf("accounts page leaked English status: %s", page)
	}
}

func TestRenderInvitesShowsCreatedCodeAndHumanizedDuration(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "invites", ViewData{NewInviteCode: "EUM-test-code-123", Invites: []sqlite.InviteCode{
		{ID: 1, CodePrefix: "EUM-TEST", Code: "EUM-test-code-123", DurationMinutes: 30 * 24 * 60, MaxUses: 1, UsedCount: 1, Enabled: true},
		{ID: 2, CodePrefix: "EUM-OLD", DurationMinutes: 45, MaxUses: 0, Enabled: false},
	}})
	page := response.Body.String()
	for _, marker := range []string{"邀请码已创建", "EUM-test-code-123", "30 天", "45 分钟", "data-confirm=\"确认删除此邀请码？\""} {
		if !strings.Contains(page, marker) {
			t.Fatalf("invites page missing %q: %s", marker, page)
		}
	}
	if strings.Contains(page, "43200 分钟") {
		t.Fatalf("invites page did not humanize duration: %s", page)
	}
}

func TestRenderPortalDashboardShowsChineseStatus(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "portal-dashboard", ViewData{Account: sqlite.Account{Username: "alice", Status: "expired", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Note: "VIP"}})
	page := response.Body.String()
	for _, marker := range []string{"你好，alice", "badge expired\">已过期", "2030-01-01 00:00", "退出登录"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("portal dashboard missing %q: %s", marker, page)
		}
	}
}

func TestRenderFormatsTimesInConfiguredLocation(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewTemplates(location)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "accounts", ViewData{Accounts: []sqlite.Account{{ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}})
	page := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(page, "2030-01-01 08:00") || !strings.Contains(page, "data-expires-at=\"2030-01-01T08:00\"") || !strings.Contains(page, "Asia/Shanghai") {
		t.Fatalf("configured time zone was not rendered: %s", page)
	}
}

func TestRenderStatusUsesRequestedStatusAfterSuccessfulExecution(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}complete{{end}}`))}
	response := httptest.NewRecorder()
	templates.RenderStatus(response, "page", ViewData{}, http.StatusUnauthorized)

	if response.Code != http.StatusUnauthorized || response.Body.String() != "complete" {
		t.Fatalf("response = (%d, %q), want (401, complete)", response.Code, response.Body.String())
	}
}

func TestRenderPlansShowsBothSaleKinds(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "plans", ViewData{
		ActivationPlans: []sqlite.PaymentPlan{{ID: 1, Kind: "activation", Name: "月卡激活", DurationDays: 30, PriceFen: 990, Enabled: true}},
		RenewalPlans:    []sqlite.PaymentPlan{{ID: 2, Kind: "renewal", Name: "季度续费", DurationDays: 90, PriceFen: 2490, Enabled: false}},
	})
	page := response.Body.String()
	for _, marker := range []string{"售卖方案", "月卡激活", "¥9.90", "季度续费", "¥24.90", "已下架", "/admin/plans/2/toggle", "/admin/plans/2/edit", "/admin/plans/2/delete"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("plans page missing %q: %s", marker, page)
		}
	}
}

func TestRenderPlanEditShowsFocusedEditor(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "plan-edit", ViewData{CSRFToken: "csrf", PlanEdit: PlanEditData{Plan: sqlite.PaymentPlan{ID: 2, Kind: "renewal", Name: "季度续费", DurationDays: 90, PriceFen: 2490, Enabled: true}}})
	page := response.Body.String()
	for _, marker := range []string{"把方案信息写清楚", "方案名称", "订阅时长（天）", "售价（元）", "保存方案", "当前方案", "已经创建的订单"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("plan edit page missing %q: %s", marker, page)
		}
	}
}

func TestRenderPaymentShowsCheckoutAndActivationCode(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "payment", ViewData{PaymentOrder: sqlite.PaymentOrder{
		PublicToken: "token", MerchantOrderNo: "EUM-ORDER-1", Kind: "activation", PlanName: "月卡", AmountFen: 990,
		PaymentStatus: "paid", FulfillmentStatus: "completed", PaymentURL: "https://pay.example/checkout", ActivationCode: "EUM-ACT-test",
	}})
	page := response.Body.String()
	for _, marker := range []string{"微信支付", "¥9.90", "打开微信收银台", "EUM-ACT-test", "data-payment-page", "data-payment-token=\"token\""} {
		if !strings.Contains(page, marker) {
			t.Fatalf("payment page missing %q: %s", marker, page)
		}
	}
}

func TestRenderSettingsShowsSelectedTimeZone(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "settings", ViewData{
		TimeZone:    "Asia/Shanghai",
		TimeZoneNow: "2026-08-07 14:30:00",
		TimeZoneOptions: []TimeZoneOption{
			{Name: "Asia/Shanghai", Label: "Asia/Shanghai（UTC+08:00）"},
			{Name: "UTC", Label: "UTC（UTC+00:00）"},
		},
	})
	page := response.Body.String()
	for _, marker := range []string{"显示时区", "保存设置", `value="Asia/Shanghai" selected`, "UTC&#43;08:00", "当前显示时区：Asia/Shanghai", "__custom__", "data-time-zone-select", "data-custom-time-zone", "/admin/settings"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("settings page missing %q: %s", marker, page)
		}
	}
}

func TestRenderStatusReturns500InsteadOfRequestedStatusOnExecutionFailure(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}partial {{.Missing}}{{end}}`))}
	response := httptest.NewRecorder()
	templates.RenderStatus(response, "page", ViewData{}, http.StatusBadRequest)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "partial") {
		t.Fatalf("response leaked partial template: %q", response.Body.String())
	}
}

func TestRenderDoesNotCommitPartialTemplateOnExecutionFailure(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}partial {{.Missing}}{{end}}`))}
	response := httptest.NewRecorder()
	templates.Render(response, "page", ViewData{})

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "partial") {
		t.Fatalf("response leaked partial template: %q", response.Body.String())
	}
}
