package domain

import (
	"errors"
	"time"
)

// InviteCode is a managed redeemable credential with a duration and usage
// constraints, usable for registration or renewal. It is not a payment
// instrument.
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
	Redemptions     []InviteRedemption
}

// InviteRedemption is a successful, recorded use of an invite code to
// register a business account or extend its subscription period.
type InviteRedemption struct {
	ID              int64
	InviteCodeID    int64
	AccountID       int64
	AccountUsername string
	Kind            string
	DurationDays    int
	DurationMinutes int
	RedeemedAt      time.Time
}

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

var ErrInviteNotRedeemable = errors.New("invite is not redeemable")
