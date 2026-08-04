// Package sqlite provides the application's SQLite persistence adapter.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ db *sql.DB }

type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID         string
	AdminID    int64
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type UserSession struct {
	ID        string
	AccountID int64
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Account struct {
	ID         int64
	Version    int64
	EmbyUserID string
	Username   string
	Status     string
	ExpiresAt  time.Time
	Note       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DisabledAt *time.Time
}

type InviteCode struct {
	ID              int64
	CodeHash        string
	Code            string
	CodePrefix      string
	DurationDays    int // retained for backwards-compatible API responses
	DurationMinutes int
	MaxUses         int
	UsedCount       int
	StartsAt        *time.Time
	ExpiresAt       *time.Time
	Enabled         bool
	Note            string
	CreatedAt       time.Time
}

var (
	ErrInviteNotRedeemable    = errors.New("invite is not redeemable")
	ErrAccountVersionConflict = errors.New("account version conflict")
	ErrIdempotencyKeyConflict = errors.New("idempotency key was already used with a different request")
	ErrAccountAlreadyExists   = errors.New("account username already exists")
)

// RedeemRenewalInput identifies one renewal redemption. RedeemedAt is the
// authoritative clock instant used for both invite eligibility and status.
type RedeemRenewalInput struct {
	CodeHash   string
	Username   string
	RedeemedAt time.Time
}

// RenewalRedemption is the complete local result of a successful renewal.
// Its account, invite usage, and immutable redemption record are committed
// together, so callers never need to compose those writes themselves.
type RenewalRedemption struct {
	Account     Account
	Invite      InviteCode
	Reactivated bool
}

// AccessSyncJob records the desired Emby login policy for a business account.
// Revision protects a newer state change from an in-flight older worker.
type AccessSyncJob struct {
	Account         Account
	DesiredDisabled bool
	Revision        int64
	Attempts        int
	LastError       string
}

// AccountCreateOperation is the durable state of an account-provisioning
// saga. Its password is always encrypted before it reaches this record.
type AccountCreateOperation struct {
	ID                 int64
	Kind               string
	IdempotencyKey     string
	RequestFingerprint string
	Username           string
	PasswordCiphertext string
	ExpiresAt          time.Time
	Note               string
	InviteCodeHash     string
	InviteCodeID       *int64
	InviteDurationDays int
	InviteDurationMins int
	EmbyUserID         string
	AccountID          *int64
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

type BeginAccountCreateOperationInput struct {
	Kind               string
	IdempotencyKey     string
	RequestFingerprint string
	Username           string
	PasswordCiphertext string
	ExpiresAt          time.Time
	Note               string
	Now                time.Time
}

type BeginRegistrationOperationInput struct {
	BeginAccountCreateOperationInput
	InviteCodeHash string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect SQLite: %w", err)
	}
	// The DSN configures these pragmas for every connection. Execute and verify
	// them once at startup as an early failure signal for unsupported drivers.
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure SQLite: %w", err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	migrations := []struct {
		version string
		file    string
	}{
		{"0001_initial", "migrations/0001_initial.sql"},
		{"0002_accounts", "migrations/0002_accounts.sql"},
		{"0003_invites", "migrations/0003_invites.sql"},
		{"0004_user_sessions", "migrations/0004_user_sessions.sql"},
		{"0005_invite_duration_minutes", "migrations/0005_invite_duration_minutes.sql"},
		{"0006_invite_code_copy", "migrations/0006_invite_code_copy.sql"},
		{"0007_account_credentials", "migrations/0007_account_credentials.sql"},
		{"0008_emby_access_sync_jobs", "migrations/0008_emby_access_sync_jobs.sql"},
		{"0009_account_version", "migrations/0009_account_version.sql"},
		{"0010_account_create_operations", "migrations/0010_account_create_operations.sql"},
	}
	for _, migration := range migrations {
		if err := s.applyMigration(ctx, migration.version, migration.file); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version, file string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&exists); err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if exists != 0 {
		var applied int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
			return fmt.Errorf("read migrations: %w", err)
		}
		if applied != 0 {
			return nil
		}
	}
	sqlText, err := migrationFiles.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", version, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(sqlText)); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version, timestamp(time.Now())); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}

