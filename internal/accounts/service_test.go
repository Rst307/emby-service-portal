package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/credentials"
	"github.com/emby-user-manager/emby-user-manager/internal/emby"
	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

type deletedUpstreamEmby struct{}

type countingEmby struct{ setDisabledCalls int }

type sagaEmby struct {
	createCalls      int
	cancelOnRestrict context.CancelFunc
}

func (f *sagaEmby) CreateUser(_ context.Context, username, _ string) (emby.User, error) {
	f.createCalls++
	return emby.User{ID: "saga-user", Username: username}, nil
}
func (*sagaEmby) DeleteUser(context.Context, string) error            { return nil }
func (*sagaEmby) SetUserDisabled(context.Context, string, bool) error { return nil }
func (f *sagaEmby) RestrictUserMediaFeatures(context.Context, string) error {
	if f.cancelOnRestrict != nil {
		f.cancelOnRestrict()
		f.cancelOnRestrict = nil
	}
	return nil
}

func (f *countingEmby) CreateUser(context.Context, string, string) (emby.User, error) {
	return emby.User{}, nil
}
func (f *countingEmby) DeleteUser(context.Context, string) error { return nil }
func (f *countingEmby) SetUserDisabled(context.Context, string, bool) error {
	f.setDisabledCalls++
	return nil
}

func (deletedUpstreamEmby) CreateUser(context.Context, string, string) (emby.User, error) {
	return emby.User{}, nil
}
func (deletedUpstreamEmby) DeleteUser(context.Context, string) error            { return emby.ErrUserNotFound }
func (deletedUpstreamEmby) SetUserDisabled(context.Context, string, bool) error { return nil }

func TestDisableQueuesAccessSyncBeforeContactingEmby(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	account, err := store.CreateAccount(ctx, sqlite.Account{EmbyUserID: "u1", Username: "alice", Status: "active", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	client := &countingEmby{}
	service := New(store, client, credentials.New("test-key"))
	if err := service.Disable(ctx, account.ID, account.Version); err != nil {
		t.Fatal(err)
	}
	if client.setDisabledCalls != 0 {
		t.Fatalf("disable contacted Emby %d times; want local outbox only", client.setDisabledCalls)
	}
	updated, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "disabled" || updated.Version != account.Version+1 {
		t.Fatalf("disabled account = %#v", updated)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || !jobs[0].DesiredDisabled {
		t.Fatalf("access sync jobs = %#v", jobs)
	}
}

func TestIdempotentCreateResumesAfterLocalFinalizationFailure(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := &sagaEmby{}
	service := New(store, client, credentials.New("test-key"))
	firstAttempt, cancel := context.WithCancel(context.Background())
	client.cancelOnRestrict = cancel
	input := CreateInput{Username: "alice", Password: "password123", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := service.CreateIdempotent(firstAttempt, "create-alice", input); err == nil {
		t.Fatal("first request unexpectedly finalized after its context was canceled")
	}
	operations, err := store.ListIncompleteAccountCreateOperations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Status != "remote_created" || operations[0].EmbyUserID != "saga-user" {
		t.Fatalf("incomplete operations = %#v", operations)
	}
	if operations[0].PasswordCiphertext == input.Password {
		t.Fatal("operation stored the plaintext password")
	}

	account, err := service.CreateIdempotent(context.Background(), "create-alice", input)
	if err != nil {
		t.Fatalf("resume account creation: %v", err)
	}
	if account.Username != "alice" || client.createCalls != 1 {
		t.Fatalf("resumed account = %#v, create calls = %d; want one remote create", account, client.createCalls)
	}
	replayed, err := service.CreateIdempotent(context.Background(), "create-alice", input)
	if err != nil {
		t.Fatalf("replay completed account creation: %v", err)
	}
	if replayed.ID != account.ID || client.createCalls != 1 {
		t.Fatalf("replay = %#v, create calls = %d; want original account and one create", replayed, client.createCalls)
	}
}

func TestUpdateToPastExpiresAccountAndQueuesPolicyInOneVersion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, sqlite.Account{EmbyUserID: "u1", Username: "alice", Status: "active", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, &countingEmby{}, credentials.New("test-key"))
	service.now = func() time.Time { return now }

	updated, err := service.Update(ctx, account.ID, UpdateInput{ExpiresAt: now.Add(-time.Minute), Version: account.Version})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "expired" || updated.Version != account.Version+1 {
		t.Fatalf("updated account = %#v, want expired with one version increment", updated)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || !jobs[0].DesiredDisabled {
		t.Fatalf("access sync jobs = %#v, want one disable", jobs)
	}
}

func TestBatchRelativeExpiryDoesNotOverwriteConcurrentUpdate(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, sqlite.Account{EmbyUserID: "u1", Username: "alice", Status: "active", ExpiresAt: base.Add(24 * time.Hour), CreatedAt: base, UpdatedAt: base})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, &countingEmby{}, credentials.New("test-key"))
	updatedConcurrently := false
	service.now = func() time.Time {
		if !updatedConcurrently {
			updatedConcurrently = true
			concurrent := account
			concurrent.ExpiresAt = base.Add(72 * time.Hour)
			concurrent.UpdatedAt = base.Add(time.Minute)
			if _, err := store.UpdateAccount(ctx, concurrent); err != nil {
				t.Fatalf("concurrent update: %v", err)
			}
		}
		return base
	}

	completed, err := service.Batch(ctx, BatchInput{AccountIDs: []int64{account.ID}, Action: BatchExtend, Duration: 24 * time.Hour})
	if completed != 0 || !errors.Is(err, ErrConflict) {
		t.Fatalf("Batch() = (%d, %v), want (0, conflict)", completed, err)
	}
	persisted, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.ExpiresAt.Equal(base.Add(72 * time.Hour)) {
		t.Fatalf("expiry = %s, concurrent update was overwritten", persisted.ExpiresAt)
	}
}

func TestDeleteRemovesLocalAccountWhenEmbyUserWasAlreadyDeleted(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	account, err := store.CreateAccount(ctx, sqlite.Account{EmbyUserID: "gone", Username: "gone", Status: "active", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := store.CreateInvite(ctx, sqlite.InviteCode{CodeHash: "hash", Code: "code", CodePrefix: "code", DurationDays: 1, DurationMinutes: 1440, MaxUses: 1, Enabled: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRedemption(ctx, invite.ID, account.ID, "register", 1, 1440, now); err != nil {
		t.Fatal(err)
	}
	service := New(store, deletedUpstreamEmby{}, credentials.New("test-key"))
	if err := service.Delete(ctx, account.ID); err != nil {
		t.Fatalf("delete account whose Emby user is already absent: %v", err)
	}
	if _, err := service.Get(ctx, account.ID); err == nil {
		t.Fatal("local account was not deleted")
	}
}
