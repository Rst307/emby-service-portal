// Package portal authenticates Emby users for the self-service portal without storing their passwords.
package portal

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
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Service struct {
	store *sqlite.Store
	emby  emby.Authenticator
	ttl   time.Duration
}

func New(store *sqlite.Store, embyClient emby.Authenticator, ttl time.Duration) *Service {
	return &Service{store: store, emby: embyClient, ttl: ttl}
}
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	account, err := s.store.FindAccountByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(password) == "" {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("find account: %w", err)
	}
	if account.Status != "active" || !account.ExpiresAt.After(time.Now()) {
		return "", ErrInvalidCredentials
	}
	user, err := s.emby.AuthenticateUser(ctx, account.Username, password)
	if err != nil || user.ID != account.EmbyUserID {
		return "", ErrInvalidCredentials
	}
	token, err := token()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if err := s.store.CreateUserSession(ctx, domain.UserSession{ID: hash(token)[:22], AccountID: account.ID, TokenHash: hash(token), CreatedAt: now, ExpiresAt: now.Add(s.ttl)}); err != nil {
		return "", fmt.Errorf("create portal session: %w", err)
	}
	return token, nil
}
func (s *Service) Account(ctx context.Context, tokenValue string) (domain.Account, bool) {
	if tokenValue == "" {
		return domain.Account{}, false
	}
	session, err := s.store.FindUserSessionByTokenHash(ctx, hash(tokenValue))
	if err != nil || !session.ExpiresAt.After(time.Now()) {
		return domain.Account{}, false
	}
	account, err := s.store.FindAccount(ctx, session.AccountID)
	if err != nil || account.Status != "active" || !account.ExpiresAt.After(time.Now()) {
		return domain.Account{}, false
	}
	return account, true
}
func (s *Service) Logout(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return nil
	}
	return s.store.DeleteUserSessionByTokenHash(ctx, hash(tokenValue))
}
func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
