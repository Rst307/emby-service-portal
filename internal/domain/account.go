// Package domain owns the business model shared by every application module.
// It contains no persistence, transport, or presentation details: services
// express business rules on these types, and persistence adapters (such as
// internal/persistence/sqlite) implement their storage.
package domain

import (
	"errors"
	"time"
)

// Account is the business record that owns an Emby 用户 association, its
// access state, expiry time, and administrative metadata. One business
// account corresponds to one Emby 用户.
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

// AccessSyncJob records the desired Emby login policy for a business account.
// Revision protects a newer state change from an in-flight older worker.
type AccessSyncJob struct {
	Account         Account
	DesiredDisabled bool
	Revision        int64
	Attempts        int
	LastError       string
}

var (
	ErrAccountVersionConflict = errors.New("account version conflict")
	ErrAccountAlreadyExists   = errors.New("account username already exists")
)
