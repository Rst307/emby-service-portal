package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/app"
	"github.com/Rst307/emby-service-portal/internal/config"
)

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func TestAdminLoginAndLogoutFlow(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	response := get(t, client, server.URL+"/admin/login")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login page status = %d", response.StatusCode)
	}
	loginPage := body(t, response)
	csrfToken := csrf(t, loginPage)

	response = postForm(t, client, server.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"correct horse battery staple"}, "csrf_token": {csrfToken}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", response.StatusCode, body(t, response))
	}

	response = get(t, client, server.URL+"/admin/")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", response.StatusCode)
	}
	dashboard := body(t, response)
	csrfToken = csrf(t, dashboard)

	response = postForm(t, client, server.URL+"/admin/logout", url.Values{"csrf_token": {csrfToken}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logged out dashboard status = %d", response.StatusCode)
	}
}

func TestLoginRejectsMissingCSRFAndWrongPassword(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	response := postForm(t, client, server.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/login")
	response = postForm(t, client, server.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid credentials status = %d", response.StatusCode)
	}
}

func TestAdminLoginRateLimitsRepeatedAttempts(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	response := get(t, client, server.URL+"/admin/login")
	token := csrf(t, body(t, response))
	for range 10 {
		response = postForm(t, client, server.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {token}})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failed login status = %d, want 401", response.StatusCode)
		}
		_ = body(t, response)
	}
	response = postForm(t, client, server.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {token}})
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate-limited login status = %d, want 429", response.StatusCode)
	}
	retryAfter := response.Header.Get("Retry-After")
	retrySeconds, err := strconv.Atoi(retryAfter)
	if err != nil || retrySeconds < 1 || retrySeconds > 60 {
		t.Fatalf("Retry-After = %q, want remaining seconds in [1, 60]", retryAfter)
	}
	_ = body(t, response)
}

func TestAdminAccountTimeUsesConfiguredTimeZone(t *testing.T) {
	application := testApplicationWithTimeZone(t, "Asia/Shanghai")
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	response := get(t, client, server.URL+"/admin/accounts")
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"shanghai-account"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", "integration-key")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "2029-12-31T16:00:00Z") {
		t.Fatal("configured Shanghai time was not converted to UTC for storage")
	}
}

func TestAdminAccountTimeIsAlwaysInterpretedAsUTC(t *testing.T) {
	originalLocation := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocation })

	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	response := get(t, client, server.URL+"/admin/accounts")
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"utc-account"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}

	response = get(t, client, server.URL+"/admin/accounts")
	if page := body(t, response); !strings.Contains(page, "2030-01-01 00:00") {
		t.Fatalf("UTC expiry was changed by server local timezone: %s", page)
	}
}

