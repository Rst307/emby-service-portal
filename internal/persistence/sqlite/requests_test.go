package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func testAccount(t *testing.T, ctx context.Context, store *Store, username string, now time.Time) domain.Account {
	t.Helper()
	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-" + username, Username: username, Status: "active",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestUpsertMediaRequestCreatesAndReactivates(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account := testAccount(t, ctx, store, "alice", now)

	input := domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar",
		Overview: "近未来地球荒芜", PosterPath: "/x.jpg", ReleaseDate: "2014-11-05", Now: now,
	}
	created, err := store.UpsertMediaRequest(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != domain.MediaRequestPending || created.Title != "星际穿越" || created.TmdbID != 157336 {
		t.Fatalf("created = %+v", created)
	}

	// Re-submission must reactivate the same row rather than inserting a new one.
	if err := store.SetMediaRequestStatus(ctx, created.ID, domain.MediaRequestRejected, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	input.Now = now.Add(2 * time.Hour)
	input.Title = "星际穿越 4K"
	reactivated, err := store.UpsertMediaRequest(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.ID != created.ID {
		t.Fatalf("reactivated id = %d, want %d (unique per account+tmdb)", reactivated.ID, created.ID)
	}
	if reactivated.Status != domain.MediaRequestPending || reactivated.Title != "星际穿越 4K" {
		t.Fatalf("reactivated = %+v", reactivated)
	}
	if !reactivated.UpdatedAt.After(reactivated.CreatedAt) {
		t.Fatalf("updated_at did not advance: %s > %s", reactivated.UpdatedAt, reactivated.CreatedAt)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_requests WHERE account_id = ?", account.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

func TestListMediaRequestsFiltersAndCounts(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	alice := testAccount(t, ctx, store, "alice", now)
	bob := testAccount(t, ctx, store, "bob", now)

	makeRequest := func(account domain.Account, id int64, mediaType, title string, at time.Time) domain.MediaRequest {
		created, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
			AccountID: account.ID, AccountUsername: account.Username, TmdbID: id, MediaType: mediaType,
			Title: title, Now: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := makeRequest(alice, 157336, "movie", "星际穿越", now)
	second := makeRequest(alice, 1399, "tv", "权力的游戏", now.Add(time.Minute))
	third := makeRequest(bob, 157336, "movie", "星际穿越", now.Add(2*time.Minute))
	if err := store.SetMediaRequestStatus(ctx, second.ID, domain.MediaRequestFulfilled, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaRequestStatus(ctx, third.ID, domain.MediaRequestRejected, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}

	page, err := store.ListMediaRequests(ctx, domain.MediaRequestFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Pending != 1 || page.Fulfilled != 1 {
		t.Fatalf("unfiltered counts = total %d pending %d fulfilled %d, want 3/1/1", page.Total, page.Pending, page.Fulfilled)
	}
	if len(page.Requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(page.Requests))
	}
	// Ordered newest first: bob's request is at id 3 and newest.
	if page.Requests[0].ID != third.ID || page.Requests[0].Status != domain.MediaRequestRejected {
		t.Fatalf("newest first: %+v", page.Requests[0])
	}

	pendingPage, err := store.ListMediaRequests(ctx, domain.MediaRequestFilter{Status: domain.MediaRequestPending, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if pendingPage.Total != 1 || pendingPage.Requests[0].ID != first.ID {
		t.Fatalf("pending filter = %+v", pendingPage)
	}

	queryPage, err := store.ListMediaRequests(ctx, domain.MediaRequestFilter{Query: "星际穿越", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if queryPage.Total != 2 {
		t.Fatalf("query filter total = %d, want 2", queryPage.Total)
	}

	userPage, err := store.ListMediaRequests(ctx, domain.MediaRequestFilter{Query: "bob", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if userPage.Total != 1 || userPage.Requests[0].AccountUsername != "bob" {
		t.Fatalf("user query = %+v", userPage)
	}

	// Pagination bounds pages even with a page beyond the data.
	emptyPage, err := store.ListMediaRequests(ctx, domain.MediaRequestFilter{Page: 9, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyPage.Requests) != 0 || emptyPage.Total != 3 {
		t.Fatalf("off-page = %+v", emptyPage)
	}
}

func TestListMediaRequestsForAccountKeys(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	alice := testAccount(t, ctx, store, "alice", now)

	for _, spec := range []struct {
		id        int64
		mediaType string
	}{
		{157336, "movie"}, {1399, "tv"},
	} {
		if _, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
			AccountID: alice.ID, AccountUsername: alice.Username, TmdbID: spec.id, MediaType: spec.mediaType, Title: "title", Now: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := store.ListMediaRequestsForAccount(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	if keys["movie:157336"] != domain.MediaRequestPending || keys["tv:1399"] != domain.MediaRequestPending {
		t.Fatalf("keys = %v", keys)
	}
}

func TestDeleteMediaRequestRemovesRow(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	alice := testAccount(t, ctx, store, "alice", now)
	created, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: alice.ID, AccountUsername: alice.Username, TmdbID: 42, MediaType: "tv", Title: "测试", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMediaRequest(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMediaRequest(ctx, created.ID); err != domain.ErrRequestNotFound {
		t.Fatalf("second delete error = %v, want ErrRequestNotFound", err)
	}
}
