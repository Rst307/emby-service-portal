package requests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
)

// stubEmby is a deterministic ProviderLibrary for tests.
type stubEmby struct {
	present map[string]bool
}

func (s *stubEmby) AnyProviderIDExists(_ context.Context, mediaTypes []string, ids []int64) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, id := range ids {
		for _, mediaType := range mediaTypes {
			key := fmt.Sprintf("%s:%d", mediaType, id)
			if s.present[key] {
				result[key] = true
			}
		}
	}
	return result, nil
}

// tmdbStub serves canned /search/multi and /movie|tv/{id} responses. The movie
// with id movieInLibrary is returned by search and by its details endpoint so
// tests can exercise the in-library rejection path.
func tmdbStub(t *testing.T, movieInLibrary int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/multi"):
			_ = writeJSON(w, map[string]any{
				"results": []map[string]any{
					{"id": 157336, "media_type": "movie", "title": "星际穿越", "original_title": "Interstellar", "overview": "你好", "poster_path": "/a.jpg", "release_date": "2014-11-05"},
					{"id": 1399, "media_type": "tv", "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国", "poster_path": "/b.jpg", "first_air_date": "2011-04-17"},
					{"id": movieInLibrary, "media_type": "movie", "title": "已在库电影", "original_title": "InLibrary", "overview": "", "release_date": "2020-01-01"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/movie/157336"):
			_ = writeJSON(w, map[string]any{"id": 157336, "title": "星际穿越", "original_title": "Interstellar", "overview": "你好", "poster_path": "/a.jpg", "release_date": "2014-11-05"})
		case strings.HasSuffix(r.URL.Path, "/tv/1399"):
			_ = writeJSON(w, map[string]any{"id": 1399, "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国", "poster_path": "/b.jpg", "first_air_date": "2011-04-17"})
		case r.URL.Path == "/movie/"+fmt.Sprint(movieInLibrary):
			_ = writeJSON(w, map[string]any{"id": movieInLibrary, "title": "已在库电影", "original_title": "InLibrary"})
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func writeJSON(w http.ResponseWriter, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func newTestService(t *testing.T, emby *stubEmby, tmdbServer *httptest.Server) (*Service, *sqlite.Store, domain.Account) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	tmdbClient := tmdb.NewClient("test-key")
	tmdbClient.SetBaseURL(tmdbServer.URL)
	service := New(store, tmdbClient, emby)
	service.now = func() time.Time { return now }
	return service, store, account
}

func TestSearchMarksLibraryAndPreviousRequests(t *testing.T) {
	emby := &stubEmby{present: map[string]bool{"movie:11111": true}}
	server := tmdbStub(t, 11111)
	defer server.Close()
	service, _, account := newTestService(t, emby, server)
	ctx := context.Background()

	items, err := service.Search(ctx, account.ID, "星际穿越", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	byID := map[int64]SearchItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if !byID[11111].InLibrary {
		t.Fatal("movie present in Emby library must be marked InLibrary")
	}
	if byID[157336].InLibrary || byID[1399].InLibrary {
		t.Fatalf("absent titles marked InLibrary: %+v", items)
	}
	if byID[157336].AlreadyRequested {
		t.Fatal("fresh title must not be marked as already requested")
	}

	// After the account requests one of the titles, only that title is marked.
	if _, err := service.Create(ctx, account, "movie", 157336); err != nil {
		t.Fatal(err)
	}
	items2, err := service.Search(ctx, account.ID, "星际穿越", 10)
	if err != nil {
		t.Fatal(err)
	}
	var requestedMarked, otherMarked bool
	for _, item := range items2 {
		if item.ID == 157336 {
			requestedMarked = item.AlreadyRequested && item.RequestStatus == domain.MediaRequestPending
		}
		if item.ID == 1399 && item.AlreadyRequested {
			otherMarked = true
		}
	}
	if !requestedMarked {
		t.Fatal("own request must be marked AlreadyRequested with pending status")
	}
	if otherMarked {
		t.Fatal("another title must not be marked as requested")
	}
}

func TestCreateRejectsInLibraryAndReactivatesRejected(t *testing.T) {
	emby := &stubEmby{present: map[string]bool{"movie:11111": true}}
	server := tmdbStub(t, 11111)
	defer server.Close()
	service, store, account := newTestService(t, emby, server)
	ctx := context.Background()

	if _, err := service.Create(ctx, account, "movie", 11111); err != domain.ErrRequestInLibrary {
		t.Fatalf("in-library create error = %v, want ErrRequestInLibrary", err)
	}
	created, err := service.Create(ctx, account, "movie", 157336)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "星际穿越" || created.MediaType != "movie" || created.Status != domain.MediaRequestPending {
		t.Fatalf("created = %+v", created)
	}

	// A rejected request reactivates to pending on a new submission.
	if err := store.SetMediaRequestStatus(ctx, created.ID, domain.MediaRequestRejected, time.Now()); err != nil {
		t.Fatal(err)
	}
	reactivated, err := service.Create(ctx, account, "movie", 157336)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.ID != created.ID || reactivated.Status != domain.MediaRequestPending {
		t.Fatalf("reactivated = %+v, want same id with pending status", reactivated)
	}

	// Unsupported media types and missing TMDB records are rejected server-side.
	if _, err := service.Create(ctx, account, "person", 157336); err == nil {
		t.Fatal("unsupported media type must fail")
	}
	if _, err := service.Create(ctx, account, "tv", 9999999); err == nil {
		t.Fatal("missing TMDB record must fail")
	}
}

func TestServiceNotConfiguredWithoutTMDBKey(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store, tmdb.NewClient(""), &stubEmby{present: map[string]bool{}})
	if _, err := service.Search(context.Background(), 1, "anything", 5); err != tmdb.ErrNotConfigured {
		t.Fatalf("search error = %v, want ErrNotConfigured", err)
	}
	if _, err := service.Create(context.Background(), domain.Account{ID: 1, Username: "u"}, "movie", 1); err != tmdb.ErrNotConfigured {
		t.Fatalf("create error = %v, want ErrNotConfigured", err)
	}
}
