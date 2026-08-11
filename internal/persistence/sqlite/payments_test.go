package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func TestFulfillActivationPaymentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	plan, err := store.CreatePaymentPlan(ctx, domain.CreatePaymentPlanInput{Kind: "activation", Name: "月卡", DurationDays: 30, DurationMinutes: 30 * 24 * 60, PriceFen: 990}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := store.CreatePaymentOrder(ctx, domain.CreatePaymentOrderInput{PublicToken: "public-token", MerchantOrderNo: "ESP-ORDER-1", Kind: "activation", PlanID: plan.ID, PlanName: plan.Name, DurationDays: 30, DurationMinutes: 30 * 24 * 60, AmountFen: 990, Currency: "CNY", ExpiresAt: now.Add(15 * time.Minute), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.FulfillPaymentOrderInput{OrderID: order.ID, EventID: "evt-1", EventType: "order.paid", AmountFen: 990, Currency: "CNY", ProviderPaymentKey: "wechat-1", PayloadHash: "hash", PaidAt: now.Add(time.Minute), Now: now.Add(time.Minute), ActivationCode: "ESP-ACT-test-code", ActivationCodeHash: "code-hash", ActivationCodePrefix: "ESP-ACT-"}
	fulfilled, err := store.FulfillPaymentOrder(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if fulfilled.FulfillmentStatus != "completed" || fulfilled.ActivationCode != "ESP-ACT-test-code" || fulfilled.InviteID == nil {
		t.Fatalf("fulfilled = %+v", fulfilled)
	}
	second, err := store.FulfillPaymentOrder(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != fulfilled.ID || second.ActivationCode != fulfilled.ActivationCode {
		t.Fatalf("second fulfillment = %+v", second)
	}
	var inviteCount, eventCount int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM invite_codes").Scan(&inviteCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM payment_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if inviteCount != 1 || eventCount != 1 {
		t.Fatalf("invite count = %d, event count = %d", inviteCount, eventCount)
	}
}

func TestDeletePaymentPlanProtectsReferencedOrders(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	unused, err := store.CreatePaymentPlan(ctx, domain.CreatePaymentPlanInput{Kind: "activation", Name: "未使用方案", DurationDays: 30, PriceFen: 990}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePaymentPlan(ctx, unused.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindPaymentPlan(ctx, unused.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted plan lookup error = %v", err)
	}

	used, err := store.CreatePaymentPlan(ctx, domain.CreatePaymentPlanInput{Kind: "activation", Name: "已有订单方案", DurationDays: 30, PriceFen: 990}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePaymentOrder(ctx, domain.CreatePaymentOrderInput{PublicToken: "delete-test-token", MerchantOrderNo: "ESP-DELETE-TEST", Kind: "activation", PlanID: used.ID, PlanName: used.Name, DurationDays: used.DurationDays, DurationMinutes: used.DurationMinutes, AmountFen: used.PriceFen, Currency: "CNY", ExpiresAt: now.Add(time.Hour), Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePaymentPlan(ctx, used.ID); !errors.Is(err, domain.ErrPaymentPlanInUse) {
		t.Fatalf("delete referenced plan error = %v", err)
	}
}

func TestListPaymentOrdersSearchesBuyerAndSummarizes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	plan, err := store.CreatePaymentPlan(ctx, domain.CreatePaymentPlanInput{Kind: "activation", Name: "月卡", DurationDays: 30, DurationMinutes: 30 * 24 * 60, PriceFen: 990}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreatePaymentOrder(ctx, domain.CreatePaymentOrderInput{PublicToken: "buyer-token-1", MerchantOrderNo: "ESP-BUYER-1", Kind: "activation", PlanID: plan.ID, PlanName: plan.Name, BuyerInfo: "张三 / wx-z3", DurationDays: 30, DurationMinutes: 30 * 24 * 60, AmountFen: 990, Currency: "CNY", ExpiresAt: now.Add(time.Hour), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPaymentOrderState(ctx, first.ID, "paid", "PAID", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePaymentOrder(ctx, domain.CreatePaymentOrderInput{PublicToken: "buyer-token-2", MerchantOrderNo: "ESP-BUYER-2", Kind: "activation", PlanID: plan.ID, PlanName: plan.Name, BuyerInfo: "李四", DurationDays: 30, DurationMinutes: 30 * 24 * 60, AmountFen: 990, Currency: "CNY", ExpiresAt: now.Add(time.Hour), Now: now}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListPaymentOrders(ctx, domain.PaymentOrderFilter{Query: "张三", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.PaidCount != 1 || page.PaidAmountFen != 990 || len(page.Orders) != 1 || page.Orders[0].BuyerInfo != "张三 / wx-z3" {
		t.Fatalf("order page = %+v", page)
	}
}

func TestFulfillRenewalPaymentExtendsExpiredAccountAndQueuesAccess(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{EmbyUserID: "emby-user", Username: "alice", Status: "expired", ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreatePaymentPlan(ctx, domain.CreatePaymentPlanInput{Kind: "renewal", Name: "续费月卡", DurationDays: 30, DurationMinutes: 30 * 24 * 60, PriceFen: 990}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := store.CreatePaymentOrder(ctx, domain.CreatePaymentOrderInput{PublicToken: "renewal-token", MerchantOrderNo: "ESP-ORDER-2", Kind: "renewal", PlanID: plan.ID, PlanName: plan.Name, AccountID: &account.ID, AccountUsername: account.Username, DurationDays: 30, DurationMinutes: 30 * 24 * 60, AmountFen: 990, Currency: "CNY", ExpiresAt: now.Add(15 * time.Minute), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FulfillPaymentOrder(ctx, domain.FulfillPaymentOrderInput{OrderID: order.ID, EventID: "evt-2", EventType: "order.paid", AmountFen: 990, Currency: "CNY", ProviderPaymentKey: "wechat-2", PaidAt: now, PayloadHash: "hash", Now: now}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "active" || !updated.ExpiresAt.Equal(now.Add(30*24*time.Hour-time.Hour)) {
		t.Fatalf("account = %+v", updated)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].DesiredDisabled {
		t.Fatalf("access jobs = %+v", jobs)
	}
}
