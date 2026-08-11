package domain

import (
	"errors"
	"time"
)

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

var ErrIdempotencyKeyConflict = errors.New("idempotency key was already used with a different request")
