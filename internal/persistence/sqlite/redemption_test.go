package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func TestListInvitesIncludesTheBusinessAccountThatUsedTheCode(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{EmbyUserID: "emby-alice", Username: "alice", Status: "active", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := store.CreateInvite(ctx, domain.InviteCode{CodeHash: "invite-hash", Code: "ESP-ACT-test", CodePrefix: "ESP-ACT", DurationDays: 1, DurationMinutes: 24 * 60, MaxUses: 1, Enabled: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeInvite(ctx, invite.CodeHash, now); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRedemption(ctx, invite.ID, account.ID, "register", invite.DurationDays, invite.DurationMinutes, now); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListInvites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].Redemptions) != 1 {
		t.Fatalf("listed invites = %+v", listed)
	}
	redemption := listed[0].Redemptions[0]
	if redemption.AccountID != account.ID || redemption.AccountUsername != "alice" || redemption.Kind != "register" || !redemption.RedeemedAt.Equal(now) {
		t.Fatalf("redemption = %+v", redemption)
	}
}

func TestRedeemRenewalAtomicallyExtendsEveryConcurrentRedemption(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: createdAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := store.CreateInvite(ctx, domain.InviteCode{
		CodeHash: "invite-hash", CodePrefix: "ESP-test", DurationDays: 1, DurationMinutes: 60,
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
			_, err := store.RedeemRenewal(ctx, domain.RedeemRenewalInput{
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
