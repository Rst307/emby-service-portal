package admin

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
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
	templates.Render(response, "accounts", ViewData{Accounts: []domain.Account{
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
	templates.Render(response, "invites", ViewData{NewInviteCode: "ESP-test-code-123", Invites: []domain.InviteCode{
		{ID: 1, CodePrefix: "ESP-TEST", Code: "ESP-test-code-123", DurationMinutes: 30 * 24 * 60, MaxUses: 1, UsedCount: 1, Enabled: true, Redemptions: []domain.InviteRedemption{{AccountUsername: "alice", Kind: "register", RedeemedAt: time.Date(2030, 1, 2, 3, 4, 0, 0, time.UTC)}}},
		{ID: 2, CodePrefix: "ESP-OLD", DurationMinutes: 45, MaxUses: 0, Enabled: false},
	}})
	page := response.Body.String()
	for _, marker := range []string{"邀请码已创建", "ESP-test-code-123", "30 天", "45 分钟", "alice", "注册使用", "2030-01-02 03:04", "data-confirm=\"确认删除此邀请码？\""} {
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
	templates.Render(response, "portal-dashboard", ViewData{Account: domain.Account{Username: "alice", Status: "expired", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Note: "VIP"}})
	page := response.Body.String()
	for _, marker := range []string{"你好，alice", "badge expired\">已过期", "2030-01-01 00:00", "href=\"/purchase\">购买激活码", "href=\"/renew\">续费订阅", "需要办理什么？", "退出登录"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("portal dashboard missing %q: %s", marker, page)
		}
	}
}

func TestRenderRenewHidesCredentialsForPortalAccount(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "renew", ViewData{
		Account:      domain.Account{ID: 7, Username: "alice", Status: "active"},
		RenewalPlans: []domain.PaymentPlan{{ID: 3, Kind: "renewal", Name: "月度续费", DurationDays: 30, PriceFen: 990, Enabled: true}},
	})
	page := response.Body.String()
	for _, marker := range []string{"当前登录账号无需再次输入用户名和密码", "当前账号：alice", "无需再次输入用户名和密码", "为当前账号续费"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("authenticated renewal page missing %q: %s", marker, page)
		}
	}
	if strings.Contains(page, `name="username"`) || strings.Contains(page, `name="password"`) {
		t.Fatalf("authenticated renewal page still asks for credentials: %s", page)
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
	templates.Render(response, "accounts", ViewData{Accounts: []domain.Account{{ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}})
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
		ActivationPlans: []domain.PaymentPlan{{ID: 1, Kind: "activation", Name: "月卡激活", DurationDays: 30, PriceFen: 990, Enabled: true}},
		RenewalPlans:    []domain.PaymentPlan{{ID: 2, Kind: "renewal", Name: "季度续费", DurationDays: 90, PriceFen: 2490, Enabled: false}},
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
	templates.Render(response, "plan-edit", ViewData{CSRFToken: "csrf", PlanEdit: PlanEditData{Plan: domain.PaymentPlan{ID: 2, Kind: "renewal", Name: "季度续费", DurationDays: 90, PriceFen: 2490, Enabled: true}}})
	page := response.Body.String()
	for _, marker := range []string{"把方案信息写清楚", "方案名称", "订阅时长（天）", "售价（元）", "保存方案", "当前方案", "已经创建的订单"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("plan edit page missing %q: %s", marker, page)
		}
	}
}

func TestRenderOrdersShowsBuyerAndSearchControls(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "orders", ViewData{
		CSRFToken: "csrf", PaymentOrders: []domain.PaymentOrder{{ID: 7, MerchantOrderNo: "ESP-ORDER-7", PublicToken: "token-7", Kind: "activation", PlanName: "月卡", DurationMinutes: 30 * 24 * 60, BuyerInfo: "张三 / wx-z3", AmountFen: 990, Currency: "CNY", PaymentStatus: "paid", FulfillmentStatus: "completed", CreatedAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}, OrderTotal: 1, OrderPaidCount: 1, OrderPaidFen: 990, OrderPage: 1, OrderPageSize: 20,
	})
	page := response.Body.String()
	for _, marker := range []string{"支付订单", "订单号、商品、购买人", "张三 / wx-z3", "ESP-ORDER-7", "已付款", "查询订单", "商品类型", "/payment/token-7"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("orders page missing %q: %s", marker, page)
		}
	}
}

