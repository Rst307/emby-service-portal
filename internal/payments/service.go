// Package payments owns sale plans, payment orders, and paid fulfillment.
package payments

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/credentials"
	"github.com/emby-user-manager/emby-user-manager/internal/invites"
	"github.com/emby-user-manager/emby-user-manager/internal/paymentcenter"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

const (
	KindActivation = "activation"
	KindRenewal    = "renewal"

	paymentBaseURLKey   = "payment_center.base_url"
	paymentAppIDKey     = "payment_center.app_id"
	paymentAppSecretKey = "payment_center.app_secret"
	paymentCallbackKey  = "payment_center.callback_url"
	paymentReturnKey    = "payment_center.return_url"
	paymentTTLKey       = "payment_center.order_ttl_minutes"
	paymentSecretName   = "payment-center-app-secret"
)

var (
	ErrPlanNotAvailable = errors.New("payment plan is not available")
	ErrPaymentNotReady  = errors.New("payment center is not configured")
	ErrPaymentOrder     = errors.New("payment order is invalid")
)

type PaymentSettings struct {
	BaseURL          string
	AppID            string
	CallbackURL      string
	ReturnURL        string
	OrderTTLMinutes  int
	Configured       bool
	SecretConfigured bool
}

type UpdatePaymentSettingsInput struct {
	BaseURL         string
	AppID           string
	AppSecret       string
	CallbackURL     string
	ReturnURL       string
	OrderTTLMinutes int
}

type Service struct {
	store    *sqlite.Store
	accounts *accounts.Service
	vault    *credentials.Vault
	center   *paymentcenter.Client
	now      func() time.Time
}

func New(store *sqlite.Store, accountService *accounts.Service, vault *credentials.Vault, center *paymentcenter.Client) *Service {
	if center == nil {
		center = paymentcenter.NewClient(nil)
	}
	return &Service{store: store, accounts: accountService, vault: vault, center: center, now: time.Now}
}

func (s *Service) Settings(ctx context.Context) (PaymentSettings, error) {
	baseURL, _, err := s.store.Setting(ctx, paymentBaseURLKey)
	if err != nil {
		return PaymentSettings{}, err
	}
	appID, _, err := s.store.Setting(ctx, paymentAppIDKey)
	if err != nil {
		return PaymentSettings{}, err
	}
	callbackURL, _, err := s.store.Setting(ctx, paymentCallbackKey)
	if err != nil {
		return PaymentSettings{}, err
	}
	returnURL, _, err := s.store.Setting(ctx, paymentReturnKey)
	if err != nil {
		return PaymentSettings{}, err
	}
	ttl := 15
	if raw, ok, err := s.store.Setting(ctx, paymentTTLKey); err != nil {
		return PaymentSettings{}, err
	} else if ok {
		if parsed, parseErr := parsePositiveInt(raw); parseErr == nil {
			ttl = parsed
		}
	}
	secretConfigured, secret, err := s.readSecret(ctx)
	if err != nil {
		return PaymentSettings{}, err
	}
	configured := strings.TrimSpace(baseURL) != "" && strings.TrimSpace(appID) != "" && secret != ""
	return PaymentSettings{BaseURL: baseURL, AppID: appID, CallbackURL: callbackURL, ReturnURL: returnURL, OrderTTLMinutes: ttl, Configured: configured, SecretConfigured: secretConfigured}, nil
}

