package expiry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

type fakeEmby struct {
	disabled bool
	err      error
}

func (f *fakeEmby) CreateUser(context.Context, string, string) (emby.User, error) {
	return emby.User{}, nil
}
func (f *fakeEmby) DeleteUser(context.Context, string) error { return nil }
func (f *fakeEmby) SetUserDisabled(_ context.Context, _ string, disabled bool) error {
	if f.err != nil {
		return f.err
	}
	f.disabled = disabled
	return nil
}

func TestRunOnceKeepsFailedAccessSyncForRetry(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	account, err := store.CreateAccount(ctx, domain.Account{EmbyUserID: "u1", Username: "due", Status: "active", ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEmby{err: errors.New("Emby unavailable")}
	runner := New(store, client)
	if err := runner.RunOnce(ctx); err == nil {
		t.Fatal("expected a failed Emby sync to be reported")
	}
	updated, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "expired" {
		t.Fatalf("status = %q, want expired", updated.Status)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Attempts != 1 || !jobs[0].DesiredDisabled {
		t.Fatalf("failed job = %#v, want one disabled retry", jobs)
	}

	client.err = nil
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	jobs, err = store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !client.disabled || len(jobs) != 0 {
		t.Fatalf("sync not completed: disabled=%t jobs=%#v", client.disabled, jobs)
	}
}

func TestExpireDueDoesNotDisableAnAccountRenewedAfterItsScan(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{EmbyUserID: "u1", Username: "due", Status: "active", ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueActiveAccounts(ctx, now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due accounts = %#v, err = %v", due, err)
	}
	if _, err := store.CreateInvite(ctx, domain.InviteCode{CodeHash: "renew", CodePrefix: "renew", DurationDays: 1, DurationMinutes: 60, MaxUses: 1, Enabled: true, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RedeemRenewal(ctx, domain.RedeemRenewalInput{CodeHash: "renew", Username: account.Username, RedeemedAt: now}); err != nil {
		t.Fatal(err)
	}

	runner := New(store, &fakeEmby{})
	if failures := runner.expireDue(ctx, due, now); len(failures) != 0 {
		t.Fatalf("stale expiry failures = %v", failures)
	}
	updated, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "active" || !updated.ExpiresAt.After(now) {
		t.Fatalf("stale expiry disabled renewed account: %#v", updated)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("stale expiry queued disable: %#v", jobs)
	}
}

func TestRunOnceDisablesDueActiveAccount(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	account, err := store.CreateAccount(context.Background(), domain.Account{EmbyUserID: "u1", Username: "due", Status: "active", ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEmby{}
	if err := New(store, client).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := store.FindAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !client.disabled || updated.Status != "expired" || updated.DisabledAt == nil {
		t.Fatalf("expected disabled expired account, got %#v", updated)
	}
}
