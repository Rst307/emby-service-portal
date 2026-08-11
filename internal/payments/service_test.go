package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/credentials"
	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/paymentcenter"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

func TestActivationOrderIsFulfilledFromVerifiedPaymentCenterCallback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	secret := "test-payment-center-secret-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orders" {
			t.Fatalf("provider request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Note      string `json:"note"`
			ReturnURL string `json:"return_url"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		if request.Note != "购买人：张三 / wx-z3" {
			t.Fatalf("provider note = %q", request.Note)
		}
		if !strings.HasPrefix(request.ReturnURL, "https://user.example/payment/") || !strings.Contains(request.ReturnURL, "order=ESP-") || strings.Contains(request.ReturnURL, "{") {
			t.Fatalf("return_url = %q", request.ReturnURL)
		}
		if got := signForTest(secret, r.Method, r.URL.Path, r.Header.Get("X-Pay-Timestamp"), r.Header.Get("X-Pay-Nonce"), body); got != r.Header.Get("X-Pay-Signature") {
			t.Fatalf("provider request signature mismatch")
		}
		_, _ = w.Write([]byte(`{"merchant_order_no":"` + merchantOrderFromBody(body) + `","amount_fen":990,"currency":"CNY","subject":"月卡","status":"PENDING","payment_memo":"PCABC123","payment_url":"https://pay.example/checkout","expires_at":"2026-08-10T12:15:00+00:00"}`))
	}))
	defer server.Close()

	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vault := credentials.New("test-credential-master-key-that-is-long-enough")
	center := paymentcenter.NewClient(server.Client())
	center.Now = func() time.Time { return now }
	service := New(store, nil, vault, center)
	service.now = func() time.Time { return now }
	if err := service.UpdateSettings(ctx, UpdatePaymentSettingsInput{BaseURL: server.URL, AppID: "app_test", AppSecret: secret, CallbackURL: "http://127.0.0.1/webhooks/wxpay-payment-center", ReturnURL: "https://user.example/payment/{token}?order={order_no}", OrderTTLMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(ctx, domain.CreatePaymentPlanInput{Kind: KindActivation, Name: "月卡", DurationDays: 30, PriceFen: 990})
	if err != nil {
		t.Fatal(err)
	}
	order, err := service.CreateActivationOrder(ctx, plan.ID, "张三 / wx-z3")
	if err != nil {
		t.Fatal(err)
	}
	if order.PaymentURL != "https://pay.example/checkout" || order.PaymentStatus != "pending" {
		t.Fatalf("order = %+v", order)
	}

	body := []byte(`{"amount_fen":990,"app_id":"app_test","currency":"CNY","event":"order.paid","event_id":"evt_paid_1","event_version":1,"merchant_order_no":"` + order.MerchantOrderNo + `","paid_at":"2026-08-10T12:03:00+00:00","payment_idempotency_key":"wechat-f2f:1","status":"PAID"}`)
	headers := http.Header{}
	headers.Set("X-Pay-App-Id", "app_test")
	headers.Set("X-Pay-Event-Id", "evt_paid_1")
	headers.Set("X-Pay-Timestamp", strconv.FormatInt(now.Unix(), 10))
	headers.Set("X-Pay-Nonce", "callback-nonce-1")
	headers.Set("X-Pay-Signature", signCallbackForTest(secret, headers.Get("X-Pay-Timestamp"), headers.Get("X-Pay-Nonce"), body))
	if err := service.HandleWebhook(ctx, headers, body); err != nil {
		t.Fatal(err)
	}
	fulfilled, err := service.PublicOrder(ctx, order.PublicToken)
	if err != nil {
		t.Fatal(err)
	}
	if fulfilled.FulfillmentStatus != "completed" || !strings.HasPrefix(fulfilled.ActivationCode, "ESP-ACT-") {
		t.Fatalf("fulfilled = %+v", fulfilled)
	}
	if err := service.HandleWebhook(ctx, headers, body); err != nil {
		t.Fatal("duplicate callback: ", err)
	}
}

func TestReconcileCancelsLocallyExpiredRPayOrder(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	current := start
	secret := "test-payment-center-secret-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/orders" && r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"merchant_order_no":"` + merchantOrderFromBody(body) + `","amount_fen":990,"currency":"CNY","subject":"月卡","status":"PENDING","payment_memo":"PCABC123","payment_url":"https://pay.example/checkout","expires_at":"2026-08-10T12:15:00+00:00"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") && r.Method == http.MethodPost {
			merchantNo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/orders/"), "/cancel")
			_, _ = w.Write([]byte(`{"merchant_order_no":"` + merchantNo + `","amount_fen":990,"currency":"CNY","subject":"月卡","status":"CANCELED","payment_memo":"PCABC123","payment_url":"https://pay.example/checkout"}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/orders/") && r.Method == http.MethodGet {
			merchantNo := strings.TrimPrefix(r.URL.Path, "/v1/orders/")
			_, _ = w.Write([]byte(`{"merchant_order_no":"` + merchantNo + `","amount_fen":990,"currency":"CNY","subject":"月卡","status":"PENDING","payment_memo":"PCABC123","payment_url":"https://pay.example/checkout"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vault := credentials.New("test-credential-master-key-that-is-long-enough")
	center := paymentcenter.NewClient(server.Client())
	center.Now = func() time.Time { return current }
	service := New(store, nil, vault, center)
	service.now = func() time.Time { return current }
	if err := service.UpdateSettings(ctx, UpdatePaymentSettingsInput{BaseURL: server.URL, AppID: "app_test", AppSecret: secret, CallbackURL: "http://127.0.0.1/webhooks/wxpay-payment-center", OrderTTLMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(ctx, domain.CreatePaymentPlanInput{Kind: KindActivation, Name: "月卡", DurationDays: 30, PriceFen: 990})
	if err != nil {
		t.Fatal(err)
	}
	order, err := service.CreateActivationOrder(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	current = start.Add(20 * time.Minute)
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	order, err = service.PublicOrder(ctx, order.PublicToken)
	if err != nil {
		t.Fatal(err)
	}
	if order.PaymentStatus != "canceled" || order.ProviderStatus != "CANCELED" {
		t.Fatalf("order after reconciliation = %+v", order)
	}
}

func TestRenewalOrderForAuthenticatedAccountSkipsPasswordVerification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	secret := "test-payment-center-secret-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orders" {
			t.Fatalf("provider request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Note string `json:"note"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		if request.Note != "业务账号：alice" {
			t.Fatalf("provider note = %q", request.Note)
		}
		_, _ = w.Write([]byte(`{"merchant_order_no":"` + merchantOrderFromBody(body) + `","amount_fen":990,"currency":"CNY","subject":"月卡续费","status":"PENDING","payment_memo":"PCABC123","payment_url":"https://pay.example/checkout","expires_at":"2026-08-10T12:15:00+00:00"}`))
	}))
	defer server.Close()

	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateAccount(ctx, domain.Account{EmbyUserID: "emby-user", Username: "alice", Status: "active", ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	center := paymentcenter.NewClient(server.Client())
	center.Now = func() time.Time { return now }
	service := New(store, nil, credentials.New("test-credential-master-key-that-is-long-enough"), center)
	service.now = func() time.Time { return now }
	if err := service.UpdateSettings(ctx, UpdatePaymentSettingsInput{BaseURL: server.URL, AppID: "app_test", AppSecret: secret, CallbackURL: "http://127.0.0.1/webhooks/wxpay-payment-center", OrderTTLMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(ctx, domain.CreatePaymentPlanInput{Kind: KindRenewal, Name: "月卡续费", DurationDays: 30, PriceFen: 990})
	if err != nil {
		t.Fatal(err)
	}
	order, err := service.CreateRenewalOrderForAccount(ctx, plan.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if order.AccountID == nil || *order.AccountID != account.ID || order.AccountUsername != "alice" || order.PaymentStatus != "pending" {
		t.Fatalf("renewal order = %+v", order)
	}
}

func merchantOrderFromBody(body []byte) string {
	const key = `"merchant_order_no":"`
	text := string(body)
	start := strings.Index(text, key)
	if start < 0 {
		return ""
	}
	start += len(key)
	end := strings.IndexByte(text[start:], '"')
	if end < 0 {
		return ""
	}
	return text[start : start+end]
}

func signForTest(secret, method, path, timestamp, nonce string, body []byte) string {
	hash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(hash[:])}, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

func signCallbackForTest(secret, timestamp, nonce string, body []byte) string {
	hash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + nonce + "\n" + hex.EncodeToString(hash[:])))
	return hex.EncodeToString(mac.Sum(nil))
}