func (s *Store) HasAdmins(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admins").Scan(&count)
	return count > 0, err
}

func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string, now time.Time) (Admin, error) {
	result, err := s.db.ExecContext(ctx, "INSERT INTO admins(username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)", username, passwordHash, timestamp(now), timestamp(now))
	if err != nil {
		return Admin{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Admin{}, err
	}
	return Admin{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}

func (s *Store) FindAdminByUsername(ctx context.Context, username string) (Admin, error) {
	var admin Admin
	var created, updated string
	err := s.db.QueryRowContext(ctx, "SELECT id, username, password_hash, created_at, updated_at FROM admins WHERE username = ?", username).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &created, &updated)
	if err != nil {
		return Admin{}, err
	}
	admin.CreatedAt, err = parseTimestamp(created)
	if err != nil {
		return Admin{}, err
	}
	admin.UpdatedAt, err = parseTimestamp(updated)
	if err != nil {
		return Admin{}, err
	}
	return admin, nil
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO admin_sessions(id, admin_id, token_hash, expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)", session.ID, session.AdminID, session.TokenHash, timestamp(session.ExpiresAt), timestamp(session.CreatedAt), timestamp(session.LastSeenAt))
	return err
}

func (s *Store) FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	var session Session
	var expires, created, seen string
	err := s.db.QueryRowContext(ctx, "SELECT id, admin_id, token_hash, expires_at, created_at, last_seen_at FROM admin_sessions WHERE token_hash = ?", tokenHash).Scan(&session.ID, &session.AdminID, &session.TokenHash, &expires, &created, &seen)
	if err != nil {
		return Session{}, err
	}
	var parseErr error
	if session.ExpiresAt, parseErr = parseTimestamp(expires); parseErr != nil {
		return Session{}, parseErr
	}
	if session.CreatedAt, parseErr = parseTimestamp(created); parseErr != nil {
		return Session{}, parseErr
	}
	if session.LastSeenAt, parseErr = parseTimestamp(seen); parseErr != nil {
		return Session{}, parseErr
	}
	return session, nil
}

func (s *Store) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash)
	return err
}

func (s *Store) CreateUserSession(ctx context.Context, session UserSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_sessions(id, account_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`, session.ID, session.AccountID, session.TokenHash, timestamp(session.ExpiresAt), timestamp(session.CreatedAt))
	return err
}

func (s *Store) FindUserSessionByTokenHash(ctx context.Context, tokenHash string) (UserSession, error) {
	var session UserSession
	var expiresAt, createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, account_id, token_hash, expires_at, created_at FROM user_sessions WHERE token_hash = ?`, tokenHash).Scan(&session.ID, &session.AccountID, &session.TokenHash, &expiresAt, &createdAt)
	if err != nil {
		return UserSession{}, err
	}
	var parseErr error
	if session.ExpiresAt, parseErr = parseTimestamp(expiresAt); parseErr != nil {
		return UserSession{}, parseErr
	}
	if session.CreatedAt, parseErr = parseTimestamp(createdAt); parseErr != nil {
		return UserSession{}, parseErr
	}
	return session, nil
}

func (s *Store) DeleteUserSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_sessions WHERE token_hash = ?", tokenHash)
	return err
}

