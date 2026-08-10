// Package paymentcenter adapts the wxpay-payment-center merchant protocol.
package paymentcenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotConfigured        = errors.New("payment center is not configured")
	ErrInvalidConfiguration = errors.New("invalid payment center configuration")
	ErrInvalidNotification  = errors.New("invalid payment notification")
)

// Config contains the merchant credentials copied from wxpay-payment-center.
// AppSecret is never returned to templates or JSON responses.
type Config struct {
	BaseURL         string
	AppID           string
	AppSecret       string
	SignatureWindow time.Duration
}

func (c Config) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.AppID) != "" && strings.TrimSpace(c.AppSecret) != ""
}

func (c Config) Validate() error {
	if !c.Configured() {
		return ErrNotConfigured
	}
	parsed, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("%w: base URL must be an absolute HTTP(S) URL", ErrInvalidConfiguration)
	}
	if parsed.Scheme == "http" && !isLocalHost(parsed.Hostname()) {
		return fmt.Errorf("%w: payment center HTTP is only allowed on localhost", ErrInvalidConfiguration)
	}
	if len(strings.TrimSpace(c.AppID)) > 200 || len(strings.TrimSpace(c.AppSecret)) < 16 {
		return fmt.Errorf("%w: app credentials are invalid", ErrInvalidConfiguration)
	}
	return nil
}

func isLocalHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

// Order is the provider's normalized merchant-order response.
type Order struct {
	MerchantOrderNo string `json:"merchant_order_no"`
	AmountFen       int    `json:"amount_fen"`
	Currency        string `json:"currency"`
	Subject         string `json:"subject"`
	Status          string `json:"status"`
	PaymentMemo     string `json:"payment_memo"`
	PaymentURL      string `json:"payment_url"`
	CreatedAt       string `json:"created_at"`
	ExpiresAt       string `json:"expires_at"`
	PaidAt          string `json:"paid_at"`
}

type CreateOrderInput struct {
	MerchantOrderNo  string
	AmountFen        int
	Currency         string
	Subject          string
	ExpiresInSeconds int
}

// Notification is the verified, provider-neutral paid event.
type Notification struct {
	EventID               string
	MerchantOrderNo       string
	AmountFen             int
	Currency              string
	PaidAt                time.Time
	PaymentIdempotencyKey string
	PayloadHash           string
}

type Client struct {
	HTTP            *http.Client
	Now             func() time.Time
	SignatureWindow time.Duration
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{HTTP: httpClient, Now: time.Now, SignatureWindow: 5 * time.Minute}
}

