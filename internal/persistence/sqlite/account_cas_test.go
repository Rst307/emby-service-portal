package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func TestAccountMutationsRequireCurrentVersion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.Version != 1 {
		t.Fatalf("created version = %d, want 1", account.Version)
	}
	staleDue := account
	account.ExpiresAt = now.Add(time.Hour)
	account.UpdatedAt = now
	account, err = store.UpdateAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if account.Version != 2 {
		t.Fatalf("updated version = %d, want 2", account.Version)
	}

	if _, err := store.SetAccountStatus(ctx, staleDue, "expired", &now, now); !errors.Is(err, domain.ErrAccountVersionConflict) {
		t.Fatalf("stale expiry error = %v, want version conflict", err)
	}
	current, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "active" || current.Version != 2 || !current.ExpiresAt.After(now) {
		t.Fatalf("stale expiry changed account: %#v", current)
	}
	jobs, err := store.ListAccessSyncJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("stale expiry queued jobs: %#v", jobs)
	}
}