func (s *Store) CreateAccount(ctx context.Context, account Account) (Account, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO accounts(emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, account.EmbyUserID, account.Username, account.Status, timestamp(account.ExpiresAt), account.Note, timestamp(account.CreatedAt), timestamp(account.UpdatedAt), nullableTimestamp(account.DisabledAt))
	if err != nil {
		return Account{}, err
	}
	account.ID, err = result.LastInsertId()
	if err != nil {
		return Account{}, err
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
func (s *Store) BeginAccountCreateOperation(ctx context.Context, input BeginAccountCreateOperationInput) (AccountCreateOperation, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return AccountCreateOperation{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return AccountCreateOperation{}, err
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
			return AccountCreateOperation{}, ErrIdempotencyKeyConflict
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return AccountCreateOperation{}, err
		}
		committed = true
		return operation, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AccountCreateOperation{}, err
	}
	var existingAccountID int64
	if err := conn.QueryRowContext(ctx, `SELECT id FROM accounts WHERE username = ?`, input.Username).Scan(&existingAccountID); err == nil {
		return AccountCreateOperation{}, ErrAccountAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AccountCreateOperation{}, err
	}

	now := input.Now.UTC()
	result, err := conn.ExecContext(ctx, `INSERT INTO account_create_operations(kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, input.Kind, input.IdempotencyKey, input.RequestFingerprint, input.Username, input.PasswordCiphertext, timestamp(input.ExpiresAt), input.Note, timestamp(now), timestamp(now))
	if err != nil {
		return AccountCreateOperation{}, err
	}
	operation.ID, err = result.LastInsertId()
	if err != nil {
		return AccountCreateOperation{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return AccountCreateOperation{}, err
	}
	committed = true
	return AccountCreateOperation{ID: operation.ID, Kind: input.Kind, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: input.RequestFingerprint, Username: input.Username, PasswordCiphertext: input.PasswordCiphertext, ExpiresAt: input.ExpiresAt.UTC(), Note: input.Note, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
}

// BeginRegistrationOperation atomically reserves an invite use and records
// the full registration saga. Replays find the operation before examining the
// invite, so an expired, disabled, or exhausted invite can still be resumed.
func (s *Store) BeginRegistrationOperation(ctx context.Context, input BeginRegistrationOperationInput) (AccountCreateOperation, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return AccountCreateOperation{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return AccountCreateOperation{}, err
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
			return AccountCreateOperation{}, ErrIdempotencyKeyConflict
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return AccountCreateOperation{}, err
		}
		committed = true
		return operation, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AccountCreateOperation{}, err
	}
	var existingAccountID int64
	if err := conn.QueryRowContext(ctx, `SELECT id FROM accounts WHERE username = ?`, input.Username).Scan(&existingAccountID); err == nil {
		return AccountCreateOperation{}, ErrAccountAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AccountCreateOperation{}, err
	}

	invite, err := scanInvite(conn.QueryRowContext(ctx, `SELECT id, code_hash, code, code_prefix, duration_days, duration_minutes, max_uses, used_count, starts_at, expires_at, enabled, note, created_at FROM invite_codes WHERE code_hash = ?`, input.InviteCodeHash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountCreateOperation{}, ErrInviteNotRedeemable
		}
		return AccountCreateOperation{}, err
	}
	now := input.Now.UTC()
	if !invite.Enabled || invite.DurationMinutes < 1 ||
		(invite.StartsAt != nil && now.Before(*invite.StartsAt)) ||
		(invite.ExpiresAt != nil && !now.Before(*invite.ExpiresAt)) ||
		(invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses) {
		return AccountCreateOperation{}, ErrInviteNotRedeemable
	}
	updatedInvite, err := conn.ExecContext(ctx, `UPDATE invite_codes SET used_count = used_count + 1 WHERE id = ? AND used_count = ?`, invite.ID, invite.UsedCount)
	if err != nil {
		return AccountCreateOperation{}, err
	}
	changed, err := updatedInvite.RowsAffected()
	if err != nil {
		return AccountCreateOperation{}, err
	}
	if changed != 1 {
		return AccountCreateOperation{}, ErrInviteNotRedeemable
	}
	invite.UsedCount++
	expiresAt := now.Add(time.Duration(invite.DurationMinutes) * time.Minute)
	result, err := conn.ExecContext(ctx, `INSERT INTO account_create_operations(kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, invite_code_hash, invite_code_id, invite_duration_days, invite_duration_minutes, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, input.Kind, input.IdempotencyKey, input.RequestFingerprint, input.Username, input.PasswordCiphertext, timestamp(expiresAt), input.Note, input.InviteCodeHash, invite.ID, invite.DurationDays, invite.DurationMinutes, timestamp(now), timestamp(now))
	if err != nil {
		return AccountCreateOperation{}, err
	}
	operation.ID, err = result.LastInsertId()
	if err != nil {
		return AccountCreateOperation{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return AccountCreateOperation{}, err
	}
	committed = true
	inviteID := invite.ID
	return AccountCreateOperation{ID: operation.ID, Kind: input.Kind, IdempotencyKey: input.IdempotencyKey, RequestFingerprint: input.RequestFingerprint, Username: input.Username, PasswordCiphertext: input.PasswordCiphertext, ExpiresAt: expiresAt, Note: input.Note, InviteCodeHash: input.InviteCodeHash, InviteCodeID: &inviteID, InviteDurationDays: invite.DurationDays, InviteDurationMins: invite.DurationMinutes, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
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

func (s *Store) FindAccountCreateOperation(ctx context.Context, id int64) (AccountCreateOperation, error) {
	return scanAccountCreateOperation(s.db.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE id = ?`, id))
}

func (s *Store) ListIncompleteAccountCreateOperations(ctx context.Context, limit int) ([]AccountCreateOperation, error) {
	if limit < 1 {
		return []AccountCreateOperation{}, nil
	}
	rows, err := s.db.QueryContext(ctx, accountCreateOperationSelect+` WHERE status <> 'completed' ORDER BY updated_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := make([]AccountCreateOperation, 0)
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
func (s *Store) CompleteAccountCreateOperation(ctx context.Context, operationID int64, now time.Time) (Account, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Account{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Account{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	operation, err := scanAccountCreateOperation(conn.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE id = ?`, operationID))
	if err != nil {
		return Account{}, err
	}
	if operation.Status == "completed" {
		account, err := scanAccount(conn.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE id = ?`, operation.AccountID))
		if err != nil {
			return Account{}, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Account{}, err
		}
		committed = true
		return account, nil
	}
	if operation.EmbyUserID == "" {
		return Account{}, fmt.Errorf("account create operation has no Emby user")
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
		return Account{}, err
	}
	accountID, err := result.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO account_credentials(account_id, password_ciphertext, updated_at) VALUES (?, ?, ?)`, accountID, operation.PasswordCiphertext, timestamp(finalizedAt)); err != nil {
		return Account{}, err
	}
	if operation.Kind == "register" {
		if operation.InviteCodeID == nil {
			return Account{}, fmt.Errorf("registration operation has no invite")
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO invite_redemptions(invite_code_id, account_id, kind, duration_days, duration_minutes, redeemed_at) VALUES (?, ?, 'register', ?, ?, ?)`, *operation.InviteCodeID, accountID, operation.InviteDurationDays, operation.InviteDurationMins, timestamp(finalizedAt)); err != nil {
			return Account{}, err
		}
	}
	if status != "active" {
		if err := upsertAccessSyncJob(ctx, conn, accountID, true, finalizedAt); err != nil {
			return Account{}, err
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE account_create_operations SET account_id = ?, status = 'completed', updated_at = ?, completed_at = ? WHERE id = ?`, accountID, timestamp(finalizedAt), timestamp(finalizedAt), operationID); err != nil {
		return Account{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Account{}, err
	}
	committed = true
	return Account{ID: accountID, Version: 1, EmbyUserID: operation.EmbyUserID, Username: operation.Username, Status: status, ExpiresAt: operation.ExpiresAt, Note: operation.Note, CreatedAt: finalizedAt, UpdatedAt: finalizedAt, DisabledAt: disabledAt}, nil
}

const accountCreateOperationSelect = `SELECT id, kind, idempotency_key, request_fingerprint, username, password_ciphertext, expires_at, note, invite_code_hash, invite_code_id, invite_duration_days, invite_duration_minutes, emby_user_id, account_id, status, created_at, updated_at, completed_at FROM account_create_operations`

type accountCreateOperationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findAccountCreateOperation(ctx context.Context, queryer accountCreateOperationQueryer, kind, idempotencyKey string) (AccountCreateOperation, error) {
	return scanAccountCreateOperation(queryer.QueryRowContext(ctx, accountCreateOperationSelect+` WHERE kind = ? AND idempotency_key = ?`, kind, idempotencyKey))
}

type accountCreateOperationScanner interface{ Scan(...any) error }

func scanAccountCreateOperation(row accountCreateOperationScanner) (AccountCreateOperation, error) {
	var operation AccountCreateOperation
	var expiresAt, createdAt, updatedAt string
	var inviteID, accountID sql.NullInt64
	var inviteCodeHash, embyUserID, completedAt sql.NullString
	if err := row.Scan(&operation.ID, &operation.Kind, &operation.IdempotencyKey, &operation.RequestFingerprint, &operation.Username, &operation.PasswordCiphertext, &expiresAt, &operation.Note, &inviteCodeHash, &inviteID, &operation.InviteDurationDays, &operation.InviteDurationMins, &embyUserID, &accountID, &operation.Status, &createdAt, &updatedAt, &completedAt); err != nil {
		return AccountCreateOperation{}, err
	}
	var err error
	if operation.ExpiresAt, err = parseTimestamp(expiresAt); err != nil {
		return AccountCreateOperation{}, err
	}
	if operation.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return AccountCreateOperation{}, err
	}
	if operation.UpdatedAt, err = parseTimestamp(updatedAt); err != nil {
		return AccountCreateOperation{}, err
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
			return AccountCreateOperation{}, err
		}
		operation.CompletedAt = &value
	}
	return operation, nil
}

func (s *Store) FindAccount(ctx context.Context, id int64) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE id = ?`, id))
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]Account, 0)
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
func (s *Store) UpdateAccount(ctx context.Context, account Account) (Account, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, note = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, timestamp(account.ExpiresAt), account.Note, timestamp(account.UpdatedAt), account.ID, account.Version)
	if err != nil {
		return Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return Account{}, err
	} else if changed == 0 {
		return Account{}, ErrAccountVersionConflict
	}
	account.Version++
	return account, nil
}

// UpdateAccountAndExpire atomically updates mutable fields, marks an active
// account expired, removes its sessions, and queues the Emby access policy.
func (s *Store) UpdateAccountAndExpire(ctx context.Context, account Account, disabledAt, now time.Time) (Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET expires_at = ?, note = ?, status = 'expired', disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND status = 'active'`, timestamp(account.ExpiresAt), account.Note, timestamp(disabledAt), timestamp(now), account.ID, account.Version)
	if err != nil {
		return Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return Account{}, err
	} else if changed == 0 {
		return Account{}, ErrAccountVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE account_id = ?`, account.ID); err != nil {
		return Account{}, err
	}
	if err := upsertAccessSyncJob(ctx, tx, account.ID, true, now); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	account.Status = "expired"
	account.DisabledAt = &disabledAt
	account.UpdatedAt = now.UTC()
	account.Version++
	return account, nil
}

// SetAccountStatus atomically changes an account lifecycle state and queues
// its desired Emby policy. The supplied account is the compare-and-swap token.
func (s *Store) SetAccountStatus(ctx context.Context, account Account, status string, disabledAt *time.Time, now time.Time) (Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET status = ?, disabled_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, status, nullableTimestamp(disabledAt), timestamp(now), account.ID, account.Version)
	if err != nil {
		return Account{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return Account{}, err
	} else if changed == 0 {
		return Account{}, ErrAccountVersionConflict
	}
	if status != "active" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE account_id = ?`, account.ID); err != nil {
			return Account{}, err
		}
	}
	if err := upsertAccessSyncJob(ctx, tx, account.ID, status != "active", now); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	account.Status = status
	account.DisabledAt = disabledAt
	account.UpdatedAt = now.UTC()
	account.Version++
	return account, nil
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE username = ?`, username))
}