func (s *Service) UpdateSettings(ctx context.Context, input UpdatePaymentSettingsInput) error {
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.AppID = strings.TrimSpace(input.AppID)
	input.CallbackURL = strings.TrimSpace(input.CallbackURL)
	input.ReturnURL = strings.TrimSpace(input.ReturnURL)
	if input.OrderTTLMinutes < 1 || input.OrderTTLMinutes > 1440 {
		return fmt.Errorf("订单有效期必须在 1 到 1440 分钟之间")
	}
	if input.BaseURL != "" {
		parsed, err := url.Parse(input.BaseURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !isLocalHost(parsed.Hostname())) {
			return fmt.Errorf("支付中心地址必须是 HTTPS；本地测试可使用 localhost HTTP")
		}
	}
	if len(input.AppID) > 200 {
		return fmt.Errorf("支付中心 App ID 过长")
	}
	if input.CallbackURL != "" {
		parsed, err := url.Parse(input.CallbackURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !isLocalHost(parsed.Hostname())) {
			return fmt.Errorf("支付回调地址必须是 HTTPS；本地测试可使用 localhost HTTP")
		}
		if strings.TrimRight(parsed.Path, "/") != "/webhooks/wxpay-payment-center" {
			return fmt.Errorf("支付回调地址路径必须是 /webhooks/wxpay-payment-center")
		}
	}
	if input.ReturnURL != "" {
		if err := validateReturnURLTemplate(input.ReturnURL); err != nil {
			return err
		}
	}
	if input.BaseURL != "" || input.AppID != "" || input.AppSecret != "" {
		if input.BaseURL == "" || input.AppID == "" {
			return fmt.Errorf("启用支付中心时必须填写地址和 App ID")
		}
		if input.CallbackURL == "" {
			return fmt.Errorf("启用支付中心时必须填写回调地址")
		}
	}
	values := map[string]string{
		paymentBaseURLKey: input.BaseURL, paymentAppIDKey: input.AppID,
		paymentCallbackKey: input.CallbackURL, paymentReturnKey: input.ReturnURL,
		paymentTTLKey: fmt.Sprintf("%d", input.OrderTTLMinutes),
	}
	if strings.TrimSpace(input.AppSecret) != "" {
		if s.vault == nil {
			return errors.New("credential vault is unavailable")
		}
		sealed, err := s.vault.Seal(paymentSecretName, strings.TrimSpace(input.AppSecret))
		if err != nil {
			return fmt.Errorf("保存支付密钥失败：%w", err)
		}
		values[paymentAppSecretKey] = sealed
	}
	return s.store.SetSettings(ctx, values)
}

func (s *Service) readSecret(ctx context.Context) (configured bool, secret string, err error) {
	value, ok, err := s.store.Setting(ctx, paymentAppSecretKey)
	if err != nil || !ok || strings.TrimSpace(value) == "" {
		return false, "", err
	}
	if s.vault == nil {
		return false, "", errors.New("credential vault is unavailable")
	}
	secret, err = s.vault.Open(paymentSecretName, value)
	if err != nil {
		return true, "", fmt.Errorf("支付密钥无法解密，请重新保存：%w", err)
	}
	return true, secret, nil
}

func (s *Service) providerConfig(ctx context.Context) (paymentcenter.Config, error) {
	view, err := s.Settings(ctx)
	if err != nil {
		return paymentcenter.Config{}, err
	}
	_, secret, err := s.readSecret(ctx)
	if err != nil {
		return paymentcenter.Config{}, err
	}
	cfg := paymentcenter.Config{BaseURL: view.BaseURL, AppID: view.AppID, AppSecret: secret, SignatureWindow: 5 * time.Minute}
	if !cfg.Configured() {
		return paymentcenter.Config{}, ErrPaymentNotReady
	}
	if err := cfg.Validate(); err != nil {
		return paymentcenter.Config{}, err
	}
	return cfg, nil
}

func (s *Service) ListPlans(ctx context.Context, kind string, enabledOnly bool) ([]sqlite.PaymentPlan, error) {
	return s.store.ListPaymentPlans(ctx, kind, enabledOnly)
}

