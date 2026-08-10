package sqlite

import (
	"errors"
	"time"
)

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

// PaymentPlan is an administrator-configured sale option. Prices are integer
// CNY fen and duration is kept in minutes for exact subscription arithmetic.
type PaymentPlan struct {
	ID              int64
	Kind            string
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	Enabled         bool
	SortOrder       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PaymentOrder is the local source of truth for a payment-center checkout and
// its fulfillment. Plan fields are copied as a snapshot at order creation.
type PaymentOrder struct {
	ID                 int64
	PublicToken        string
	MerchantOrderNo    string
	Kind               string
	PlanID             int64
	PlanName           string
	AccountID          *int64
	AccountUsername    string
	DurationDays       int
	DurationMinutes    int
	AmountFen          int
	Currency           string
	PaymentStatus      string
	FulfillmentStatus  string
	ProviderStatus     string
	PaymentURL         string
	PaymentMemo        string
	ProviderPaymentKey string
	InviteID           *int64
	ActivationCode     string
	FailureReason      string
	ExpiresAt          time.Time
	PaidAt             *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreatePaymentPlanInput struct {
	Kind            string
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	SortOrder       int
}

type UpdatePaymentPlanInput struct {
	Name            string
	DurationDays    int
	DurationMinutes int
	PriceFen        int
	Note            string
	SortOrder       int
}

type CreatePaymentOrderInput struct {
	PublicToken     string
	MerchantOrderNo string
	Kind            string
	PlanID          int64
	PlanName        string
	AccountID       *int64
	AccountUsername string
	DurationDays    int
	DurationMinutes int
	AmountFen       int
	Currency        string
	ExpiresAt       time.Time
	Now             time.Time
}

type UpdatePaymentProviderInput struct {
	OrderID        int64
	ProviderStatus string
	PaymentURL     string
	PaymentMemo    string
	ExpiresAt      time.Time
	Now            time.Time
}

type FulfillPaymentOrderInput struct {
	OrderID              int64
	EventID              string
	EventType            string
	AmountFen            int
	Currency             string
	ProviderPaymentKey   string
	PayloadHash          string
	PaidAt               time.Time
	Now                  time.Time
	ActivationCode       string
	ActivationCodeHash   string
	ActivationCodePrefix string
}
