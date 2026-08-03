package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRedeemRenewalAtomicallyExtendsEveryConcurrentRedemption(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: createdAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := store.CreateInvite(ctx, InviteCode{
		CodeHash: "invite-hash", CodePrefix: "EUM-test", DurationDays: 1, DurationMinutes: 60,
		MaxUses: 2, Enabled: true, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.RedeemRenewal(ctx, RedeemRenewalInput{
				CodeHash: "invite-hash", Username: "alice", RedeemedAt: createdAt,
			})
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("renewal failed: %v", err)
		}
	}

	renewed, err := store.FindAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := createdAt.Add(2 * time.Hour)
	if !renewed.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiry = %s, want %s", renewed.ExpiresAt, wantExpiry)
	}
	if renewed.Version != 3 {
		t.Fatalf("version = %d, want 3 after two renewals", renewed.Version)
	}
	var usedCount, redemptions int
	if err := store.db.QueryRowContext(ctx, `SELECT used_count FROM invite_codes WHERE id = ?`, invite.ID).Scan(&usedCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invite_redemptions WHERE invite_code_id = ? AND account_id = ? AND kind = 'renew'`, invite.ID, account.ID).Scan(&redemptions); err != nil {
		t.Fatal(err)
	}
	if usedCount != 2 || redemptions != 2 {
		t.Fatalf("used_count = %d, redemptions = %d; want 2, 2", usedCount, redemptions)
	}
}
