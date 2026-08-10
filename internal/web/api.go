package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/accounts"
	"github.com/emby-user-manager/emby-user-manager/internal/invites"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

func (s *Server) apiAuthorized(r *http.Request) bool {
	key := r.Header.Get("X-API-Key")
	return key != "" && len(key) == len(s.apiKey) && subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) == 1
}
func (s *Server) requireAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if s.apiAuthorized(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
	return false
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key is required"})
		return "", false
	}
	return key, true
}
func (s *Server) apiAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	accounts, err := s.accounts.List(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "load accounts"})
		return
	}
	writeJSON(w, http.StatusOK, accountJSONList(accounts))
}
func (s *Server) apiCreateAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var input struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		ExpiresAt string `json:"expires_at"`
		Note      string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err == nil {
		account, createErr := s.accounts.CreateIdempotent(r.Context(), idempotencyKey, accounts.CreateInput{Username: input.Username, Password: input.Password, ExpiresAt: expiresAt, Note: input.Note})
		err = createErr
		if err == nil {
			writeJSON(w, http.StatusCreated, accountJSONFrom(account))
			return
		}
	}
	status := http.StatusBadRequest
	if errors.Is(err, accounts.ErrIdempotencyKeyConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": accountError(err)})
}
func (s *Server) apiRestrictAccountMedia(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	count, err := s.accounts.RestrictAllMediaFeatures(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "restrict account media features"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated_accounts": count})
}
func (s *Server) apiUpdateAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		ExpiresAt string `json:"expires_at"`
		Note      string `json:"note"`
		Version   int64  `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	expiresAt, parseErr := time.Parse(time.RFC3339, input.ExpiresAt)
	if err == nil {
		err = parseErr
	}
	if err == nil {
		account, updateErr := s.accounts.Update(r.Context(), id, accounts.UpdateInput{ExpiresAt: expiresAt, Note: input.Note, Version: input.Version})
		err = updateErr
		if err == nil {
			writeJSON(w, http.StatusOK, accountJSONFrom(account))
			return
		}
	}
	status := http.StatusBadRequest
	if errors.Is(err, accounts.ErrConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": accountError(err)})
}
func (s *Server) apiEnableAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Version int64 `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil {
		err = s.accounts.Enable(r.Context(), id, input.Version)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiDisableAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Version int64 `json:"version"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil {
		err = s.accounts.Disable(r.Context(), id, input.Version)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.accounts.Delete(r.Context(), id)
	}
	s.apiActionResult(w, err)
}
func (s *Server) apiActionResult(w http.ResponseWriter, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, accounts.ErrConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": accountError(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) apiInvites(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	codes, err := s.invites.List(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "load invites"})
		return
	}
	output := make([]inviteJSON, 0, len(codes))
	for _, code := range codes {
		output = append(output, inviteJSON{ID: code.ID, CodePrefix: code.CodePrefix, DurationDays: code.DurationDays, DurationMinutes: code.DurationMinutes, MaxUses: code.MaxUses, UsedCount: code.UsedCount, Enabled: code.Enabled, Note: code.Note})
	}
	writeJSON(w, 200, output)
}

type inviteJSON struct {
	ID              int64  `json:"id"`
	CodePrefix      string `json:"code_prefix"`
	DurationDays    int    `json:"duration_days"` // deprecated; use duration_minutes
	DurationMinutes int    `json:"duration_minutes"`
	MaxUses         int    `json:"max_uses"`
	UsedCount       int    `json:"used_count"`
	Enabled         bool   `json:"enabled"`
	Note            string `json:"note"`
}

func (s *Server) apiCreateInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	var input struct {
		DurationDays    int    `json:"duration_days"` // backwards-compatible input
		DurationMinutes int    `json:"duration_minutes"`
		MaxUses         int    `json:"max_uses"`
		Note            string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := s.invites.Create(r.Context(), invites.CreateInput{DurationDays: input.DurationDays, DurationMinutes: input.DurationMinutes, MaxUses: input.MaxUses, Note: input.Note})
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": inviteError(err)})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": created.Invite.ID, "code": created.Code})
}
func (s *Server) apiUpdateInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err == nil && !decodeJSON(w, r, &input) {
		return
	}
	if err == nil && input.Enabled == nil {
		err = errors.New("enabled is required")
	}
	if err == nil {
		err = s.invites.SetEnabled(r.Context(), id, *input.Enabled)
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "cannot update invite"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) apiDeleteInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	id, err := accountID(r)
	if err == nil {
		err = s.invites.Delete(r.Context(), id)
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "cannot delete invite"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) apiRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, err := s.invites.RegisterIdempotent(r.Context(), idempotencyKey, input.Code, input.Username, input.Password)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, accounts.ErrIdempotencyKeyConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": "registration failed"})
		return
	}
	writeJSON(w, http.StatusCreated, accountJSONFrom(account))
}
func (s *Server) apiRenew(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, err := s.invites.Renew(r.Context(), input.Code, input.Username, input.Password)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "renewal failed"})
		return
	}
	writeJSON(w, http.StatusOK, accountJSONFrom(account))
}

type accountJSON struct {
	ID         int64      `json:"id"`
	Version    int64      `json:"version"`
	EmbyUserID string     `json:"emby_user_id"`
	Username   string     `json:"username"`
	Status     string     `json:"status"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

func accountJSONFrom(account sqlite.Account) accountJSON {
	return accountJSON{
		ID: account.ID, Version: account.Version, EmbyUserID: account.EmbyUserID, Username: account.Username,
		Status: account.Status, ExpiresAt: account.ExpiresAt, Note: account.Note,
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, DisabledAt: account.DisabledAt,
	}
}

func accountJSONList(accounts []sqlite.Account) []accountJSON {
	output := make([]accountJSON, 0, len(accounts))
	for _, account := range accounts {
		output = append(output, accountJSONFrom(account))
	}
	return output
}
