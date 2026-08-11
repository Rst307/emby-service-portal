// Package accounts owns the business-account lifecycle and Emby synchronization.
package accounts

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Rst307/emby-service-portal/internal/credentials"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

var (
	ErrInvalidUsername                 = errors.New("username is required")
	ErrInvalidPassword                 = errors.New("password must contain at least 8 characters")
	ErrExpiredAccount                  = errors.New("cannot enable an expired account")
	ErrNotFound                        = errors.New("account not found")
	ErrInvalidCredentials              = errors.New("invalid account credentials")
	ErrConflict                        = sqlite.ErrAccountVersionConflict
	ErrIdempotencyKeyRequired          = errors.New("idempotency key is required")
	ErrIdempotencyKeyConflict          = sqlite.ErrIdempotencyKeyConflict
	ErrProvisioningRecoveryUnavailable = errors.New("cannot safely recover uncertain Emby user creation")
)

type Service struct {
	store       *sqlite.Store
	emby        emby.Client
	vault       *credentials.Vault
	now         func() time.Time
	provisionMu sync.Mutex
}

type CreateInput struct {
	Username  string
	Password  string
	ExpiresAt time.Time
	Note      string
}

type UpdateInput struct {
	ExpiresAt time.Time
	Note      string
	Version   int64
}
type SyncInput struct {
	ExpiresAt time.Time
	Note      string
}

type BatchAction string

const (
	BatchSetExpiry BatchAction = "set_expiry"
	BatchExtend    BatchAction = "extend"
	BatchReduce    BatchAction = "reduce"
	BatchEnable    BatchAction = "enable"
	BatchDisable   BatchAction = "disable"
)

type BatchInput struct {
	AccountIDs []int64
	Versions   map[int64]int64
	Action     BatchAction
	ExpiresAt  time.Time
	Duration   time.Duration
}

func New(store *sqlite.Store, embyClient emby.Client, vault *credentials.Vault) *Service {
	return &Service{store: store, emby: embyClient, vault: vault, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (sqlite.Account, error) {
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" {
		return sqlite.Account{}, ErrInvalidUsername
	}
	if len(input.Password) < 8 {
		return sqlite.Account{}, ErrInvalidPassword
	}
	if input.ExpiresAt.IsZero() {
		return sqlite.Account{}, fmt.Errorf("expiry time is required")
	}
	user, err := s.emby.CreateUser(ctx, input.Username, input.Password)
	if err != nil {
		return sqlite.Account{}, err
	}
	if restrict, ok := s.emby.(emby.PolicyRestricter); ok {
		if err := restrict.RestrictUserMediaFeatures(ctx, user.ID); err != nil {
			_ = s.emby.DeleteUser(ctx, user.ID)
			return sqlite.Account{}, fmt.Errorf("restrict Emby media features: %w", err)
		}
	}
	now := s.now().UTC()
	status := "active"
	var disabledAt *time.Time
	if !input.ExpiresAt.After(now) {
		status = "expired"
		disabledAt = &now
	}
	ciphertext, err := s.vault.Seal(input.Username, input.Password)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("encrypt password: %w", err)
	}
	account, err := s.store.CreateAccount(ctx, sqlite.Account{EmbyUserID: user.ID, Username: input.Username, Status: status, ExpiresAt: input.ExpiresAt.UTC(), Note: strings.TrimSpace(input.Note), CreatedAt: now, UpdatedAt: now, DisabledAt: disabledAt})
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("record Emby user %q: %w", input.Username, err)
	}
	if err := s.store.SaveAccountPassword(ctx, account.ID, ciphertext, now); err != nil {
		return sqlite.Account{}, fmt.Errorf("save encrypted password: %w", err)
	}
	if account.Status != "active" {
		account, err = s.store.SetAccountStatus(ctx, account, account.Status, account.DisabledAt, now)
		if err != nil {
			return sqlite.Account{}, fmt.Errorf("queue initial Emby access policy: %w", err)
		}
	}
	return account, nil
}

