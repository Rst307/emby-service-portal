package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) CreateInvite(ctx context.Context, invite InviteCode) (InviteCode, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO invite_codes(code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`, invite.CodeHash, invite.Code, invite.CodePrefix, invite.DurationDays, invite.DurationMinutes, invite.MaxUses, nullableTimestamp(invite.StartsAt), nullableTimestamp(invite.ExpiresAt), invite.Enabled, invite.Note, timestamp(invite.CreatedAt))
	if err != nil {
		return InviteCode{}, err
	}
	invite.ID, err = result.LastInsertId()
	if err != nil {
		return InviteCode{}, err
	}
	return invite, nil
}
func (s *Store) ListInvites(ctx context.Context) ([]InviteCode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at FROM invite_codes ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invites := make([]InviteCode, 0)
	for rows.Next() {
		invite, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	return invites, rows.Err()
}
func (s *Store) SetInviteEnabled(ctx context.Context, id int64, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE invite_codes SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return err
	}
	if changes, err := result.RowsAffected(); err != nil {
		return err
	} else if changes == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) DeleteInvite(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM invite_codes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changes, err := result.RowsAffected(); err != nil {
		return err
	} else if changes == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) ConsumeInvite(ctx context.Context, codeHash string, now time.Time) (InviteCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InviteCode{}, err
	}
	defer tx.Rollback()
	invite, err := scanInvite(tx.QueryRowContext(ctx, `SELECT id, code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at FROM invite_codes WHERE code_hash = ?`, codeHash))
	if err != nil {
		return InviteCode{}, err
	}
	if !invite.Enabled || (invite.StartsAt != nil && now.Before(*invite.StartsAt)) || (invite.ExpiresAt != nil && !now.Before(*invite.ExpiresAt)) || (invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses) {
		return InviteCode{}, fmt.Errorf("invite unavailable")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invite_codes SET used_count = used_count + 1 WHERE id = ?`, invite.ID); err != nil {
		return InviteCode{}, err
	}
	invite.UsedCount++
	if err := tx.Commit(); err != nil {
		return InviteCode{}, err
	}
	return invite, nil
}
func (s *Store) ReleaseInvite(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE invite_codes SET used_count = CASE WHEN used_count > 0 THEN used_count - 1 ELSE 0 END WHERE id = ?`, id)
	return err
}
func (s *Store) RecordRedemption(ctx context.Context, inviteID, accountID int64, kind string, durationDays, durationMinutes int, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO invite_redemptions(invite_code_id, account_id, kind, duration_days, duration_minutes, redeemed_at) VALUES (?, ?, ?, ?, ?, ?)`, inviteID, accountID, kind, durationDays, durationMinutes, timestamp(now))
	return err
}

// RedeemRenewal atomically consumes an eligible invite, extends a business
// account from its existing expiry, and records the redemption. BEGIN
// IMMEDIATE serializes the read-modify-write sequence across SQLite clients;
// a failed operation rolls back all three effects.
func (s *Store) RedeemRenewal(ctx context.Context, input RedeemRenewalInput) (RenewalRedemption, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return RenewalRedemption{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return RenewalRedemption{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	account, err := scanAccount(conn.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE username = ?`, input.Username))
	if err != nil {
		return RenewalRedemption{}, err
	}
	invite, err := scanInvite(conn.QueryRowContext(ctx, `SELECT id, code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at FROM invite_codes WHERE code_hash = ?`, input.CodeHash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RenewalRedemption{}, ErrInviteNotRedeemable
		}
		return RenewalRedemption{}, err
	}

	now := input.RedeemedAt.UTC()
	if now.IsZero() || !invite.Enabled || invite.DurationMinutes < 1 ||
		(invite.StartsAt != nil && now.Before(*invite.StartsAt)) ||
		(invite.ExpiresAt != nil && !now.Before(*invite.ExpiresAt)) ||
		(invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses) {
		return RenewalRedemption{}, ErrInviteNotRedeemable
	}
	updatedInvite, err := conn.ExecContext(ctx, `UPDATE invite_codes SET used_count = used_count + 1 WHERE id = ? AND used_count = ?`, invite.ID, invite.UsedCount)
	if err != nil {
		return RenewalRedemption{}, err
	}
	changed, err := updatedInvite.RowsAffected()
	if err != nil {
		return RenewalRedemption{}, err
	}
	if changed != 1 {
		return RenewalRedemption{}, ErrInviteNotRedeemable
	}
	invite.UsedCount++

	account.ExpiresAt = account.ExpiresAt.Add(time.Duration(invite.DurationMinutes) * time.Minute)
	account.UpdatedAt = now
	reactivated := account.Status == "expired" && account.ExpiresAt.After(now)
	if reactivated {
		account.Status = "active"
		account.DisabledAt = nil
	}
	if _, err := conn.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, status = ?, disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ?`, timestamp(account.ExpiresAt), account.Status, nullableTimestamp(account.DisabledAt), timestamp(account.UpdatedAt), account.ID); err != nil {
		return RenewalRedemption{}, err
	}
	account.Version++
	if _, err := conn.ExecContext(ctx, `INSERT INTO invite_redemptions(invite_code_id, account_id, kind, duration_days, duration_minutes, redeemed_at) VALUES (?, ?, 'renew', ?, ?, ?)`, invite.ID, account.ID, invite.DurationDays, invite.DurationMinutes, timestamp(now)); err != nil {
		return RenewalRedemption{}, err
	}
	if reactivated {
		if err := upsertAccessSyncJob(ctx, conn, account.ID, false, now); err != nil {
			return RenewalRedemption{}, err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return RenewalRedemption{}, err
	}
	committed = true
	return RenewalRedemption{Account: account, Invite: invite, Reactivated: reactivated}, nil
}

type inviteScanner interface{ Scan(...any) error }

func scanInvite(row inviteScanner) (InviteCode, error) {
	var invite InviteCode
	var code, starts, expires sql.NullString
	var enabled int
	var created string
	err := row.Scan(&invite.ID, &invite.CodeHash, &code, &invite.CodePrefix, &invite.DurationDays, &invite.DurationMinutes, &invite.MaxUses, &invite.UsedCount, &starts, &expires, &enabled, &invite.Note, &created)
	if err != nil {
		return InviteCode{}, err
	}
	if code.Valid {
		invite.Code = code.String
	}
	var parseErr error
	if starts.Valid {
		parsed, err := parseTimestamp(starts.String)
		if err != nil {
			return InviteCode{}, err
		}
		invite.StartsAt = &parsed
	}
	if expires.Valid {
		parsed, err := parseTimestamp(expires.String)
		if err != nil {
			return InviteCode{}, err
		}
		invite.ExpiresAt = &parsed
	}
	invite.Enabled = enabled != 0
	invite.CreatedAt, parseErr = parseTimestamp(created)
	if parseErr != nil {
		return InviteCode{}, parseErr
	}
	return invite, nil
}