func (s *Service) ListOrders(ctx context.Context, filter sqlite.PaymentOrderFilter) (sqlite.PaymentOrderPage, error) {
	if filter.Status != "" && !isPaymentOrderStatus(filter.Status) {
		filter.Status = ""
	}
	if filter.Kind != "" && filter.Kind != KindActivation && filter.Kind != KindRenewal {
		filter.Kind = ""
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if query := []rune(filter.Query); len(query) > 100 {
		filter.Query = string(query[:100])
	}
	return s.store.ListPaymentOrders(ctx, filter)
}

func isPaymentOrderStatus(status string) bool {
	switch status {
	case "pending", "paid", "expired", "canceled", "failed":
		return true
	default:
		return false
	}
}

func (s *Service) FindPlan(ctx context.Context, id int64) (sqlite.PaymentPlan, error) {
	return s.store.FindPaymentPlan(ctx, id)
}

func (s *Service) DeletePlan(ctx context.Context, id int64) error {
	return s.store.DeletePaymentPlan(ctx, id)
}

func (s *Service) CreatePlan(ctx context.Context, input sqlite.CreatePaymentPlanInput) (sqlite.PaymentPlan, error) {
	if err := validatePlan(input.Kind, input.Name, input.DurationDays, input.PriceFen, input.Note); err != nil {
		return sqlite.PaymentPlan{}, err
	}
	if input.DurationMinutes == 0 {
		input.DurationMinutes = input.DurationDays * 24 * 60
	}
	return s.store.CreatePaymentPlan(ctx, input, s.now().UTC())
}

func (s *Service) UpdatePlan(ctx context.Context, id int64, input sqlite.UpdatePaymentPlanInput) (sqlite.PaymentPlan, error) {
	plan, err := s.store.FindPaymentPlan(ctx, id)
	if err != nil {
		return sqlite.PaymentPlan{}, err
	}
	if err := validatePlan(plan.Kind, input.Name, input.DurationDays, input.PriceFen, input.Note); err != nil {
		return sqlite.PaymentPlan{}, err
	}
	if input.DurationMinutes == 0 {
		input.DurationMinutes = input.DurationDays * 24 * 60
	}
	return s.store.UpdatePaymentPlan(ctx, id, input, s.now().UTC())
}

func (s *Service) SetPlanEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.store.SetPaymentPlanEnabled(ctx, id, enabled, s.now().UTC())
}

func validatePlan(kind, name string, days, price int, _ string) error {
	if kind != KindActivation && kind != KindRenewal {
		return fmt.Errorf("套餐类型无效")
	}
	if strings.TrimSpace(name) == "" || len([]rune(strings.TrimSpace(name))) > 100 {
		return fmt.Errorf("套餐名称不能为空且不超过 100 个字符")
	}
	if days < 1 || days > 3650 {
		return fmt.Errorf("天数必须在 1 到 3650 天之间")
	}
	if price < 1 || price > 100_000_000 {
		return fmt.Errorf("价格必须在 0.01 到 1,000,000 元之间")
	}
	return nil
}

func (s *Service) CreateActivationOrder(ctx context.Context, planID int64, buyerInfo ...string) (sqlite.PaymentOrder, error) {
	buyer := ""
	if len(buyerInfo) > 0 {
		buyer = buyerInfo[0]
	}
	return s.createOrder(ctx, KindActivation, planID, nil, "", buyer)
}

func (s *Service) CreateRenewalOrder(ctx context.Context, planID int64, username, password string) (sqlite.PaymentOrder, error) {
	username = strings.TrimSpace(username)
	if err := s.accounts.VerifyPassword(ctx, username, password); err != nil {
		return sqlite.PaymentOrder{}, errors.New("用户名或密码错误")
	}
	account, err := s.store.FindAccountByUsername(ctx, username)
	if err != nil {
		return sqlite.PaymentOrder{}, errors.New("账号不存在")
	}
	return s.createOrder(ctx, KindRenewal, planID, &account.ID, account.Username, account.Username)
}