// CreateIdempotent durably records an API account-create request before
// calling Emby. Retrying a key resumes its operation rather than reissuing a
// remote create or making a second local business account.
func (s *Service) CreateIdempotent(ctx context.Context, idempotencyKey string, input CreateInput) (sqlite.Account, error) {
	idempotencyKey, err := validIdempotencyKey(idempotencyKey)
	if err != nil {
		return sqlite.Account{}, err
	}
	input, err = normalizedCreateInput(input)
	if err != nil {
		return sqlite.Account{}, err
	}
	ciphertext, err := s.vault.Seal(input.Username, input.Password)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("encrypt password: %w", err)
	}
	fingerprint, err := s.vault.Fingerprint("account_create", input.Username, input.Password, timestampInput(input.ExpiresAt), input.Note)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("fingerprint account create: %w", err)
	}
	operation, err := s.store.BeginAccountCreateOperation(ctx, sqlite.BeginAccountCreateOperationInput{
		Kind: "account_create", IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint,
		Username: input.Username, PasswordCiphertext: ciphertext, ExpiresAt: input.ExpiresAt, Note: input.Note, Now: s.now().UTC(),
	})
	if err != nil {
		return sqlite.Account{}, err
	}
	return s.provisionAccountCreate(ctx, operation)
}

// RegisterIdempotent is the registration half of the durable provisioning
// saga. inviteCodeHash must be a one-way hash of the submitted raw invite.
func (s *Service) RegisterIdempotent(ctx context.Context, idempotencyKey, inviteCodeHash, username, password string) (sqlite.Account, error) {
	idempotencyKey, err := validIdempotencyKey(idempotencyKey)
	if err != nil {
		return sqlite.Account{}, err
	}
	input, err := normalizedCredentials(username, password)
	if err != nil {
		return sqlite.Account{}, err
	}
	ciphertext, err := s.vault.Seal(input.Username, input.Password)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("encrypt password: %w", err)
	}
	fingerprint, err := s.vault.Fingerprint("register", inviteCodeHash, input.Username, input.Password)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("fingerprint registration: %w", err)
	}
	operation, err := s.store.BeginRegistrationOperation(ctx, sqlite.BeginRegistrationOperationInput{
		BeginAccountCreateOperationInput: sqlite.BeginAccountCreateOperationInput{
			Kind: "register", IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint,
			Username: input.Username, PasswordCiphertext: ciphertext, Note: "", Now: s.now().UTC(),
		},
		InviteCodeHash: inviteCodeHash,
	})
	if errors.Is(err, sqlite.ErrInviteNotRedeemable) {
		return sqlite.Account{}, err
	}
	if err != nil {
		return sqlite.Account{}, err
	}
	return s.provisionAccountCreate(ctx, operation)
}

