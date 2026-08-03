// Package invites manages invite-code issuance and redemption.
package invites

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

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

var (
	ErrInvalidDuration = errors.New("duration must be between 1 minute and 3650 days")
	ErrInvalidMaxUses  = errors.New("maximum uses cannot be negative")
	ErrUnavailable     = errors.New("invite code is unavailable")
)

type Service struct {
	store    *sqlite.Store
	accounts *accounts.Service
	now      func() time.Time
}
type CreateInput struct {
	DurationDays, DurationMinutes, MaxUses int
	StartsAt, ExpiresAt                    *time.Time
	Note                                   string
}
type CreatedCode struct {
	Invite sqlite.InviteCode
	Code   string
}

func New(store *sqlite.Store, accountService *accounts.Service) *Service {
	return &Service{store: store, accounts: accountService, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (CreatedCode, error) {
	if input.DurationMinutes == 0 {
		input.DurationMinutes = input.DurationDays * 24 * 60
	}
	if input.DurationMinutes < 1 || input.DurationMinutes > 3650*24*60 {
		return CreatedCode{}, ErrInvalidDuration
	}
	input.DurationDays = (input.DurationMinutes + 1439) / 1440
	if input.MaxUses < 0 {
		return CreatedCode{}, ErrInvalidMaxUses
	}
	if input.StartsAt != nil && input.ExpiresAt != nil && !input.ExpiresAt.After(*input.StartsAt) {
		return CreatedCode{}, fmt.Errorf("invite expiry must be after its start")
	}
	code, err := newCode()
	if err != nil {
		return CreatedCode{}, err
	}
	now := s.now().UTC()
	invite, err := s.store.CreateInvite(ctx, sqlite.InviteCode{CodeHash: hash(code), Code: code, CodePrefix: code[:8], DurationDays: input.DurationDays, DurationMinutes: input.DurationMinutes, MaxUses: input.MaxUses, StartsAt: utc(input.StartsAt), ExpiresAt: utc(input.ExpiresAt), Enabled: true, Note: strings.TrimSpace(input.Note), CreatedAt: now})
	if err != nil {
		return CreatedCode{}, err
	}
	return CreatedCode{Invite: invite, Code: code}, nil
}
func (s *Service) List(ctx context.Context) ([]sqlite.InviteCode, error) {
	return s.store.ListInvites(ctx)
}
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.store.SetInviteEnabled(ctx, id, enabled)
}
func (s *Service) Delete(ctx context.Context, id int64) error { return s.store.DeleteInvite(ctx, id) }

func (s *Service) Register(ctx context.Context, code, username, password string) (sqlite.Account, error) {
	now := s.now().UTC()
	invite, err := s.consume(ctx, code, now)
	if err != nil {
		return sqlite.Account{}, err
	}
	account, err := s.accounts.Create(ctx, accounts.CreateInput{Username: username, Password: password, ExpiresAt: now.Add(time.Duration(invite.DurationMinutes) * time.Minute)})
	if err != nil {
		_ = s.store.ReleaseInvite(ctx, invite.ID)
		return sqlite.Account{}, err
	}
	if err := s.store.RecordRedemption(ctx, invite.ID, account.ID, "register", invite.DurationDays, invite.DurationMinutes, now); err != nil {
		return sqlite.Account{}, err
	}
	return account, nil
}

// RegisterIdempotent records the invite reservation and account provisioning
// under one idempotency key. Replaying the API request resumes that saga even
// after the invite would otherwise be exhausted or expired.
func (s *Service) RegisterIdempotent(ctx context.Context, idempotencyKey, code, username, password string) (sqlite.Account, error) {
	account, err := s.accounts.RegisterIdempotent(ctx, idempotencyKey, hash(strings.TrimSpace(code)), username, password)
	if errors.Is(err, sqlite.ErrInviteNotRedeemable) {
		return sqlite.Account{}, ErrUnavailable
	}
	return account, err
}

func (s *Service) Renew(ctx context.Context, code, username, password string) (sqlite.Account, error) {
	if err := s.accounts.VerifyPassword(ctx, username, password); err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			return sqlite.Account{}, accounts.ErrNotFound
		}
		return sqlite.Account{}, ErrUnavailable
	}
	result, err := s.store.RedeemRenewal(ctx, sqlite.RedeemRenewalInput{
		CodeHash:   hash(strings.TrimSpace(code)),
		Username:   strings.TrimSpace(username),
		RedeemedAt: s.now().UTC(),
	})
	switch {
	case errors.Is(err, sqlite.ErrInviteNotRedeemable):
		return sqlite.Account{}, ErrUnavailable
	case errors.Is(err, sql.ErrNoRows):
		return sqlite.Account{}, accounts.ErrNotFound
	case err != nil:
		return sqlite.Account{}, fmt.Errorf("redeem renewal: %w", err)
	}
	// RedeemRenewal atomically queues any required access restoration. The
	// expiry runner owns all Emby policy calls, so a committed renewal never
	// performs a remote side effect before its durable local state is visible.
	return result.Account, nil
}
func (s *Service) consume(ctx context.Context, code string, now time.Time) (sqlite.InviteCode, error) {
	invite, err := s.store.ConsumeInvite(ctx, hash(strings.TrimSpace(code)), now)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return sqlite.InviteCode{}, ErrUnavailable
	}
	return invite, nil
}
func newCode() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "EUM-" + base64.RawURLEncoding.EncodeToString(bytes), nil
}
func hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func utc(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}
