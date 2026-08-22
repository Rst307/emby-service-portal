package recent

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

type fakeWatcher struct {
	items []emby.RecentlyAddedItem
	err   error
}

func (f *fakeWatcher) RecentlyAdded(_ context.Context, _ int) ([]emby.RecentlyAddedItem, error) {
	return f.items, f.err
}

func TestScanOnceRecordsAndFulfillsPendingRequest(t *testing.T) {
	originalWriter := log.Writer()
	log.SetOutput(io.Discard) // silence expected fulfillment logging
	defer log.SetOutput(originalWriter)
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	request, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar",
		Overview: "近未来地球荒芜", PosterPath: "/x.jpg", ReleaseDate: "2014-11-05",
		Kind: domain.MediaRequestKindFull, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestID := request.ID

	service := New(store, &fakeWatcher{items: []emby.RecentlyAddedItem{
		{ID: "m1", Name: "Interstellar", Type: "movie", TmdbID: 157336, DateCreated: now.Add(time.Hour)},
		{ID: "s1", Name: "The Expanse", Type: "tv", TmdbID: 46952, DateCreated: now.Add(2 * time.Hour)},
		{ID: "m2", Name: "NoDate", Type: "movie", TmdbID: 99999}, // zero DateCreated falls back to scan time
	}}, "https://emby.example.com/emby")
	service.now = func() time.Time { return now.Add(3 * time.Hour) }

	if err := service.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}

	after, err := store.FindMediaRequestByTmdb(ctx, 157336, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.MediaRequestFulfilled {
		t.Fatalf("matched request status = %q, want fulfilled", after.Status)
	}
	if requestID == 0 {
		t.Fatal("test setup error: request ID must be positive")
	}

	items, err := store.ListRecentlyAdded(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("stored items = %d, want 3", len(items))
	}
	// Newest first: NoDate anchored to the scan time (now+3h) beats both.
	if items[0].EmbyItemID != "m2" || !items[0].DateCreated.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("fallback-ordered item = %+v", items[0])
	}
}

func TestScanOncePropagatesFetchErrors(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store, &fakeWatcher{err: errors.New("emby down")}, "https://emby.example.com/emby")
	if err := service.ScanOnce(context.Background()); err == nil {
		t.Fatal("ScanOnce must propagate watcher errors")
	}
}

func TestRecentBuildsPosterURLs(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "item/1", TmdbID: 157336, MediaType: "movie", Title: "星际穿越",
		DateCreated: now, Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	service := New(store, &fakeWatcher{}, "https://emby.example.com/emby/")
	items, err := service.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	wantURL := "https://emby.example.com/emby/Items/item%2F1/Images/Primary?maxHeight=438&maxWidth=292"
	if items[0].ImageURL != wantURL {
		t.Fatalf("ImageURL = %q, want %q", items[0].ImageURL, wantURL)
	}
	if items[0].Title != "星际穿越" || items[0].MediaType != "movie" {
		t.Fatalf("item = %+v", items[0])
	}
}
