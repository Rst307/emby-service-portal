package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const paymentPlanSelect = `SELECT id, kind, name, duration_days, duration_minutes, price_fen, note, enabled, sort_order, created_at, updated_at FROM payment_plans`

func (s *Store) CreatePaymentPlan(ctx context.Context, input CreatePaymentPlanInput, now time.Time) (PaymentPlan, error) {
	durationMinutes := input.DurationMinutes
	if durationMinutes == 0 {
		durationMinutes = input.DurationDays * 24 * 60
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO payment_plans(kind, name, duration_days, duration_minutes, price_fen, note, enabled, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, input.Kind, strings.TrimSpace(input.Name), input.DurationDays, durationMinutes, input.PriceFen, strings.TrimSpace(input.Note), input.SortOrder, timestamp(now), timestamp(now))
	if err != nil {
		return PaymentPlan{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PaymentPlan{}, err
	}
	return s.FindPaymentPlan(ctx, id)
}

func (s *Store) ListPaymentPlans(ctx context.Context, kind string, enabledOnly bool) ([]PaymentPlan, error) {
	query := paymentPlanSelect
	args := make([]any, 0, 2)
	conditions := make([]string, 0, 2)
	if kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, kind)
	}
	if enabledOnly {
		conditions = append(conditions, "enabled = 1")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY sort_order, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]PaymentPlan, 0)
	for rows.Next() {
		plan, err := scanPaymentPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *Store) FindPaymentPlan(ctx context.Context, id int64) (PaymentPlan, error) {
	return scanPaymentPlan(s.db.QueryRowContext(ctx, paymentPlanSelect+" WHERE id = ?", id))
}

