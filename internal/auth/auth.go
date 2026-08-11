// Package auth owns administrator credential and session operations.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Service struct {
	store *sqlite.Store
	clock Clock
	ttl   time.Duration
}

func New(store *sqlite.Store, ttl time.Duration) *Service {
	return &Service{store: store, clock: realClock{}, ttl: ttl}
}

func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) error {
	exists, err := s.store.HasAdmins(ctx)
	if err != nil {
		return fmt.Errorf("check bootstrap administrator: %w", err)
	}
	if exists {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap administrator password: %w", err)
	}
	if _, err := s.store.CreateAdmin(ctx, strings.TrimSpace(username), string(hash), s.clock.Now()); err != nil {
		return fmt.Errorf("create bootstrap administrator: %w", err)
	}
	return nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	admin, err := s.store.FindAdminByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("find administrator: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) != nil {
		return "", ErrInvalidCredentials
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	now := s.clock.Now().UTC()
	session := domain.Session{ID: tokenID(token), AdminID: admin.ID, TokenHash: hashToken(token), CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.ttl)}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return "", fmt.Errorf("create administrator session: %w", err)
	}
	return token, nil
}

func (s *Service) Authenticated(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	session, err := s.store.FindSessionByTokenHash(ctx, hashToken(token))
	return err == nil && session.ExpiresAt.After(s.clock.Now())
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.store.DeleteSessionByTokenHash(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("delete administrator session: %w", err)
	}
	return nil
}

func newToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func tokenID(token string) string { return hashToken(token)[:22] }
