package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/accounts"
	"github.com/Rst307/emby-service-portal/internal/auth"
	"github.com/Rst307/emby-service-portal/internal/credentials"
	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/invites"
	"github.com/Rst307/emby-service-portal/internal/paymentcenter"
	"github.com/Rst307/emby-service-portal/internal/payments"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/portal"
	"github.com/Rst307/emby-service-portal/internal/recent"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/settings"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
	"github.com/Rst307/emby-service-portal/internal/update"
)

// stubEmby satisfies the narrow Emby surfaces the web test wiring needs. User
// operations are never exercised by the portal dashboard path, so everything
// panics rather than silently succeeding.
type stubEmby struct{}

func (stubEmby) AuthenticateUser(context.Context, string, string) (emby.User, error) {
	panic("unexpected authentication")
}
func (stubEmby) AnyProviderIDExists(context.Context, []string, []int64) (map[string]bool, error) {
	return nil, nil
}
func (stubEmby) SeriesEpisodes(context.Context, int64) (emby.SeasonEpisodes, bool, error) {
	return emby.SeasonEpisodes{}, false, nil
}
func (stubEmby) RecentlyAdded(context.Context, int) ([]emby.RecentlyAddedItem, error) {
	return nil, nil
}
func (stubEmby) ItemPoster(_ context.Context, itemID string, _, _ int) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("poster-bytes-" + itemID)), "image/jpeg", nil
}

// The remaining emby.Client methods are never exercised by these tests.
func (stubEmby) CreateUser(context.Context, string, string) (emby.User, error) {
	panic("unexpected user creation")
}
func (stubEmby) DeleteUser(context.Context, string) error {
	panic("unexpected user deletion")
}
func (stubEmby) SetUserDisabled(context.Context, string, bool) error {
	panic("unexpected policy update")
}

// testPortalServer wires the same module composition the app uses, with a
// temporary database and stub Emby, so portal pages can be exercised end to
// end (session lookup + data loading + template rendering).
func testPortalServer(t *testing.T, ctx context.Context) (*Server, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(ctx, t.TempDir()+"/portal.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	authService := auth.New(store, time.Hour)
	embyStub := stubEmby{}
	vault := credentials.New("master-key-0123456789abcdef0123456789abcdef", "", "api-key")
	accountService := accounts.New(store, embyStub, vault)
	inviteService := invites.New(store, accountService)
	paymentService := payments.New(store, accountService, vault, paymentcenter.NewClient(nil))
	portalService := portal.New(store, embyStub, time.Hour)
	tmdbClient := tmdb.NewClient("")
	requestService := requests.New(store, tmdbClient, embyStub)
	recentService := recent.New(store, embyStub)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	settingsService := settings.New(store, location)
	if err := settingsService.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	updateService := update.New(store, update.Options{})
	server, err := New(authService, portalService, accountService, inviteService, paymentService, settingsService, requestService, recentService, tmdbClient, updateService, "api-key", false, time.Hour, location)
	if err != nil {
		t.Fatal(err)
	}
	return server, store
}

// portalTokenHash mirrors the portal package's session-token hashing so a test
// can create a session the dashboard will recognize.
func portalTokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestPortalDashboardShowsRecentlyAddedFeed(t *testing.T) {
	ctx := context.Background()
	server, store := testPortalServer(t, ctx)
	now := time.Now().UTC()

	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-alice", Username: "alice", Status: "active",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "dummy-session-token"
	if err := store.CreateUserSession(ctx, domain.UserSession{
		ID: "session-1", AccountID: account.ID, TokenHash: portalTokenHash(token),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// One addition without a request link, one that fulfilled a 求剧.
	if _, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "m-new", TmdbID: 999001, MediaType: "movie", Title: "Fresh Pick",
		DateCreated: now.Add(-time.Hour), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar",
		Kind: domain.MediaRequestKindFull, Now: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "m-requested", TmdbID: 157336, MediaType: "movie", Title: "星际穿越",
		DateCreated: now.Add(-time.Hour), Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/", nil)
	req.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	body := resp.Body.String()
	for _, want := range []string{
		"最近更新",
		"Fresh Pick",
		"星际穿越",
		"求剧已入库",
		"src=\"/img/emby/m-new\"",
		"data-scroll-row",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q", want)
		}
	}
}

func TestPortalDashboardRedirectsAnonymousVisitors(t *testing.T) {
	ctx := context.Background()
	server, _ := testPortalServer(t, ctx)
	req := httptest.NewRequest(http.MethodGet, "/portal/", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", resp.Code)
	}
}

func TestRecentImageProxyRequiresSessionAndStreamsPoster(t *testing.T) {
	ctx := context.Background()
	server, store := testPortalServer(t, ctx)
	now := time.Now().UTC()

	// Anonymous visitors must not be able to pull proxied posters.
	anonymous := httptest.NewRequest(http.MethodGet, "/img/emby/m-new", nil)
	anonymousResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(anonymousResp, anonymous)
	if anonymousResp.Code != http.StatusNotFound {
		t.Fatalf("anonymous status = %d, want 404", anonymousResp.Code)
	}

	account, err := store.CreateAccount(ctx, domain.Account{
		EmbyUserID: "emby-bob", Username: "bob", Status: "active",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "poster-session-token"
	if err := store.CreateUserSession(ctx, domain.UserSession{
		ID: "session-poster", AccountID: account.ID, TokenHash: portalTokenHash(token),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/img/emby/m-new", nil)
	req.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if got := resp.Body.String(); got != "poster-bytes-m-new" {
		t.Fatalf("body = %q, want proxied poster bytes", got)
	}
	if contentType := resp.Header().Get("Content-Type"); contentType != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", contentType)
	}
	if cache := resp.Header().Get("Cache-Control"); cache == "" {
		t.Fatal("missing Cache-Control on proxied poster")
	}
}
