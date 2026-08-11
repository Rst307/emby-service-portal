package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func (s *Store) BeginAccountCreateOperation(ctx context.Context, input domain.BeginAccountCreateOperationInput) (domain.AccountCreateOperation, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	operation, err := findAccountCreateOperation(ctx, conn, input.Kind, input.IdempotencyKey)
	if err == nil {
		if operation.RequestFingerprint != input.RequestFingerprint {
			return domain.AccountCreateOperation{}, domain.ErrIdempotencyKeyConflict
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return domain.AccountCreateOperation{}, err
		}
		committed = true
		return operation, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountCreateOperation{}, err
	}
	var existingAccountID int64
	if err := conn.QueryRowContext(ctx, `SELECT id FROM accounts WHERE username = ?`, input.Username).Scan(&existingAccountID); err == nil {
		return domain.AccountCreateOperation{}, domain.ErrAccountAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountCreateOperation{}, err
	}

	now := input.Now.UTC()
	result, err := conn.ExecContext(ctx, `INSERT INTO account_create_operations(kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, input.Kind, input.IdempotencyKey, input.RequestFingerprint, input.Username, input.PasswordCiphertext, timestamp(input.ExpiresAt), input.Note, timestamp(now), timestamp(now))
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	operation.ID, err = result.LastInsertId()
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	committed = true
	return domain.AccountCreateOperation{ID: operation.ID, Kind: input.Kind, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: input.RequestFingerprint, Username: input.Username, PasswordCiphertext: input.PasswordCiphertext, ExpiresAt: input.ExpiresAt.UTC(), Note: input.Note, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
}

// BeginRegistrationOperation atomically reserves an invite use and records
// the full registration saga. Replays find the operation before examining the
// invite, so an expired, disabled, or exhausted invite can still be resumed.
func (s *Store) BeginRegistrationOperation(ctx context.Context, input domain.BeginRegistrationOperationInput) (domain.AccountCreateOperation, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	operation, err := findAccountCreateOperation(ctx, conn, input.Kind, input.IdempotencyKey)
	if err == nil {
		if operation.RequestFingerprint != input.RequestFingerprint {
			return domain.AccountCreateOperation{}, domain.ErrIdempotencyKeyConflict
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return domain.AccountCreateOperation{}, err
		}
		committed = true
		return operation, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountCreateOperation{}, err
	}
	var existingAccountID int64
	if err := conn.QueryRowContext(ctx, `SELECT id FROM accounts WHERE username = ?`, input.Username).Scan(&existingAccountID); err == nil {
		return domain.AccountCreateOperation{}, domain.ErrAccountAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountCreateOperation{}, err
	}

	invite, err := scanInvite(conn.QueryRowContext(ctx, `SELECT id, code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at FROM invite_codes WHERE code_hash = ?`, input.InviteCodeHash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.AccountCreateOperation{}, domain.ErrInviteNotRedeemable
		}
		return domain.AccountCreateOperation{}, err
	}
	now := input.Now.UTC()
	if !invite.Enabled || invite.DurationMinutes < 1 ||
		(invite.StartsAt != nil && now.Before(*invite.StartsAt)) ||
		(invite.ExpiresAt != nil && !now.Before(*invite.ExpiresAt)) ||
		(invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses) {
		return domain.AccountCreateOperation{}, domain.ErrInviteNotRedeemable
	}
	updatedInvite, err := conn.ExecContext(ctx, `UPDATE invite_codes SET used_count = used_count + 1 WHERE id = ? AND used_count = ?`, invite.ID, invite.UsedCount)
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	changed, err := updatedInvite.RowsAffected()
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if changed != 1 {
		return domain.AccountCreateOperation{}, domain.ErrInviteNotRedeemable
	}
	invite.UsedCount++
	expiresAt := now.Add(time.Duration(invite.DurationMinutes) * time.Minute)
	result, err := conn.ExecContext(ctx, `INSERT INTO account_create_operations(kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, invite_code_hash, invite_code_id, invite_duration_days, invite_duration_minutes, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, input.Kind, input.IdempotencyKey, input.RequestFingerprint, input.Username, input.PasswordCiphertext, timestamp(expiresAt), input.Note, input.InviteCodeHash, invite.ID, invite.DurationDays, invite.DurationMinutes, timestamp(now), timestamp(now))
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	operation.ID, err = result.LastInsertId()
	if err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	committed = true
	inviteID := invite.ID
	return domain.AccountCreateOperation{ID: operation.ID, Kind: input.Kind, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: input.RequestFingerprint, Username: input.Username, PasswordCiphertext: input.PasswordCiphertext, ExpiresAt: expiresAt, Note: input.Note, InviteCodeHash: input.InviteCodeHash, InviteCodeID: &inviteID, InviteDurationDays: invite.DurationDays, InviteDurationMins: invite.DurationMinutes, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
}

// MarkAccountCreateOperationCreating checkpoints an uncertain remote create
// before issuing it. A later retry can use a username lookup rather than
// blindly sending a second create request.
func (s *Store) MarkAccountCreateOperationCreating(ctx context.Context, operationID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE account_create_operations SET status = 'creating', updated_at = ? WHERE id = ? AND status = 'pending'`, timestamp(now), operationID)
	return err
}

func (s *Store) SaveAccountCreateOperationRemote(ctx context.Context, operationID int64, embyUserID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE account_create_operations SET emby_user_id = ?, status = 'remote_created', updated_at = ? WHERE id = ? AND status <> 'completed' AND (emby_user_id IS NULL OR emby_user_id = ?)`, embyUserID, timestamp(now), operationID, embyUserID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 0 {
		return nil
	}
	operation, err := s.FindAccountCreateOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.EmbyUserID == embyUserID {
		return nil
	}
	return fmt.Errorf("account create operation has a different Emby user")
}

func (s *Store) FindAccountCreateOperation(ctx context.Context, id int64) (domain.AccountCreateOperation, error) {
	return scanAccountCreateOperation(s.db.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE id = ?`, id))
}

func (s *Store) ListIncompleteAccountCreateOperations(ctx context.Context, limit int) ([]domain.AccountCreateOperation, error) {
	if limit < 1 {
		return []domain.AccountCreateOperation{}, nil
	}
	rows, err := s.db.QueryContext(ctx, accountCreateOperationSelect+` WHERE status <> 'completed' ORDER BY updated_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := make([]domain.AccountCreateOperation, 0)
	for rows.Next() {
		operation, err := scanAccountCreateOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// CompleteAccountCreateOperation performs every local finalization write in
// one transaction. Thus a failure leaves the remote checkpoint and, for a
// registration, its already-reserved invite use intact for a safe retry.
func (s *Store) CompleteAccountCreateOperation(ctx context.Context, operationID int64, now time.Time) (domain.Account, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return domain.Account{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	operation, err := scanAccountCreateOperation(conn.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE id = ?`, operationID))
	if err != nil {
		return domain.Account{}, err
	}
	if operation.Status == "completed" {
		account, err := scanAccount(conn.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE id = ?`, operation.AccountID))
		if err != nil {
			return domain.Account{}, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return domain.Account{}, err
		}
		committed = true
		return account, nil
	}
	if operation.EmbyUserID == "" {
		return domain.Account{}, fmt.Errorf("account create operation has no Emby user")
	}

	finalizedAt := now.UTC()
	status := "active"
	var disabledAt *time.Time
	if !operation.ExpiresAt.After(finalizedAt) {
		status = "expired"
		disabledAt = &finalizedAt
	}
	result, err := conn.ExecContext(ctx, `INSERT INTO accounts(emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, operation.EmbyUserID, operation.Username, status, timestamp(operation.ExpiresAt), operation.Note, timestamp(finalizedAt), timestamp(finalizedAt), nullableTimestamp(disabledAt))
	if err != nil {
		return domain.Account{}, err
	}
	accountID, err := result.LastInsertId()
	if err != nil {
		return domain.Account{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO account_credentials(account_id, password_ciphertext, updated_at) VALUES (?, ?, ?)`, accountID, operation.PasswordCiphertext, timestamp(finalizedAt)); err != nil {
		return domain.Account{}, err
	}
	if operation.Kind == "register" {
		if operation.InviteCodeID == nil {
			return domain.Account{}, fmt.Errorf("registration operation has no invite")
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO invite_redemptions(invite_code_id, account_id, kind, duration_days, duration_minutes, redeemed_at) VALUES (?, ?, 'register', ?, ?, ?)`, *operation.InviteCodeID, accountID, operation.InviteDurationDays, operation.InviteDurationMins, timestamp(finalizedAt)); err != nil {
			return domain.Account{}, err
		}
	}
	if status != "active" {
		if err := upsertAccessSyncJob(ctx, conn, accountID, true, finalizedAt); err != nil {
			return domain.Account{}, err
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE account_create_operations SET account_id = ?, status = 'completed', updated_at = ?, completed_at = ? WHERE id = ?`, accountID, timestamp(finalizedAt), timestamp(finalizedAt), operationID); err != nil {
		return domain.Account{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return domain.Account{}, err
	}
	committed = true
	return domain.Account{ID: accountID, Version: 1, EmbyUserID: operation.EmbyUserID, Username: operation.Username, Status: status, ExpiresAt: operation.ExpiresAt, Note: operation.Note, CreatedAt: finalizedAt, UpdatedAt: finalizedAt, DisabledAt: disabledAt}, nil
}

const accountCreateOperationSelect = `SELECT id, kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, invite_code_hash, invite_code_id, invite_duration_days, invite_duration_minutes, emby_user_id, account_id, status, created_at, updated_at, completed_at FROM account_create_operations`

type accountCreateOperationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findAccountCreateOperation(ctx context.Context, queryer accountCreateOperationQueryer, kind, idempotencyKey string) (domain.AccountCreateOperation, error) {
	return scanAccountCreateOperation(queryer.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE kind = ? AND idempotency_key = ?`, kind, idempotencyKey))
}

type accountCreateOperationScanner interface{ Scan(...any) error }

func scanAccountCreateOperation(row accountCreateOperationScanner) (domain.AccountCreateOperation, error) {
	var operation domain.AccountCreateOperation
	var expiresAt, createdAt, updatedAt string
	var inviteID, accountID sql.NullInt64
	var inviteCodeHash, embyUserID, completedAt sql.NullString
	if err := row.Scan(&operation.ID, &operation.Kind, &operation.IdempotencyKey, &operation.RequestFingerprint, &operation.Username, &operation.PasswordCiphertext, &expiresAt, &operation.Note, &inviteCodeHash, &inviteID, &operation.InviteDurationDays, &operation.InviteDurationMins, &embyUserID, &accountID, &operation.Status, &createdAt, &updatedAt, &completedAt); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	var err error
	if operation.ExpiresAt, err = parseTimestamp(expiresAt); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if operation.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if operation.UpdatedAt, err = parseTimestamp(updatedAt); err != nil {
		return domain.AccountCreateOperation{}, err
	}
	if inviteCodeHash.Valid {
		operation.InviteCodeHash = inviteCodeHash.String
	}
	if inviteID.Valid {
		value := inviteID.Int64
		operation.InviteCodeID = &value
	}
	if accountID.Valid {
		value := accountID.Int64
		operation.AccountID = &value
	}
	if embyUserID.Valid {
		operation.EmbyUserID = embyUserID.String
	}
	if completedAt.Valid {
		value, err := parseTimestamp(completedAt.String)
		if err != nil {
			return domain.AccountCreateOperation{}, err
		}
		operation.CompletedAt = &value
	}
	return operation, nil
}