func (s *Store) UpdatePaymentPlan(ctx context.Context, id int64, input UpdatePaymentPlanInput, now time.Time) (PaymentPlan, error) {
	durationMinutes := input.DurationMinutes
	if durationMinutes == 0 {
		durationMinutes = input.DurationDays * 24 * 60
	}
	result, err := s.db.ExecContext(ctx, `UPDATE payment_plans SET name = ?, duration_days = ?, duration_minutes = ?, price_fen = ?, note = ?, sort_order = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(input.Name), input.DurationDays, durationMinutes, input.PriceFen, strings.TrimSpace(input.Note), input.SortOrder, timestamp(now), id)
	if err != nil {
		return PaymentPlan{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PaymentPlan{}, err
	}
	if changed != 1 {
		return PaymentPlan{}, sql.ErrNoRows
	}
	return s.FindPaymentPlan(ctx, id)
}

func (s *Store) SetPaymentPlanEnabled(ctx context.Context, id int64, enabled bool, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE payment_plans SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, timestamp(now), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

const paymentOrderSelect = `SELECT id, public_token, merchant_order_no, kind, plan_id, plan_name, account_id, account_username, duration_days, duration_minutes, amount_fen, currency, payment_status, fulfillment_status, provider_status, payment_url, payment_memo, provider_payment_key, invite_id, failure_reason, expires_at, paid_at, created_at, updated_at FROM payment_orders`

func (s *Store) CreatePaymentOrder(ctx context.Context, input CreatePaymentOrderInput) (PaymentOrder, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO payment_orders(public_token, merchant_order_no, kind, plan_id, plan_name, account_id, account_username, duration_days, duration_minutes, amount_fen, currency, payment_status, fulfillment_status, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 'pending', ?, ?, ?)`, input.PublicToken, input.MerchantOrderNo, input.Kind, input.PlanID, input.PlanName, input.AccountID, strings.TrimSpace(input.AccountUsername), input.DurationDays, input.DurationMinutes, input.AmountFen, input.Currency, timestamp(input.ExpiresAt), timestamp(input.Now), timestamp(input.Now))
	if err != nil {
		return PaymentOrder{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PaymentOrder{}, err
	}
	return s.FindPaymentOrder(ctx, id)
}

func (s *Store) FindPaymentOrder(ctx context.Context, id int64) (PaymentOrder, error) {
	return s.findPaymentOrderWith(ctx, s.db, paymentOrderSelect+" WHERE id = ?", id)
}

func (s *Store) FindPaymentOrderByToken(ctx context.Context, token string) (PaymentOrder, error) {
	return s.findPaymentOrderWith(ctx, s.db, paymentOrderSelect+" WHERE public_token = ?", token)
}

func (s *Store) FindPaymentOrderByMerchantNo(ctx context.Context, merchantOrderNo string) (PaymentOrder, error) {
	return s.findPaymentOrderWith(ctx, s.db, paymentOrderSelect+" WHERE merchant_order_no = ?", merchantOrderNo)
}

func (s *Store) ListPendingPaymentOrders(ctx context.Context, limit int) ([]PaymentOrder, error) {
	if limit < 1 {
		return []PaymentOrder{}, nil
	}
	rows, err := s.db.QueryContext(ctx, paymentOrderSelect+` WHERE payment_status = 'pending' AND fulfillment_status = 'pending' ORDER BY updated_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]PaymentOrder, 0)
	for rows.Next() {
		order, err := scanPaymentOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *Store) UpdatePaymentProvider(ctx context.Context, input UpdatePaymentProviderInput) (PaymentOrder, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE payment_orders SET provider_status = ?, payment_url = ?, payment_memo = ?, expires_at = ?, updated_at = ? WHERE id = ? AND fulfillment_status = 'pending'`, input.ProviderStatus, input.PaymentURL, input.PaymentMemo, timestamp(input.ExpiresAt), timestamp(input.Now), input.OrderID)
	if err != nil {
		return PaymentOrder{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return PaymentOrder{}, err
	} else if changed != 1 {
		return s.FindPaymentOrder(ctx, input.OrderID)
	}
	return s.FindPaymentOrder(ctx, input.OrderID)
}

func (s *Store) SetPaymentOrderState(ctx context.Context, id int64, status, providerStatus, reason string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE payment_orders SET payment_status = CASE WHEN payment_status = 'paid' THEN payment_status ELSE ? END, provider_status = ?, failure_reason = ?, updated_at = ? WHERE id = ? AND fulfillment_status = 'pending'`, status, providerStatus, strings.TrimSpace(reason), timestamp(now), id)
	return err
}

// FulfillPaymentOrder atomically records a verified payment and delivers the
// configured benefit. It never calls an external system while holding SQLite.
func (s *Store) FulfillPaymentOrder(ctx context.Context, input FulfillPaymentOrderInput) (PaymentOrder, error) {
	if input.OrderID < 1 || strings.TrimSpace(input.EventID) == "" || input.AmountFen < 1 || input.Currency == "" || input.PaidAt.IsZero() || input.Now.IsZero() {
		return PaymentOrder{}, errors.New("invalid payment fulfillment input")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return PaymentOrder{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return PaymentOrder{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	order, err := scanPaymentOrder(conn.QueryRowContext(ctx, paymentOrderSelect+" WHERE id = ?", input.OrderID))
	if err != nil {
		return PaymentOrder{}, err
	}
	if order.FulfillmentStatus == "completed" {
		result, err := s.findPaymentOrderWith(ctx, conn, paymentOrderSelect+" WHERE id = ?", order.ID)
		if err != nil {
			return PaymentOrder{}, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return PaymentOrder{}, err
		}
		committed = true
		return result, nil
	}
	if order.AmountFen != input.AmountFen || order.Currency != input.Currency {
		return PaymentOrder{}, fmt.Errorf("payment amount or currency does not match local order")
	}
	var existingOrderID int64
	eventErr := conn.QueryRowContext(ctx, `SELECT payment_order_id FROM payment_events WHERE event_id = ?`, input.EventID).Scan(&existingOrderID)
	if eventErr == nil {
		if existingOrderID != order.ID {
			return PaymentOrder{}, fmt.Errorf("payment event belongs to another order")
		}
		result, err := s.findPaymentOrderWith(ctx, conn, paymentOrderSelect+" WHERE id = ?", order.ID)
		if err != nil {
			return PaymentOrder{}, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return PaymentOrder{}, err
		}
		committed = true
		return result, nil
	}
	if !errors.Is(eventErr, sql.ErrNoRows) {
		return PaymentOrder{}, eventErr
	}
	if input.EventType == "" {
		input.EventType = "order.paid"
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO payment_events(payment_order_id, event_id, event_type, amount_fen, currency, payload_hash, received_at, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, order.ID, input.EventID, input.EventType, input.AmountFen, input.Currency, input.PayloadHash, timestamp(input.Now), timestamp(input.Now)); err != nil {
		return PaymentOrder{}, err
	}

	paidAt := input.PaidAt.UTC()
	if _, err := conn.ExecContext(ctx, `UPDATE payment_orders SET payment_status = 'paid', provider_status = 'PAID', provider_payment_key = ?, paid_at = ?, failure_reason = '', updated_at = ? WHERE id = ? AND fulfillment_status = 'pending'`, input.ProviderPaymentKey, timestamp(paidAt), timestamp(input.Now), order.ID); err != nil {
		return PaymentOrder{}, err
	}

	var inviteID *int64
	switch order.Kind {
	case "activation":
		if strings.TrimSpace(input.ActivationCode) == "" || strings.TrimSpace(input.ActivationCodeHash) == "" || strings.TrimSpace(input.ActivationCodePrefix) == "" {
			return PaymentOrder{}, errors.New("activation code is required for activation fulfillment")
		}
		result, err := conn.ExecContext(ctx, `INSERT INTO invite_codes(code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at) VALUES (?, ?, ?, ?, ?, 1, 0, NULL, NULL, 1, ?, ?)`, input.ActivationCodeHash, input.ActivationCode, input.ActivationCodePrefix, order.DurationDays, order.DurationMinutes, "付费激活码 · "+order.PlanName, timestamp(input.Now))
		if err != nil {
			return PaymentOrder{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return PaymentOrder{}, err
		}
		inviteID = &id
	case "renewal":
		if order.AccountID == nil {
			return PaymentOrder{}, errors.New("renewal order has no account")
		}
		account, err := scanAccount(conn.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE id = ?`, *order.AccountID))
		if err != nil {
			return PaymentOrder{}, err
		}
		account.ExpiresAt = account.ExpiresAt.Add(time.Duration(order.DurationMinutes) * time.Minute)
		account.UpdatedAt = input.Now.UTC()
		reactivated := account.Status == "expired" && account.ExpiresAt.After(input.Now)
		if reactivated {
			account.Status = "active"
			account.DisabledAt = nil
		}
		result, err := conn.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, status = ?, disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, timestamp(account.ExpiresAt), account.Status, nullableTimestamp(account.DisabledAt), timestamp(account.UpdatedAt), account.ID, account.Version)
		if err != nil {
			return PaymentOrder{}, err
		}
		if changed, err := result.RowsAffected(); err != nil {
			return PaymentOrder{}, err
		} else if changed != 1 {
			return PaymentOrder{}, ErrAccountVersionConflict
		}
		if reactivated {
			if _, err := conn.ExecContext(ctx, `DELETE FROM user_sessions WHERE account_id = ?`, account.ID); err != nil {
				return PaymentOrder{}, err
			}
			if err := upsertAccessSyncJob(ctx, conn, account.ID, false, input.Now.UTC()); err != nil {
				return PaymentOrder{}, err
			}
		}
	default:
		return PaymentOrder{}, fmt.Errorf("unknown payment order kind %q", order.Kind)
	}

	if _, err := conn.ExecContext(ctx, `UPDATE payment_orders SET fulfillment_status = 'completed', invite_id = ?, updated_at = ? WHERE id = ?`, inviteID, timestamp(input.Now), order.ID); err != nil {
		return PaymentOrder{}, err
	}
	result, err := s.findPaymentOrderWith(ctx, conn, paymentOrderSelect+" WHERE id = ?", order.ID)
	if err != nil {
		return PaymentOrder{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return PaymentOrder{}, err
	}
	committed = true
	return result, nil
}

