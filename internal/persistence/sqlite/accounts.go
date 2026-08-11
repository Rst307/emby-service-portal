package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func (s *Store) CreateAccount(ctx context.Context, account domain.Account) (domain.Account, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO accounts(emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, account.EmbyUserID, account.Username, account.Status, timestamp(account.ExpiresAt), account.Note, timestamp(account.CreatedAt), timestamp(account.UpdatedAt), nullableTimestamp(account.DisabledAt))
	if err != nil {
		return domain.Account{}, err
	}
	account.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Account{}, err
	}
	account.Version = 1
	return account, nil
}

func (s *Store) SaveAccountPassword(ctx context.Context, accountID int64, ciphertext string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO account_credentials(account_id, password_ciphertext, updated_at) VALUES (?, ?, ?) ON CONFLICT(account_id) DO UPDATE SET password_ciphertext = excluded.password_ciphertext, updated_at = excluded.updated_at`, accountID, ciphertext, timestamp(now))
	return err
}
func (s *Store) AccountPasswordCiphertext(ctx context.Context, accountID int64) (string, error) {
	var ciphertext string
	err := s.db.QueryRowContext(ctx, `SELECT password_ciphertext FROM account_credentials WHERE account_id = ?`, accountID).Scan(&ciphertext)
	return ciphertext, err
}

// BeginAccountCreateOperation records an API account-create request before
// any remote Emby call. A matching idempotency key returns its original saga.
func (s *Store) FindAccount(ctx context.Context, id int64) (domain.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE id = ?`, id))
}

func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]domain.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// UpdateAccount atomically replaces mutable account fields only when account.Version
// still matches the database version. A successful write advances the version.
func (s *Store) UpdateAccount(ctx context.Context, account domain.Account) (domain.Account, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, note = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, timestamp(account.ExpiresAt), account.Note, timestamp(account.UpdatedAt), account.ID, account.Version)
	if err != nil {
		return domain.Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return domain.Account{}, err
	} else if changed == 0 {
		return domain.Account{}, domain.ErrAccountVersionConflict
	}
	account.Version++
	return account, nil
}

// UpdateAccountAndExpire atomically updates mutable fields, marks an active
// account expired, removes its sessions, and queues the Emby access policy.
func (s *Store) UpdateAccountAndExpire(ctx context.Context, account domain.Account, disabledAt, now time.Time) (domain.Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, note = ?, status = 'expired', disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND status = 'active'`, timestamp(account.ExpiresAt), account.Note, timestamp(disabledAt), timestamp(now), account.ID, account.Version)
	if err != nil {
		return domain.Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return domain.Account{}, err
	} else if changed == 0 {
		return domain.Account{}, domain.ErrAccountVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE account_id = ?`, account.ID); err != nil {
		return domain.Account{}, err
	}
	if err := upsertAccessSyncJob(ctx, tx, account.ID, true, now); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Account{}, err
	}
	account.Status = "expired"
	account.DisabledAt = &disabledAt
	account.UpdatedAt = now.UTC()
	account.Version++
	return account, nil
}

// SetAccountStatus atomically changes an account lifecycle state and queues
// its desired Emby policy. The supplied account is the compare-and-swap token.
func (s *Store) SetAccountStatus(ctx context.Context, account domain.Account, status string, disabledAt *time.Time, now time.Time) (domain.Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET status = ?, disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, status, nullableTimestamp(disabledAt), timestamp(now), account.ID, account.Version)
	if err != nil {
		return domain.Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return domain.Account{}, err
	} else if changed == 0 {
		return domain.Account{}, domain.ErrAccountVersionConflict
	}
	if status != "active" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE account_id = ?`, account.ID); err != nil {
			return domain.Account{}, err
		}
	}
	if err := upsertAccessSyncJob(ctx, tx, account.ID, status != "active", now); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Account{}, err
	}
	account.Status = status
	account.DisabledAt = disabledAt
	account.UpdatedAt = now.UTC()
	account.Version++
	return account, nil
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (domain.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE username = ?`, username))
}

func (s *Store) ListDueActiveAccounts(ctx context.Context, now time.Time) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE status = 'active' AND expires_at <= ? ORDER BY expires_at`, timestamp(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]domain.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Completed idempotency operations refer to their response account. An
	// explicit account deletion retires that replay record with the account.
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_create_operations WHERE account_id = ?`, id); err != nil {
		return err
	}
	// Redemptions are immutable history while an account exists, but must be
	// removed with an explicitly deleted business account to satisfy its FK.
	if _, err := tx.ExecContext(ctx, `DELETE FROM invite_redemptions WHERE account_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

type accountScanner interface{ Scan(...any) error }

func scanAccount(row accountScanner) (domain.Account, error) {
	var account domain.Account
	var expires, created, updated string
	var disabled sql.NullString
	err := row.Scan(&account.ID, &account.Version, &account.EmbyUserID, &account.Username, &account.Status, &expires, &account.Note, &created, &updated, &disabled)
	if err != nil {
		return domain.Account{}, err
	}
	var parseErr error
	if account.ExpiresAt, parseErr = parseTimestamp(expires); parseErr != nil {
		return domain.Account{}, parseErr
	}
	if account.CreatedAt, parseErr = parseTimestamp(created); parseErr != nil {
		return domain.Account{}, parseErr
	}
	if account.UpdatedAt, parseErr = parseTimestamp(updated); parseErr != nil {
		return domain.Account{}, parseErr
	}
	if disabled.Valid {
		parsed, err := parseTimestamp(disabled.String)
		if err != nil {
			return domain.Account{}, err
		}
		account.DisabledAt = &parsed
	}
	return account, nil
}
