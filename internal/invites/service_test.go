package invites

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/accounts"
	"github.com/Rst307/emby-service-portal/internal/credentials"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

type registrationSagaEmby struct{ createCalls int }

func (f *registrationSagaEmby) CreateUser(_ context.Context, username, _ string) (emby.User, error) {
	f.createCalls++
	return emby.User{ID: "registration-user", Username: username}, nil
}
func (*registrationSagaEmby) DeleteUser(context.Context, string) error            { return nil }
func (*registrationSagaEmby) SetUserDisabled(context.Context, string, bool) error { return nil }

func TestRenewForAuthenticatedAccountDoesNotNeedPassword(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	account, err := store.CreateAccount(ctx, sqlite.Account{EmbyUserID: "renew-user", Username: "alice", Status: "active", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, nil)
	created, err := service.Create(ctx, CreateInput{DurationMinutes: 24 * 60, MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := service.RenewForAccount(ctx, created.Code, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != account.ID || !renewed.ExpiresAt.Equal(account.ExpiresAt.Add(24*time.Hour)) {
		t.Fatalf("renewed account = %+v", renewed)
	}
}

func TestIdempotentRegistrationDoesNotConsumeInviteTwice(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := &registrationSagaEmby{}
	accountService := accounts.New(store, client, credentials.New("test-key"))
	service := New(store, accountService)
	created, err := service.Create(ctx, CreateInput{DurationMinutes: 60, MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := []string{"register-alice", created.Code, "alice", "password123"}
	account, err := service.RegisterIdempotent(ctx, input[0], input[1], input[2], input[3])
	if err != nil {
		t.Fatalf("first registration: %v", err)
	}
	replayed, err := service.RegisterIdempotent(ctx, input[0], input[1], input[2], input[3])
	if err != nil {
		t.Fatalf("replay registration: %v", err)
	}
	if replayed.ID != account.ID || client.createCalls != 1 {
		t.Fatalf("replay = %#v, creates = %d; want original account and one remote create", replayed, client.createCalls)
	}
	invites, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 || invites[0].UsedCount != 1 {
		t.Fatalf("invite usage = %#v; want one use", invites)
	}
	registered, err := accountService.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Username != "alice" || !registered[0].ExpiresAt.After(time.Now()) {
		t.Fatalf("registered accounts = %#v", registered)
	}
}