// RecoverAccountCreates retries every incomplete provisioning operation. It
// is safe to run repeatedly; completed operations are excluded and each
// remote create is checkpointed before local finalization.
func (s *Service) RecoverAccountCreates(ctx context.Context) error {
	operations, err := s.store.ListIncompleteAccountCreateOperations(ctx, 100)
	if err != nil {
		return err
	}
	var firstErr error
	for _, operation := range operations {
		if _, err := s.provisionAccountCreate(ctx, operation); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) provisionAccountCreate(ctx context.Context, operation sqlite.AccountCreateOperation) (sqlite.Account, error) {
	// The durable state resolves restarts; this in-process guard also prevents
	// two simultaneous HTTP retries from both acting on a stale pending row.
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()
	var err error
	operation, err = s.store.FindAccountCreateOperation(ctx, operation.ID)
	if err != nil {
		return sqlite.Account{}, err
	}
	if operation.Status == "completed" {
		return s.store.CompleteAccountCreateOperation(ctx, operation.ID, s.now().UTC())
	}
	password, err := s.vault.Open(operation.Username, operation.PasswordCiphertext)
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("decrypt provisioning password: %w", err)
	}
	remoteUserID := operation.EmbyUserID
	if remoteUserID == "" {
		createNow := operation.Status == "pending"
		if createNow {
			if err := s.store.MarkAccountCreateOperationCreating(ctx, operation.ID, s.now().UTC()); err != nil {
				return sqlite.Account{}, fmt.Errorf("checkpoint Emby create attempt: %w", err)
			}
		}
		var user emby.User
		foundByLookup := false
		if !createNow {
			finder, ok := s.emby.(emby.UserFinder)
			if !ok {
				return sqlite.Account{}, ErrProvisioningRecoveryUnavailable
			}
			user, err = finder.FindUserByUsername(ctx, operation.Username)
			if err == nil {
				foundByLookup = true
			} else if !errors.Is(err, emby.ErrUserNotFound) {
				return sqlite.Account{}, fmt.Errorf("find Emby user %q: %w", operation.Username, err)
			}
		}
		if !foundByLookup {
			user, err = s.emby.CreateUser(ctx, operation.Username, password)
			if err != nil {
				// The creating checkpoint deliberately remains. The outcome may be
				// unknown, so a later retry must look up the name before retrying.
				return sqlite.Account{}, err
			}
		}
		if user.ID == "" {
			return sqlite.Account{}, fmt.Errorf("emby user creation returned no user ID")
		}
		if foundByLookup {
			if setter, ok := s.emby.(emby.PasswordSetter); ok {
				if err := setter.SetUserPassword(ctx, user.ID, password); err != nil {
					return sqlite.Account{}, fmt.Errorf("restore Emby password: %w", err)
				}
			}
		}
		remoteUserID = user.ID
		if err := s.store.SaveAccountCreateOperationRemote(ctx, operation.ID, remoteUserID, s.now().UTC()); err != nil {
			return sqlite.Account{}, fmt.Errorf("checkpoint Emby user: %w", err)
		}
	}
	if restrict, ok := s.emby.(emby.PolicyRestricter); ok {
		if err := restrict.RestrictUserMediaFeatures(ctx, remoteUserID); err != nil {
			return sqlite.Account{}, fmt.Errorf("restrict Emby media features: %w", err)
		}
	}
	account, err := s.store.CompleteAccountCreateOperation(ctx, operation.ID, s.now().UTC())
	if err != nil {
		return sqlite.Account{}, fmt.Errorf("finalize account provisioning: %w", err)
	}
	return account, nil
}

func normalizedCredentials(username, password string) (CreateInput, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return CreateInput{}, ErrInvalidUsername
	}
	if len(password) < 8 {
		return CreateInput{}, ErrInvalidPassword
	}
	return CreateInput{Username: username, Password: password}, nil
}

func normalizedCreateInput(input CreateInput) (CreateInput, error) {
	credentials, err := normalizedCredentials(input.Username, input.Password)
	if err != nil {
		return CreateInput{}, err
	}
	if input.ExpiresAt.IsZero() {
		return CreateInput{}, fmt.Errorf("expiry time is required")
	}
	credentials.ExpiresAt = input.ExpiresAt.UTC()
	credentials.Note = strings.TrimSpace(input.Note)
	return credentials, nil
}

func validIdempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return "", ErrIdempotencyKeyRequired
	}
	return value, nil
}