func (s *Store) ListDueActiveAccounts(ctx context.Context, now time.Time) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, emby_user_id, username, status, expires_at, note, created_at, updated_at, disabled_at FROM accounts WHERE status = 'active' AND expires_at <= ? ORDER BY expires_at`, timestamp(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]Account, 0)
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

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertAccessSyncJob(ctx context.Context, executor sqlExecutor, accountID int64, desiredDisabled bool, now time.Time) error {
	desired := 0
	if desiredDisabled {
		desired = 1
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO emby_access_sync_jobs(account_id, desired_disabled, revision, attempts, last_error, created_at, updated_at) VALUES (?, ?, 1, 0, '', ?, ?) ON CONFLICT(account_id) DO UPDATE SET desired_disabled = excluded.desired_disabled, revision = emby_access_sync_jobs.revision + 1, attempts = 0, last_error = '', updated_at = excluded.updated_at`, accountID, desired, timestamp(now), timestamp(now))
	return err
}

// ListAccessSyncJobs returns a bounded batch of pending Emby policy updates.
func (s *Store) ListAccessSyncJobs(ctx context.Context, limit int) ([]AccessSyncJob, error) {
	if limit < 1 {
		return []AccessSyncJob{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.version, a.emby_user_id, a.username, a.status, a.expires_at, a.note, a.created_at, a.updated_at, a.disabled_at, j.desired_disabled, j.revision, j.attempts, j.last_error FROM emby_access_sync_jobs j JOIN accounts a ON a.id = j.account_id ORDER BY j.updated_at, j.account_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]AccessSyncJob, 0)
	for rows.Next() {
		job, err := scanAccessSyncJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) CompleteAccessSync(ctx context.Context, accountID, revision int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM emby_access_sync_jobs WHERE account_id = ? AND revision = ?`, accountID, revision)
	return err
}

func (s *Store) FailAccessSync(ctx context.Context, accountID, revision int64, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE emby_access_sync_jobs SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE account_id = ? AND revision = ?`, message, timestamp(now), accountID, revision)
	return err
}

