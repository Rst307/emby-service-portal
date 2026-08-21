// Package sqlite provides the application's SQLite persistence adapter.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ db *sql.DB }

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
		{"0011_settings", "migrations/0011_settings.sql"},
		{"0012_payment_catalog", "migrations/0012_payment_catalog.sql"},
		{"0013_payment_order_buyer", "migrations/0013_payment_order_buyer.sql"},
		{"0014_media_requests", "migrations/0014_media_requests.sql"},
		{"0015_media_request_kind", "migrations/0015_media_request_kind.sql"},
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
func nullableTimestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timestamp(*t)
}

func timestamp(t time.Time) string               { return t.UTC().Format(time.RFC3339Nano) }
func parseTimestamp(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }
