package domain

import "time"

// Admin is the bootstrap-created administrator identity for the management
// backend. Its password is stored as a bcrypt hash.
type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session is a server-side administrator session. Only the token hash is
// persisted; the raw token is returned to the browser once.
type Session struct {
	ID         string
	AdminID    int64
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// UserSession is a server-side portal session that identifies one 业务账号.
// It lets renewals use the authenticated account identity instead of trusting
// a username or password submitted by the browser.
type UserSession struct {
	ID        string
	AccountID int64
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}