func (s *Service) createOrder(ctx context.Context, kind string, planID int64, accountID *int64, username, buyerInfo string) (sqlite.PaymentOrder, error) {
	buyerInfo = strings.TrimSpace(buyerInfo)
	if len(buyerInfo) > 200 {
		return sqlite.PaymentOrder{}, errors.New("购买人或联系方式不能超过 200 个字符")
	}
	cfg, err := s.providerConfig(ctx)
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	plan, err := s.store.FindPaymentPlan(ctx, planID)
	if err != nil || !plan.Enabled || plan.Kind != kind {
		return sqlite.PaymentOrder{}, ErrPlanNotAvailable
	}
	view, err := s.Settings(ctx)
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	now := s.now().UTC()
	merchantNo, err := newMerchantOrderNo()
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	publicToken, err := newPublicToken()
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	returnURL, err := resolveReturnURL(view.ReturnURL, publicToken, merchantNo)
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	localOrder, err := s.store.CreatePaymentOrder(ctx, sqlite.CreatePaymentOrderInput{
		PublicToken: publicToken, MerchantOrderNo: merchantNo, Kind: kind, PlanID: plan.ID, PlanName: plan.Name,
		AccountID: accountID, AccountUsername: username, BuyerInfo: buyerInfo, DurationDays: plan.DurationDays, DurationMinutes: plan.DurationMinutes,
		AmountFen: plan.PriceFen, Currency: "CNY", ExpiresAt: now.Add(time.Duration(view.OrderTTLMinutes) * time.Minute), Now: now,
	})
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	providerOrder, err := s.center.CreateOrder(ctx, cfg, paymentcenter.CreateOrderInput{MerchantOrderNo: merchantNo, AmountFen: plan.PriceFen, Currency: "CNY", Subject: plan.Name, ExpiresInSeconds: view.OrderTTLMinutes * 60, Note: providerOrderNote(kind, username, buyerInfo), ReturnURL: returnURL})
	if err != nil {
		return localOrder, fmt.Errorf("创建微信支付订单失败：%w", err)
	}
	if providerOrder.MerchantOrderNo != merchantNo || providerOrder.AmountFen != plan.PriceFen || providerOrder.Currency != "CNY" {
		return localOrder, errors.New("支付中心返回的订单信息与本地套餐不一致")
	}
	expiresAt := parseProviderTime(providerOrder.ExpiresAt, localOrder.ExpiresAt)
	localOrder, err = s.store.UpdatePaymentProvider(ctx, sqlite.UpdatePaymentProviderInput{OrderID: localOrder.ID, ProviderStatus: providerOrder.Status, PaymentURL: providerOrder.PaymentURL, PaymentMemo: providerOrder.PaymentMemo, ExpiresAt: expiresAt, Now: now})
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	if providerOrder.Status == "PAID" {
		return s.fulfill(ctx, localOrder, paymentcenter.Notification{EventID: "provider-paid-" + merchantNo, MerchantOrderNo: merchantNo, AmountFen: providerOrder.AmountFen, Currency: providerOrder.Currency, PaidAt: parseProviderTime(providerOrder.PaidAt, now), PaymentIdempotencyKey: "provider-paid-" + merchantNo})
	}
	if isProviderCanceled(providerOrder.Status) || providerOrder.Status == "EXPIRED" {
		localStatus := "expired"
		if isProviderCanceled(providerOrder.Status) {
			localStatus = "canceled"
		}
		_ = s.store.SetPaymentOrderState(ctx, localOrder.ID, localStatus, providerOrder.Status, "", now)
		return s.store.FindPaymentOrder(ctx, localOrder.ID)
	}
	return localOrder, nil
}

func providerOrderNote(kind, username, buyerInfo string) string {
	if kind == KindRenewal {
		return "业务账号：" + strings.TrimSpace(username)
	}
	if buyer := strings.TrimSpace(buyerInfo); buyer != "" {
		return "购买人：" + buyer
	}
	return ""
}

func isProviderCanceled(status string) bool {
	return status == "CANCELED" || status == "CANCELLED"
}

func (s *Service) PublicOrder(ctx context.Context, token string) (sqlite.PaymentOrder, error) {
	order, err := s.store.FindPaymentOrderByToken(ctx, token)
	if err != nil {
		return sqlite.PaymentOrder{}, err
	}
	if order.PaymentStatus == "pending" && !order.ExpiresAt.After(s.now()) {
		_ = s.store.SetPaymentOrderState(ctx, order.ID, "expired", "EXPIRED", "订单已过期", s.now().UTC())
		return s.store.FindPaymentOrder(ctx, order.ID)
	}
	return order, nil
}