func TestAdminCanChangeDisplayTimeZone(t *testing.T) {
	application := testApplicationWithTimeZone(t, "Asia/Shanghai")
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	// 设置页默认选中配置的时区（Asia/Shanghai）
	response := get(t, client, server.URL+"/admin/settings")
	page := body(t, response)
	if !strings.Contains(page, `value="Asia/Shanghai" selected`) || !strings.Contains(page, "设置") || !strings.Contains(page, "保存设置") {
		t.Fatalf("settings page does not show default zone selected: %s", page)
	}

	// 切到 America/New_York，账号页与后台时间输入随之转换
	response = postForm(t, client, server.URL+"/admin/settings", url.Values{"time_zone": {"America/New_York"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update settings status = %d: %s", response.StatusCode, body(t, response))
	}
	page = body(t, get(t, client, server.URL+response.Header.Get("Location")))
	if !strings.Contains(page, "显示时区已更新为 America/New_York") {
		t.Fatalf("settings update notice absent: %s", page)
	}
	response = get(t, client, server.URL+"/admin/accounts")
	page = body(t, response)
	if !strings.Contains(page, "时间以 America/New_York 展示") {
		t.Fatalf("accounts page did not switch display zone: %s", page)
	}
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"ny-account"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}
	// 输入按显示时区（纽约）解释，页面原样展示，API 则以 UTC 保存
	if page := body(t, get(t, client, server.URL+"/admin/accounts")); !strings.Contains(page, "2030-01-01 00:00") || !strings.Contains(page, "America/New_York") {
		t.Fatalf("account expiry not shown in New York time: %s", page)
	}
	if response := body(t, getWithKey(t, http.DefaultClient, server.URL+"/api/v1/accounts", "integration-key")); !strings.Contains(response, "2030-01-01T05:00:00Z") {
		t.Fatalf("account expiry not stored as UTC: %s", response)
	}

	// 自定义时区：选择“自定义”并提供任意 IANA 名称
	page = body(t, get(t, client, server.URL+"/admin/settings"))
	response = postForm(t, client, server.URL+"/admin/settings", url.Values{"time_zone": {"__custom__"}, "custom_time_zone": {"Europe/Paris"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("custom zone update status = %d: %s", response.StatusCode, body(t, response))
	}
	if page := body(t, get(t, client, server.URL+"/admin/accounts")); !strings.Contains(page, "时间以 Europe/Paris 展示") {
		t.Fatalf("accounts page did not switch to custom zone: %s", page)
	}

	// 无效时区被拒绝并保留原设置
	page = body(t, get(t, client, server.URL+"/admin/settings"))
	response = postForm(t, client, server.URL+"/admin/settings", url.Values{"time_zone": {"__custom__"}, "custom_time_zone": {"not/a-zone"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body(t, response), "保存失败") {
		t.Fatalf("invalid zone was not rejected: %d %s", response.StatusCode, body(t, response))
	}
	if page := body(t, get(t, client, server.URL+"/admin/accounts")); !strings.Contains(page, "时间以 Europe/Paris 展示") {
		t.Fatalf("invalid zone replaced the stored setting: %s", page)
	}
}

func TestBatchAccountExpiryManagement(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	response := get(t, client, server.URL+"/admin/accounts")
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"batch-user"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d", response.StatusCode)
	}

	response = get(t, client, server.URL+"/admin/accounts")
	page := body(t, response)
	if !strings.Contains(page, "account-batch") || !strings.Contains(page, "batch-select") || !strings.Contains(page, "data-batch-status-filter") || !strings.Contains(page, "data-batch-select-status") || !strings.Contains(page, "app.js?v=5") {
		t.Fatalf("batch controls absent: %s", page)
	}
	response = postForm(t, client, server.URL+"/admin/accounts/batch", url.Values{"account_id": {"1:1"}, "action": {"extend"}, "duration": {"2"}, "duration_unit": {"day"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("batch update failed: status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+response.Header.Get("Location"))
	if !strings.Contains(body(t, response), "已完成 1 个账号的批量操作") {
		t.Fatal("batch success notice is absent")
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", "integration-key")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "2030-01-03T00:00:00Z") {
		t.Fatal("batch extension was not persisted")
	}
}

func TestBatchRejectsStalePageAfterConcurrentRenewal(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	response := get(t, client, server.URL+"/admin/accounts")
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"stale-batch-user"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/accounts")
	stalePage := body(t, response)

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/v1/accounts/1", strings.NewReader(`{"expires_at":"2030-02-01T00:00:00Z","note":"renewed","version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Key", "integration-key")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("concurrent API update status = %d: %s", response.StatusCode, body(t, response))
	}

	response = postForm(t, client, server.URL+"/admin/accounts/batch", url.Values{"account_id": {"1:1"}, "action": {"set_expiry"}, "expires_at": {"2030-01-15T00:00"}, "csrf_token": {csrf(t, stalePage)}})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body(t, response), "批量操作失败") {
		t.Fatalf("stale batch status = %d, want rejected batch", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", "integration-key")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body(t, response), "2030-02-01T00:00:00Z") {
		t.Fatal("stale batch overwrote the concurrent renewal")
	}
}

func TestAccountCreateDisableAndDelete(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)

	response := get(t, client, server.URL+"/admin/accounts")
	response = postForm(t, client, server.URL+"/admin/accounts", url.Values{"username": {"alice"}, "password": {"password123"}, "expires_at": {"2030-01-01T00:00"}, "note": {"test"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}
	response = get(t, client, server.URL+"/admin/accounts")
	page := body(t, response)
	if !strings.Contains(page, "alice") || !strings.Contains(page, "active") {
		t.Fatalf("created account absent: %s", page)
	}
	response = postForm(t, client, server.URL+"/admin/accounts/1/update", url.Values{"expires_at": {"2031-01-01T00:00"}, "note": {"changed"}, "csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update account status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/accounts")
	page = body(t, response)
	if !strings.Contains(page, "changed") {
		t.Fatalf("updated account absent: %s", page)
	}

	response = postForm(t, client, server.URL+"/admin/accounts/1/disable", url.Values{"csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable account status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/accounts")
	page = body(t, response)
	if !strings.Contains(page, "disabled") {
		t.Fatalf("disabled account absent: %s", page)
	}

	response = postForm(t, client, server.URL+"/admin/accounts/1/delete", url.Values{"csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete confirmation status = %d", response.StatusCode)
	}
	confirmation := body(t, response)
	response = postForm(t, client, server.URL+"/admin/accounts/1/delete/confirm", url.Values{"csrf_token": {csrf(t, confirmation)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete account status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/admin/accounts")
	if !strings.Contains(body(t, response), "尚未创建账号") {
		t.Fatal("deleted account remains visible")
	}
}

func TestAccountDeletionRequiresSecondCSRFProtectedConfirmation(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"delete-confirmation","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}
	_ = body(t, response)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)
	response = get(t, client, server.URL+"/admin/accounts")
	page := body(t, response)
	if strings.Contains(page, "onsubmit=") {
		t.Fatalf("delete confirmation must not rely on CSP-blocked inline handlers: %s", page)
	}

	response = postForm(t, client, server.URL+"/admin/accounts/1/delete", url.Values{"csrf_token": {csrf(t, page)}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first delete request status = %d", response.StatusCode)
	}
	confirmation := body(t, response)
	if !strings.Contains(confirmation, "确认删除业务账号 delete-confirmation") {
		t.Fatalf("confirmation page does not name the account: %s", confirmation)
	}

	response = get(t, client, server.URL+"/admin/accounts")
	if !strings.Contains(body(t, response), "delete-confirmation") {
		t.Fatal("first delete request must not delete the account")
	}
}

func TestUserPortalLoginAndDashboard(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"portal-user","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create portal account status = %d", response.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response = get(t, client, server.URL+"/portal/login")
	response = postForm(t, client, server.URL+"/portal/login", url.Values{"username": {"portal-user"}, "password": {"password123"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("portal login status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/portal/")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "我的订阅") {
		t.Fatal("portal dashboard is unavailable")
	}
}

func TestAuthenticatedPortalRenewalUsesSessionAccountWithoutCredentials(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"portal-user","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create portal account status = %d: %s", response.StatusCode, body(t, response))
	}
	_ = body(t, response)

	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, adminClient, server.URL)
	response = get(t, adminClient, server.URL+"/admin/invites")
	responseBody := body(t, response)
	response = postForm(t, adminClient, server.URL+"/admin/invites", url.Values{"duration": {"1"}, "duration_unit": {"day"}, "max_uses": {"1"}, "csrf_token": {csrf(t, responseBody)}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create renewal invite status = %d: %s", response.StatusCode, body(t, response))
	}
	inviteMatch := regexp.MustCompile(`ESP-[A-Za-z0-9_-]+`).FindString(body(t, response))
	if inviteMatch == "" {
		t.Fatalf("created invite code missing: %s", body(t, response))
	}

	portalJar, _ := cookiejar.New(nil)
	portalClient := &http.Client{Jar: portalJar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response = get(t, portalClient, server.URL+"/portal/login")
	response = postForm(t, portalClient, server.URL+"/portal/login", url.Values{"username": {"portal-user"}, "password": {"password123"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("portal login status = %d", response.StatusCode)
	}
	response = get(t, portalClient, server.URL+"/portal/")
	portalPage := body(t, response)
	for _, marker := range []string{`href="/purchase">购买激活码`, `href="/renew">续费订阅`} {
		if !strings.Contains(portalPage, marker) {
			t.Fatalf("portal dashboard missing %q: %s", marker, portalPage)
		}
	}
	response = get(t, portalClient, server.URL+"/renew")
	renewPage := body(t, response)
	if strings.Contains(renewPage, `name="username"`) || strings.Contains(renewPage, `name="password"`) {
		t.Fatalf("authenticated renewal page still asks for credentials: %s", renewPage)
	}
	response = postForm(t, portalClient, server.URL+"/renew", url.Values{"code": {inviteMatch}, "csrf_token": {csrf(t, renewPage)}})
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "续费成功") {
		t.Fatalf("authenticated invite renewal status = %d: %s", response.StatusCode, body(t, response))
	}
}

func TestPortalSessionIsDeniedImmediatelyAfterAccountIsDisabled(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"portal-user","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create portal account status = %d: %s", response.StatusCode, body(t, response))
	}
	_ = body(t, response)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response = get(t, client, server.URL+"/portal/login")
	response = postForm(t, client, server.URL+"/portal/login", url.Values{"username": {"portal-user"}, "password": {"password123"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("portal login status = %d", response.StatusCode)
	}

	response = postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts/1/disable", `{}`, "integration-key")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable account status = %d: %s", response.StatusCode, body(t, response))
	}
	_ = body(t, response)

	response = get(t, client, server.URL+"/portal/")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("disabled account portal status = %d, want redirect to login", response.StatusCode)
	}
}

func TestManagedPasswordsAreNeverExposedByWebPagesOrEndpoints(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"portal-user","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create account status = %d: %s", response.StatusCode, body(t, response))
	}
	createdBody := body(t, response)
	if !strings.Contains(createdBody, `"expires_at"`) || strings.Contains(createdBody, `"ExpiresAt"`) {
		t.Fatalf("account response must use stable snake_case fields: %s", createdBody)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL)
	response = get(t, client, server.URL+"/admin/accounts")
	accountsPage := body(t, response)
	if strings.Contains(accountsPage, "data-password") || strings.Contains(accountsPage, "获取密码") {
		t.Fatal("admin account page still exposes a password retrieval control")
	}
	response = postForm(t, client, server.URL+"/admin/accounts/1/password", url.Values{"csrf_token": {csrf(t, accountsPage)}})
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("retired password endpoint status = %d, want 404 or 405", response.StatusCode)
	}
	_ = body(t, response)

	response = get(t, client, server.URL+"/portal/login")
	response = postForm(t, client, server.URL+"/portal/login", url.Values{"username": {"portal-user"}, "password": {"password123"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("portal login status = %d", response.StatusCode)
	}
	response = get(t, client, server.URL+"/portal/")
	portalPage := body(t, response)
	if strings.Contains(portalPage, "password123") || strings.Contains(portalPage, "账号密码：") {
		t.Fatal("portal page still exposes the managed password")
	}
}

func TestAccountCreateAndRegistrationRequireIdempotencyKey(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	for _, endpoint := range []string{"/api/v1/accounts", "/api/v1/register"} {
		request, err := http.NewRequest(http.MethodPost, server.URL+endpoint, strings.NewReader(`{"username":"alice","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-API-Key", "integration-key")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s without Idempotency-Key status = %d, want 400", endpoint, response.StatusCode)
		}
		_ = body(t, response)
	}
}

func TestExternalAPIRejectsTrailingJSON(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/invites", `{"duration_days":30,"max_uses":1}{}`, "integration-key")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d", response.StatusCode)
	}
}

func TestExternalAPIInviteRegistrationAndRenewal(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := get(t, http.DefaultClient, server.URL+"/api/v1/invites")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status = %d", response.StatusCode)
	}

	response = postJSON(t, http.DefaultClient, server.URL+"/api/v1/invites", `{"duration_minutes":60,"max_uses":2,"note":"integration"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create invite status = %d: %s", response.StatusCode, body(t, response))
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body(t, response)), &created); err != nil || created.Code == "" {
		t.Fatalf("decode invite: %v", err)
	}

	response = postJSON(t, http.DefaultClient, server.URL+"/api/v1/register", `{"code":"`+created.Code+`","username":"invite-user","password":"password123"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d: %s", response.StatusCode, body(t, response))
	}
	response = postJSON(t, http.DefaultClient, server.URL+"/api/v1/renew", `{"code":"`+created.Code+`","username":"invite-user","password":"wrong-password"}`, "integration-key")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-password renew status = %d: %s", response.StatusCode, body(t, response))
	}
	_ = body(t, response)
	response = postJSON(t, http.DefaultClient, server.URL+"/api/v1/renew", `{"code":"`+created.Code+`","username":"invite-user","password":"password123"}`, "integration-key")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("renew status = %d: %s", response.StatusCode, body(t, response))
	}
	response = getWithKey(t, http.DefaultClient, server.URL+"/api/v1/invites", "integration-key")
	inviteList := body(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(inviteList, `"used_count":2`) || !strings.Contains(inviteList, `"duration_minutes":60`) {
		t.Fatal("minute-card usage was not recorded")
	}
}

func TestHomeAndUserLoginPages(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := get(t, http.DefaultClient, server.URL+"/")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "用户登录") {
		t.Fatal("home page is unavailable")
	}
	response = get(t, http.DefaultClient, server.URL+"/login")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "登录用户中心") {
		t.Fatal("user login page is unavailable")
	}
}

func TestAdminPagesLoadTheServedStylesheet(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := get(t, http.DefaultClient, server.URL+"/admin/login")
	if !strings.Contains(body(t, response), `href="/static/app.css"`) {
		t.Fatal("login page does not load the stylesheet")
	}
	response = get(t, http.DefaultClient, server.URL+"/static/app.css")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "--brand") {
		t.Fatal("stylesheet is not served")
	}
	if policy := response.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "style-src 'self'") {
		t.Fatalf("stylesheet is blocked by CSP: %q", policy)
	}
}

func TestSensitivePagesAndStaticAssetsHaveSafeCaching(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	response := get(t, http.DefaultClient, server.URL+"/admin/login")
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("admin login Cache-Control = %q, want no-store", cacheControl)
	}
	_ = body(t, response)

	for _, asset := range []string{"/static/app.css", "/static/app.js"} {
		response = get(t, http.DefaultClient, server.URL+asset)
		if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-cache, must-revalidate" {
			t.Fatalf("%s Cache-Control = %q, want revalidation", asset, cacheControl)
		}
		_ = body(t, response)
	}
}

func TestHealthEndpoints(t *testing.T) {
	application := testApplication(t)
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := get(t, http.DefaultClient, server.URL+"/healthz")
	if response.StatusCode != http.StatusOK || body(t, response) != "ok\n" {
		t.Fatal("health endpoint is not ready")
	}
	response = get(t, http.DefaultClient, server.URL+"/api/v1/health")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), `"status":"ok"`) {
		t.Fatal("API health endpoint is not ready")
	}
}

func testApplication(t *testing.T) *app.Application {
	return testApplicationWithTimeZone(t, "UTC")
}

func testApplicationWithTimeZone(t *testing.T, timeZone string) *app.Application {
	t.Helper()
	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/AuthenticateByName":
			var credentials struct {
				Username string `json:"Username"`
				Password string `json:"Pw"`
			}
			_ = json.NewDecoder(r.Body).Decode(&credentials)
			if credentials.Username != "portal-user" || credentials.Password != "password123" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"User": map[string]string{"Id": "emby-test-user", "Name": credentials.Username}})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/New":
			if r.Header.Get("X-Emby-Token") != "test-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "emby-test-user", "Name": r.URL.Query().Get("Name")})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/emby-test-user/Password":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/emby/Users/emby-test-user":
			_ = json.NewEncoder(w).Encode(map[string]any{"Policy": map[string]any{"IsDisabled": false, "IsAdministrator": false, "EnableAudioPlaybackTranscoding": true, "EnableVideoPlaybackTranscoding": true, "EnablePlaybackRemuxing": true, "EnableContentDownloading": true, "EnableSyncTranscoding": true, "EnableSubtitleDownloading": true, "EnableSubtitleManagement": true, "EnableMediaConversion": true}})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/emby-test-user/Policy":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/emby/Users/emby-test-user":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(emby.Close)
	application, err := app.New(context.Background(), config.Config{
		ListenAddr: ":0", DatabasePath: filepath.Join(t.TempDir(), "manager.db"),
		EmbyBaseURL: emby.URL + "/emby", EmbyAPIKey: "test-key", APIKey: "integration-key",
		CredentialMasterKey: "test-credential-master-key-that-is-long-enough",
		AdminUsername:       "admin", AdminPassword: "correct horse battery staple",
		CookieSecure: false, SessionTTL: time.Hour, TimeZone: timeZone,
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	return application
}
func postJSON(t *testing.T, client *http.Client, target, payload, apiKey string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Key", apiKey)
	if strings.HasSuffix(target, "/api/v1/accounts") || strings.HasSuffix(target, "/api/v1/register") {
		request.Header.Set("Idempotency-Key", "acceptance-idempotency-key")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func getWithKey(t *testing.T, client *http.Client, target, apiKey string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", apiKey)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func login(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	response := get(t, client, baseURL+"/admin/login")
	response = postForm(t, client, baseURL+"/admin/login", url.Values{"username": {"admin"}, "password": {"correct horse battery staple"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d", response.StatusCode)
	}
}
func get(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func postForm(t *testing.T, client *http.Client, target string, values url.Values) *http.Response {
	t.Helper()
	response, err := client.PostForm(target, values)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func body(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	value, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
func csrf(t *testing.T, html string) string {
	t.Helper()
	match := csrfPattern.FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatalf("CSRF token absent in %q", html)
	}
	return match[1]
}