func timestampInput(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func (s *Service) List(ctx context.Context) ([]sqlite.Account, error) {
	return s.store.ListAccounts(ctx)
}
func (s *Service) SyncFromEmby(ctx context.Context, input SyncInput) (int, error) {
	if input.ExpiresAt.IsZero() {
		return 0, fmt.Errorf("expiry time is required")
	}
	lister, ok := s.emby.(emby.UserLister)
	if !ok {
		return 0, errors.New("emby client does not support user listing")
	}
	users, err := lister.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	local, err := s.store.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	existing := make(map[string]struct{}, len(local))
	for _, account := range local {
		existing[strings.ToLower(account.Username)] = struct{}{}
	}
	now := s.now().UTC()
	imported := 0
	for _, user := range users {
		if user.ID == "" || strings.TrimSpace(user.Username) == "" || user.Policy.IsAdministrator {
			continue
		}
		if _, ok := existing[strings.ToLower(user.Username)]; ok {
			continue
		}
		status := "active"
		var disabledAt *time.Time
		if user.Policy.IsDisabled {
			status = "disabled"
			disabledAt = &now
		} else if !input.ExpiresAt.After(now) {
			status = "expired"
			disabledAt = &now
		}
		if restrict, ok := s.emby.(emby.PolicyRestricter); ok {
			if err := restrict.RestrictUserMediaFeatures(ctx, user.ID); err != nil {
				return imported, err
			}
		}
		account, err := s.store.CreateAccount(ctx, sqlite.Account{EmbyUserID: user.ID, Username: user.Username, Status: status, ExpiresAt: input.ExpiresAt.UTC(), Note: strings.TrimSpace(input.Note), CreatedAt: now, UpdatedAt: now, DisabledAt: disabledAt})
		if err != nil {
			return imported, err
		}
		if account.Status != "active" {
			if _, err := s.store.SetAccountStatus(ctx, account, account.Status, account.DisabledAt, now); err != nil {
				return imported, fmt.Errorf("queue %q Emby access policy: %w", account.Username, err)
			}
		}
		existing[strings.ToLower(user.Username)] = struct{}{}
		imported++
	}
	return imported, nil
}
func (s *Service) RestrictAllMediaFeatures(ctx context.Context) (int, error) {
	restrict, ok := s.emby.(emby.PolicyRestricter)
	if !ok {
		return 0, errors.New("emby client does not support policy restrictions")
	}
	list, err := s.store.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	for _, account := range list {
		if err := restrict.RestrictUserMediaFeatures(ctx, account.EmbyUserID); err != nil {
			return 0, fmt.Errorf("restrict %q: %w", account.Username, err)
		}
	}
	return len(list), nil
}

// VerifyPassword verifies a managed account password without requiring the
// Emby user to be enabled. This lets an expired user authenticate a renewal
// while still preventing a code from being applied to an arbitrary username.
func (s *Service) VerifyPassword(ctx context.Context, username, password string) error {
	account, err := s.store.FindAccountByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	stored, err := s.Password(ctx, account.ID)
	if err != nil {
		return ErrInvalidCredentials
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(password)) != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Service) Password(ctx context.Context, id int64) (string, error) {
	account, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	ciphertext, err := s.store.AccountPasswordCiphertext(ctx, id)
	if err != nil {
		return "", err
	}
	return s.vault.Open(account.Username, ciphertext)
}
func (s *Service) Get(ctx context.Context, id int64) (sqlite.Account, error) {
	account, err := s.store.FindAccount(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlite.Account{}, ErrNotFound
	}
	return account, err
}

// Batch applies the same management operation to every selected account. It
// validates the complete selection before changing any account; a later
// persistence failure can still leave already-applied operations intact.
func (s *Service) Batch(ctx context.Context, input BatchInput) (int, error) {
	ids, err := uniqueAccountIDs(input.AccountIDs)
	if err != nil {
		return 0, err
	}
	if input.Action != BatchSetExpiry && input.Action != BatchExtend && input.Action != BatchReduce && input.Action != BatchEnable && input.Action != BatchDisable {
		return 0, errors.New("invalid batch action")
	}
	if (input.Action == BatchSetExpiry && input.ExpiresAt.IsZero()) || ((input.Action == BatchExtend || input.Action == BatchReduce) && input.Duration <= 0) {
		return 0, errors.New("invalid batch expiry value")
	}

	accounts := make([]sqlite.Account, 0, len(ids))
	for _, id := range ids {
		account, err := s.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if expected := input.Versions[id]; expected != 0 && expected != account.Version {
			return 0, ErrConflict
		}
		accounts = append(accounts, account)
	}
	now := s.now()
	if input.Action == BatchEnable {
		for _, account := range accounts {
			if !account.ExpiresAt.After(now) {
				return 0, ErrExpiredAccount
			}
		}
	}
	for completed, account := range accounts {
		var err error
		switch input.Action {
		case BatchSetExpiry:
			_, err = s.Update(ctx, account.ID, UpdateInput{ExpiresAt: input.ExpiresAt, Note: account.Note, Version: account.Version})
		case BatchExtend:
			_, err = s.Update(ctx, account.ID, UpdateInput{ExpiresAt: account.ExpiresAt.Add(input.Duration), Note: account.Note, Version: account.Version})
		case BatchReduce:
			_, err = s.Update(ctx, account.ID, UpdateInput{ExpiresAt: account.ExpiresAt.Add(-input.Duration), Note: account.Note, Version: account.Version})
		case BatchEnable:
			err = s.Enable(ctx, account.ID, account.Version)
		case BatchDisable:
			err = s.Disable(ctx, account.ID, account.Version)
		}
		if err != nil {
			return completed, fmt.Errorf("batch operation stopped: %w", err)
		}
	}
	return len(accounts), nil
}

func uniqueAccountIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, errors.New("select at least one account")
	}
	unique := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, errors.New("invalid account ID")
		}
		if _, exists := unique[id]; !exists {
			unique[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (sqlite.Account, error) {
	account, err := s.Get(ctx, id)
	if err != nil {
		return sqlite.Account{}, err
	}
	if err := checkVersion(account, input.Version); err != nil {
		return sqlite.Account{}, err
	}
	if input.ExpiresAt.IsZero() {
		return sqlite.Account{}, fmt.Errorf("expiry time is required")
	}
	account.ExpiresAt = input.ExpiresAt.UTC()
	account.Note = strings.TrimSpace(input.Note)
	now := s.now().UTC()
	account.UpdatedAt = now
	if account.Status == "active" && !account.ExpiresAt.After(now) {
		return s.store.UpdateAccountAndExpire(ctx, account, now, now)
	}
	account, err = s.store.UpdateAccount(ctx, account)
	if err != nil {
		return sqlite.Account{}, err
	}
	return account, nil
}

// Disable records the desired disabled state before the expiry runner calls
// Emby. An optional version lets HTTP and form clients reject stale actions.
func (s *Service) Disable(ctx context.Context, id int64, version ...int64) error {
	account, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkVersion(account, optionalVersion(version)); err != nil {
		return err
	}
	now := s.now().UTC()
	_, err = s.store.SetAccountStatus(ctx, account, "disabled", &now, now)
	return err
}

// Enable records the desired enabled state before the expiry runner calls
// Emby. It never enables an account whose subscription has ended.
func (s *Service) Enable(ctx context.Context, id int64, version ...int64) error {
	account, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkVersion(account, optionalVersion(version)); err != nil {
		return err
	}
	if !account.ExpiresAt.After(s.now()) {
		return ErrExpiredAccount
	}
	_, err = s.store.SetAccountStatus(ctx, account, "active", nil, s.now().UTC())
	return err
}

func checkVersion(account sqlite.Account, expected int64) error {
	if expected != 0 && expected != account.Version {
		return ErrConflict
	}
	return nil
}

func optionalVersion(versions []int64) int64 {
	if len(versions) == 0 {
		return 0
	}
	return versions[0]
}
func (s *Service) Delete(ctx context.Context, id int64) error {
	account, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.emby.DeleteUser(ctx, account.EmbyUserID); err != nil && !errors.Is(err, emby.ErrUserNotFound) {
		return err
	}
	return s.store.DeleteAccount(ctx, id)
}