func (s *Service) HandleWebhook(ctx context.Context, headers http.Header, body []byte) error {
	cfg, err := s.providerConfig(ctx)
	if err != nil {
		return err
	}
	notification, err := s.center.VerifyNotification(cfg, headers, body)
	if err != nil {
		return err
	}
	order, err := s.store.FindPaymentOrderByMerchantNo(ctx, notification.MerchantOrderNo)
	if err != nil {
		return fmt.Errorf("payment order not found: %w", err)
	}
	_, err = s.fulfillWithHash(ctx, order, notification)
	return err
}

func (s *Service) fulfill(ctx context.Context, order sqlite.PaymentOrder, notification paymentcenter.Notification) (sqlite.PaymentOrder, error) {
	return s.fulfillWithHash(ctx, order, notification)
}

func (s *Service) fulfillWithHash(ctx context.Context, order sqlite.PaymentOrder, notification paymentcenter.Notification) (sqlite.PaymentOrder, error) {
	if order.FulfillmentStatus == "completed" {
		return s.store.FindPaymentOrder(ctx, order.ID)
	}
	activationCode, activationHash, prefix := "", "", ""
	if order.Kind == KindActivation {
		var err error
		activationCode, err = invites.NewActivationCode()
		if err != nil {
			return sqlite.PaymentOrder{}, err
		}
		activationHash = invites.HashCode(activationCode)
		prefix = activationCode[:8]
	}
	return s.store.FulfillPaymentOrder(ctx, sqlite.FulfillPaymentOrderInput{
		OrderID: order.ID, EventID: notification.EventID, EventType: "order.paid", AmountFen: notification.AmountFen,
		Currency: notification.Currency, ProviderPaymentKey: notification.PaymentIdempotencyKey, PayloadHash: notification.PayloadHash,
		PaidAt: notification.PaidAt, Now: s.now().UTC(), ActivationCode: activationCode, ActivationCodeHash: activationHash, ActivationCodePrefix: prefix,
	})
}