func (c *Client) CreateOrder(ctx context.Context, cfg Config, input CreateOrderInput) (Order, error) {
	if err := cfg.Validate(); err != nil {
		return Order{}, err
	}
	payload := struct {
		MerchantOrderNo  string `json:"merchant_order_no"`
		AmountFen        int    `json:"amount_fen"`
		Currency         string `json:"currency"`
		Subject          string `json:"subject"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
	}{input.MerchantOrderNo, input.AmountFen, input.Currency, input.Subject, input.ExpiresInSeconds}
	body, err := json.Marshal(payload)
	if err != nil {
		return Order{}, fmt.Errorf("marshal payment order: %w", err)
	}
	return c.doOrder(ctx, cfg, http.MethodPost, "/v1/orders", body)
}

func (c *Client) QueryOrder(ctx context.Context, cfg Config, merchantOrderNo string) (Order, error) {
	if err := cfg.Validate(); err != nil {
		return Order{}, err
	}
	path := "/v1/orders/" + url.PathEscape(merchantOrderNo)
	return c.doOrder(ctx, cfg, http.MethodGet, path, nil)
}

func (c *Client) doOrder(ctx context.Context, cfg Config, method, path string, body []byte) (Order, error) {
	timestamp := strconv.FormatInt(c.clock().Unix(), 10)
	nonce, err := newNonce()
	if err != nil {
		return Order{}, err
	}
	signature := signRequest(cfg.AppSecret, method, path, timestamp, nonce, body)
	target := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + path
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, requestBody)
	if err != nil {
		return Order{}, fmt.Errorf("build payment-center request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Pay-App-Id", cfg.AppID)
	request.Header.Set("X-Pay-Timestamp", timestamp)
	request.Header.Set("X-Pay-Nonce", nonce)
	request.Header.Set("X-Pay-Signature", signature)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return Order{}, fmt.Errorf("call payment center: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Order{}, fmt.Errorf("read payment center response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Order{}, fmt.Errorf("payment center returned HTTP %d: %s", response.StatusCode, providerMessage(responseBody))
	}
	var order Order
	if err := json.Unmarshal(responseBody, &order); err != nil {
		return Order{}, fmt.Errorf("decode payment center response: %w", err)
	}
	if order.MerchantOrderNo == "" || order.AmountFen < 1 || order.Currency == "" || order.Status == "" {
		return Order{}, fmt.Errorf("payment center returned an incomplete order")
	}
	return order, nil
}

func providerMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if payload.Message != "" {
			return payload.Message
		}
		if payload.Error != "" {
			return payload.Error
		}
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 240 {
		return message[:240]
	}
	return message
}

func (c *Client) VerifyNotification(cfg Config, headers http.Header, body []byte) (Notification, error) {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AppSecret) == "" {
		return Notification{}, ErrNotConfigured
	}
	timestampText := headers.Get("X-Pay-Timestamp")
	nonce := headers.Get("X-Pay-Nonce")
	signature := headers.Get("X-Pay-Signature")
	if headers.Get("X-Pay-App-Id") != cfg.AppID || headers.Get("X-Pay-Event-Id") == "" || timestampText == "" || nonce == "" || signature == "" {
		return Notification{}, ErrInvalidNotification
	}
	requestTimestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || len(nonce) < 8 || len(nonce) > 128 || len(signature) != 64 {
		return Notification{}, ErrInvalidNotification
	}
	if absInt64(c.clock().Unix()-requestTimestamp) > int64(c.signatureWindow().Seconds()) {
		return Notification{}, ErrInvalidNotification
	}
	expected := signCallback(cfg.AppSecret, timestampText, nonce, body)
	if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(expected)) {
		return Notification{}, ErrInvalidNotification
	}
	var payload struct {
		Event                 string `json:"event"`
		EventID               string `json:"event_id"`
		AppID                 string `json:"app_id"`
		MerchantOrderNo       string `json:"merchant_order_no"`
		AmountFen             int    `json:"amount_fen"`
		Currency              string `json:"currency"`
		PaidAt                string `json:"paid_at"`
		PaymentIdempotencyKey string `json:"payment_idempotency_key"`
		Status                string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Notification{}, ErrInvalidNotification
	}
	if payload.Event != "order.paid" || payload.EventID == "" || payload.EventID != headers.Get("X-Pay-Event-Id") || payload.AppID != cfg.AppID || payload.MerchantOrderNo == "" || payload.AmountFen < 1 || payload.Currency != "CNY" || payload.Status != "PAID" || payload.PaymentIdempotencyKey == "" {
		return Notification{}, ErrInvalidNotification
	}
	paidAt, err := time.Parse(time.RFC3339, payload.PaidAt)
	if err != nil {
		return Notification{}, ErrInvalidNotification
	}
	hash := sha256.Sum256(body)
	return Notification{EventID: payload.EventID, MerchantOrderNo: payload.MerchantOrderNo, AmountFen: payload.AmountFen, Currency: payload.Currency, PaidAt: paidAt.UTC(), PaymentIdempotencyKey: payload.PaymentIdempotencyKey, PayloadHash: hex.EncodeToString(hash[:])}, nil
}

func (c *Client) clock() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) signatureWindow() time.Duration {
	if c.SignatureWindow <= 0 {
		return 5 * time.Minute
	}
	return c.SignatureWindow
}

func newNonce() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate payment nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func signRequest(secret, method, path, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{strings.ToUpper(method), path, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func signCallback(secret, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	canonical := timestamp + "\n" + nonce + "\n" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