func (s *Store) findPaymentOrderWith(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) (PaymentOrder, error) {
	order, err := scanPaymentOrder(queryer.QueryRowContext(ctx, query, args...))
	if err != nil {
		return PaymentOrder{}, err
	}
	if order.InviteID != nil {
		if err := queryer.QueryRowContext(ctx, `SELECT COALESCE(code, '') FROM invite_codes WHERE id = ?`, *order.InviteID).Scan(&order.ActivationCode); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return PaymentOrder{}, err
		}
	}
	return order, nil
}

func scanPaymentPlan(row interface{ Scan(...any) error }) (PaymentPlan, error) {
	var plan PaymentPlan
	var enabled int
	var created, updated string
	if err := row.Scan(&plan.ID, &plan.Kind, &plan.Name, &plan.DurationDays, &plan.DurationMinutes, &plan.PriceFen, &plan.Note, &enabled, &plan.SortOrder, &created, &updated); err != nil {
		return PaymentPlan{}, err
	}
	var err error
	if plan.CreatedAt, err = parseTimestamp(created); err != nil {
		return PaymentPlan{}, err
	}
	if plan.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return PaymentPlan{}, err
	}
	plan.Enabled = enabled != 0
	return plan, nil
}

func scanPaymentOrder(row interface{ Scan(...any) error }) (PaymentOrder, error) {
	var order PaymentOrder
	var accountID, inviteID sql.NullInt64
	var paidAt sql.NullString
	var expires, created, updated string
	if err := row.Scan(&order.ID, &order.PublicToken, &order.MerchantOrderNo, &order.Kind, &order.PlanID, &order.PlanName, &accountID, &order.AccountUsername, &order.DurationDays, &order.DurationMinutes, &order.AmountFen, &order.Currency, &order.PaymentStatus, &order.FulfillmentStatus, &order.ProviderStatus, &order.PaymentURL, &order.PaymentMemo, &order.ProviderPaymentKey, &inviteID, &order.FailureReason, &expires, &paidAt, &created, &updated); err != nil {
		return PaymentOrder{}, err
	}
	if accountID.Valid {
		value := accountID.Int64
		order.AccountID = &value
	}
	if inviteID.Valid {
		value := inviteID.Int64
		order.InviteID = &value
	}
	var err error
	if order.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return PaymentOrder{}, err
	}
	if paidAt.Valid {
		value, err := parseTimestamp(paidAt.String)
		if err != nil {
			return PaymentOrder{}, err
		}
		order.PaidAt = &value
	}
	if order.CreatedAt, err = parseTimestamp(created); err != nil {
		return PaymentOrder{}, err
	}
	if order.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return PaymentOrder{}, err
	}
	return order, nil
}