func (s *Service) Reconcile(ctx context.Context) error {
	cfg, err := s.providerConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrPaymentNotReady) {
			return nil
		}
		return err
	}
	orders, err := s.store.ListPendingPaymentOrders(ctx, 50)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	providerOrders := make(map[string]paymentcenter.Order)
	if listed, listErr := s.center.ListOrders(ctx, cfg, 200, ""); listErr == nil {
		for _, providerOrder := range listed {
			providerOrders[providerOrder.MerchantOrderNo] = providerOrder
		}
	}
	var firstErr error
	for _, order := range orders {
		providerOrder, found := providerOrders[order.MerchantOrderNo]
		var queryErr error
		if !found {
			// The list endpoint is bounded to the newest 200 records. Fall back
			// to the exact query for older local orders or older R Pay versions.
			providerOrder, queryErr = s.center.QueryOrder(ctx, cfg, order.MerchantOrderNo)
		}
		if queryErr != nil {
			if firstErr == nil {
				firstErr = queryErr
			}
			continue
		}
		if providerOrder.MerchantOrderNo != order.MerchantOrderNo || providerOrder.AmountFen != order.AmountFen || providerOrder.Currency != order.Currency {
			if firstErr == nil {
				firstErr = fmt.Errorf("payment center order mismatch for %s", order.MerchantOrderNo)
			}
			continue
		}
		switch {
		case providerOrder.Status == "PAID":
			_, queryErr = s.fulfill(ctx, order, paymentcenter.Notification{EventID: "reconcile-" + order.MerchantOrderNo, MerchantOrderNo: order.MerchantOrderNo, AmountFen: providerOrder.AmountFen, Currency: providerOrder.Currency, PaidAt: parseProviderTime(providerOrder.PaidAt, now), PaymentIdempotencyKey: "reconcile-" + order.MerchantOrderNo})
		case isProviderCanceled(providerOrder.Status):
			queryErr = s.store.SetPaymentOrderState(ctx, order.ID, "canceled", providerOrder.Status, "", now)
		case providerOrder.Status == "EXPIRED":
			queryErr = s.store.SetPaymentOrderState(ctx, order.ID, "expired", providerOrder.Status, "", now)
		case providerOrder.Status == "PENDING" && !order.ExpiresAt.After(now):
			// R Pay supports explicit soft cancellation. Use it when the local
			// snapshot has expired but the provider has not yet transitioned.
			canceledOrder, cancelErr := s.center.CancelOrder(ctx, cfg, order.MerchantOrderNo)
			if cancelErr != nil {
				queryErr = cancelErr
				break
			}
			if canceledOrder.MerchantOrderNo != order.MerchantOrderNo || canceledOrder.AmountFen != order.AmountFen || canceledOrder.Currency != order.Currency {
				queryErr = fmt.Errorf("payment center cancellation mismatch for %s", order.MerchantOrderNo)
				break
			}
			switch {
			case isProviderCanceled(canceledOrder.Status):
				queryErr = s.store.SetPaymentOrderState(ctx, order.ID, "canceled", canceledOrder.Status, "", now)
			case canceledOrder.Status == "EXPIRED":
				queryErr = s.store.SetPaymentOrderState(ctx, order.ID, "expired", canceledOrder.Status, "", now)
			case canceledOrder.Status == "PAID":
				_, queryErr = s.fulfill(ctx, order, paymentcenter.Notification{EventID: "reconcile-" + order.MerchantOrderNo, MerchantOrderNo: order.MerchantOrderNo, AmountFen: canceledOrder.AmountFen, Currency: canceledOrder.Currency, PaidAt: parseProviderTime(canceledOrder.PaidAt, now), PaymentIdempotencyKey: "reconcile-" + order.MerchantOrderNo})
			default:
				queryErr = fmt.Errorf("payment center returned unexpected cancellation status %q for %s", canceledOrder.Status, order.MerchantOrderNo)
			}
		}
		if queryErr != nil && firstErr == nil {
			firstErr = queryErr
		}
	}
	return firstErr
}

func resolveReturnURL(template, publicToken, merchantOrderNo string) (string, error) {
	value := strings.TrimSpace(template)
	if value == "" {
		return "", nil
	}
	resolved := strings.NewReplacer(
		"{token}", publicToken,
		"{order_no}", merchantOrderNo,
		"{merchant_order_no}", merchantOrderNo,
	).Replace(value)
	if err := validateReturnURL(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func validateReturnURLTemplate(value string) error {
	if strings.ContainsAny(value, "{}") {
		resolved := strings.NewReplacer("{token}", "EUM-SAMPLE-TOKEN", "{order_no}", "EUM-SAMPLE-ORDER", "{merchant_order_no}", "EUM-SAMPLE-ORDER").Replace(value)
		if strings.ContainsAny(resolved, "{}") {
			return fmt.Errorf("支付后跳转地址只支持 {token}、{order_no} 或 {merchant_order_no} 占位符")
		}
		return validateReturnURL(resolved)
	}
	return validateReturnURL(value)
}

func validateReturnURL(value string) error {
	if len(value) > 2048 {
		return fmt.Errorf("支付后跳转地址不能超过 2048 个字符")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("支付后跳转地址必须是完整的 HTTPS 地址，不能包含用户名、密码或片段")
	}
	return nil
}

func parseProviderTime(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	return fallback.UTC()
}

func parsePositiveInt(value string) (int, error) {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil || parsed < 1 {
		return 0, errors.New("not a positive integer")
	}
	return parsed, nil
}

func isLocalHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func newPublicToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func newMerchantOrderNo() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "EUM-" + time.Now().UTC().Format("20060102150405") + "-" + base64.RawURLEncoding.EncodeToString(value), nil
}
