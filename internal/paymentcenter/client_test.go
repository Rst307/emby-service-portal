package paymentcenter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateOrderSignsExactRequestBody(t *testing.T) {
	secret := "test-payment-center-secret-123"
	fixed := time.Unix(1_786_360_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orders" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Pay-App-Id"); got != "app_test" {
			t.Fatalf("app id = %q", got)
		}
		if got := signRequest(secret, r.Method, r.URL.Path, r.Header.Get("X-Pay-Timestamp"), r.Header.Get("X-Pay-Nonce"), body); got != r.Header.Get("X-Pay-Signature") {
			t.Fatalf("signature mismatch: computed %q, header %q", got, r.Header.Get("X-Pay-Signature"))
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["amount_fen"] != float64(990) || payload["merchant_order_no"] != "EUM-TEST-1" || payload["return_url"] != "https://user.example/payment/token" {
			t.Fatalf("payload = %s", body)
		}
		_, _ = w.Write([]byte(`{"merchant_order_no":"EUM-TEST-1","amount_fen":990,"currency":"CNY","subject":"月卡","status":"PENDING","payment_memo":"PCABC123","payment_url":"https://pay.test/pay/abc","expires_at":"2026-08-10T12:15:00+00:00"}`))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.Now = func() time.Time { return fixed }
	order, err := client.CreateOrder(context.Background(), Config{BaseURL: server.URL, AppID: "app_test", AppSecret: secret}, CreateOrderInput{MerchantOrderNo: "EUM-TEST-1", AmountFen: 990, Currency: "CNY", Subject: "月卡", ExpiresInSeconds: 900, ReturnURL: "https://user.example/payment/token"})
	if err != nil {
		t.Fatal(err)
	}
	if order.PaymentURL != "https://pay.test/pay/abc" || order.Status != "PENDING" {
		t.Fatalf("order = %+v", order)
	}
}

func TestVerifyNotificationChecksRawBodyAndEventHeaders(t *testing.T) {
	secret := "test-payment-center-secret-123"
	fixed := time.Unix(1_786_360_000, 0)
	body := []byte(`{"amount_fen":990,"app_id":"app_test","currency":"CNY","event":"order.paid","event_id":"evt_1","event_version":1,"merchant_order_no":"EUM-TEST-1","paid_at":"2026-08-10T12:03:00+00:00","payment_idempotency_key":"wechat-f2f:1","status":"PAID"}`)
	timestamp := "1786360000"
	nonce := "nonce-123456"
	client := NewClient(nil)
	client.Now = func() time.Time { return fixed }
	headers := http.Header{
		"X-Pay-App-Id":    []string{"app_test"},
		"X-Pay-Event-Id":  []string{"evt_1"},
		"X-Pay-Timestamp": []string{timestamp},
		"X-Pay-Nonce":     []string{nonce},
		"X-Pay-Signature": []string{signCallback(secret, timestamp, nonce, body)},
	}
	notification, err := client.VerifyNotification(Config{AppID: "app_test", AppSecret: secret}, headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if notification.EventID != "evt_1" || notification.AmountFen != 990 || notification.MerchantOrderNo != "EUM-TEST-1" {
		t.Fatalf("notification = %+v", notification)
	}
	body[0] = 'X'
	if _, err := client.VerifyNotification(Config{AppID: "app_test", AppSecret: secret}, headers, body); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("tampered body error = %v", err)
	}
}