func TestRenderPaymentShowsCheckoutAndActivationCode(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "payment", ViewData{PaymentOrder: domain.PaymentOrder{
		PublicToken: "token", MerchantOrderNo: "ESP-ORDER-1", Kind: "activation", PlanName: "月卡", AmountFen: 990,
		PaymentStatus: "paid", FulfillmentStatus: "completed", PaymentURL: "https://pay.example/checkout", ActivationCode: "ESP-ACT-test",
	}})
	page := response.Body.String()
	for _, marker := range []string{"微信支付", "¥9.90", "打开微信收银台", "ESP-ACT-test", "data-payment-page", "data-payment-token=\"token\""} {
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
	for _, marker := range []string{"显示时区", "保存设置", `value="Asia/Shanghai" selected`, "UTC&#43;08:00", "当前显示时区：Asia/Shanghai", "__custom__", "data-time-zone-select", "data-custom-time-zone", "支付后跳转地址（可选）", "/admin/settings"} {
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

func TestRenderPortalRequestShowsSearchAndRequestButton(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "portal-request", ViewData{
		CSRFToken: "csrf", Account: domain.Account{Username: "alice", Status: "active"}, PortalActive: "request",
		TmdbConfigured: true, RequestQuery: "星际穿越",
		SearchResults: []requests.SearchItem{
			{Result: tmdb.Result{ID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar", PosterPath: "/a.jpg", ReleaseDate: "2014-11-05", Overview: "地球荒芜"}},
			{Result: tmdb.Result{ID: 111, MediaType: "movie", Title: "教父", PosterPath: ""}, AlreadyRequested: true, RequestStatus: "pending"},
			{Result: tmdb.Result{ID: 1399, MediaType: "tv", Title: "权力的游戏", PosterPath: ""}, InLibrary: true},
		},
	})
	page := response.Body.String()
	for _, marker := range []string{"求剧", "搜索影视", "name=\"q\" value=\"星际穿越\"", "星际穿越", "电影", "剧集", "TMDB #157336", "type=\"submit\">求剧", "已在库", "已求剧", "MEMBER ACCESS", "退出登录"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("portal-request missing %q: %s", marker, page)
		}
	}
	if !strings.Contains(page, `value="157336"`) || !strings.Contains(page, `name="media_type" value="movie"`) {
		t.Fatalf("portal-request form lacks tmdb fields: %s", page)
	}
	if strings.Contains(page, `<button class="seerr-btn seerr-btn-primary" type="submit">求剧</button>`) {
		return
	}
	// InLibrary results must never offer the request button.
	if idx := strings.Index(page, "已在库"); idx >= 0 {
		if strings.Contains(page[idx:], "type=\"submit\">求剧") {
			t.Fatalf("in-library item still offers request: %s", page[idx:idx+400])
		}
	}
}

func TestRenderPortalRequestNotConfiguredShowsNotice(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "portal-request", ViewData{Account: domain.Account{Username: "alice"}, TmdbConfigured: false, PortalActive: "request", CSRFToken: "csrf"})
	page := response.Body.String()
	for _, marker := range []string{"求剧功能尚未启用", "ESP_TMDB_API_KEY"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("portal-request missing %q: %s", marker, page)
		}
	}
}

func TestRenderAdminRequestsShowsRecordsAndActions(t *testing.T) {
	templates, err := NewTemplates(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "requests", ViewData{
		CSRFToken: "csrf", RequestEnabled: true,
		MediaRequests: []domain.MediaRequest{
			{ID: 7, AccountUsername: "alice", AccountID: 1, TmdbID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar", PosterPath: "/a.jpg", ReleaseDate: "2014-11-05", Status: "pending", CreatedAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: 8, AccountUsername: "bob", AccountID: 2, TmdbID: 1399, MediaType: "tv", Title: "权力的游戏", Status: "fulfilled", CreatedAt: time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)},
		},
		RequestTotal: 2, RequestPending: 1, RequestFulfilled: 1, RequestPage: 1, RequestPageSize: 20, RequestTotalPages: 1,
	})
	page := response.Body.String()
	for _, marker := range []string{"求剧管理", "星际穿越", "Interstellar", "alice", "bob", "TMDB 编号", "#157336", "themoviedb.org/movie/157336", "待处理", "已入库", "标记已入库", "驳回", "删除", "确认删除这条求剧记录？", "求剧总数", "查询求剧"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("requests page missing %q: %s", marker, page)
		}
	}
	if !strings.Contains(page, "/admin/requests/7/fulfill") || !strings.Contains(page, "/admin/requests/7/reject") || !strings.Contains(page, "/admin/requests/8/delete") {
		t.Fatalf("requests page lacks action forms: %s", page)
	}
	if strings.Contains(page, "当前未配置 TMDB API Key") {
		t.Fatalf("enabled requests page still shows the not-configured banner: %s", page[:300])
	}
}
