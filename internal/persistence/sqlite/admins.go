package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

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

// Setting returns the stored value for key. ok is false when the key is absent.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting inserts or replaces the value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// SetSettings atomically updates a group of runtime settings.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
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