type accessSyncJobScanner interface{ Scan(...any) error }

func scanAccessSyncJob(row accessSyncJobScanner) (AccessSyncJob, error) {
	var job AccessSyncJob
	var expires, created, updated string
	var disabled sql.NullString
	var desired int
	if err := row.Scan(&job.Account.ID, &job.Account.Version, &job.Account.EmbyUserID, &job.Account.Username, &job.Account.Status, &expires, &job.Account.Note, &created, &updated, &disabled, &desired, &job.Revision, &job.Attempts, &job.LastError); err != nil {
		return AccessSyncJob{}, err
	}
	var err error
	if job.Account.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return AccessSyncJob{}, err
	}
	if job.Account.CreatedAt, err = parseTimestamp(created); err != nil {
		return AccessSyncJob{}, err
	}
	if job.Account.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return AccessSyncJob{}, err
	}
	if disabled.Valid {
		parsed, err := parseTimestamp(disabled.String)
		if err != nil {
			return AccessSyncJob{}, err
		}
		job.Account.DisabledAt = &parsed
	}
	job.DesiredDisabled = desired != 0
	return job, nil
}

type accountScanner interface{ Scan(...any) error }

func scanAccount(row accountScanner) (Account, error) {
	var account Account
	var expires, created, updated string
	var disabled sql.NullString
	err := row.Scan(&account.ID, &account.Version, &account.EmbyUserID, &account.Username, &account.Status, &expires, &account.Note, &created, &updated, &disabled)
	if err != nil {
		return Account{}, err
	}
	var parseErr error
	if account.ExpiresAt, parseErr = parseTimestamp(expires); parseErr != nil {
		return Account{}, parseErr
	}
	if account.CreatedAt, parseErr = parseTimestamp(created); parseErr != nil {
		return Account{}, parseErr
	}
	if account.UpdatedAt, parseErr = parseTimestamp(updated); parseErr != nil {
		return Account{}, parseErr
	}
	if disabled.Valid {
		parsed, err := parseTimestamp(disabled.String)
		if err != nil {
			return Account{}, err
		}
		account.DisabledAt = &parsed
	}
	return account, nil
}
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

func nullableTimestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timestamp(*t)
}

func timestamp(t time.Time) string               { return t.UTC().Format(time.RFC3339Nano) }
func parseTimestamp(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }
