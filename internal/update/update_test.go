package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCompareVersionsOrdersBuildTags(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.0.0-build.1", "v0.0.0-build.2", -1},
		{"v0.0.0-build.999", "v0.0.0-build.2", 1},
		{"v0.0.0-build.42", "v0.0.0-build.42", 0},
		{"v1.2.3", "v0.0.0-build.999999", 1},
		{"v0.9.0", "v0.10.0", -1},
		{"dev", "v0.0.0-build.1", -1},
		{"", "v1.0.0", -1},
		{"1.0.0", "v1.0.0", 0},
	}
	for _, tc := range cases {
		got := compareVersions(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseVersionRejectsGarbage(t *testing.T) {
	for _, value := range []string{"", "dev", "v", "v1", "v1.x.3", "v1.2.3-beta.1", "v1.2.3-build", "v1.2.3-build.x"} {
		if _, ok := parseVersion(value); ok {
			t.Errorf("parseVersion(%q) unexpectedly accepted", value)
		}
	}
}

func TestAssetNameFor(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "emby-service-portal-linux-amd64"},
		{"windows", "amd64", "emby-service-portal-windows-amd64.exe"},
		{"darwin", "amd64", ""},
		{"linux", "arm64", ""},
	}
	for _, tc := range cases {
		if got := assetNameFor(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("assetNameFor(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	content := []byte("the new binary bytes")
	ok := fmt.Sprintf("%x  emby-service-portal-linux-amd64\n", sha256.Sum256(content))
	if err := verifyChecksum(strings.Fields(ok)[0], strings.NewReader(string(content))); err != nil {
		t.Fatalf("matching checksum rejected: %v", err)
	}
	if err := verifyChecksum(strings.Repeat("00", 32), strings.NewReader(string(content))); err == nil {
		t.Fatal("mismatching checksum accepted")
	}
	if err := verifyChecksum("zz", strings.NewReader(string(content))); err == nil {
		t.Fatal("malformed checksum accepted")
	}
}

// fakeStore is a minimal SettingStore for tests.
type fakeStore struct {
	mu    sync.Mutex
	items map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{items: map[string]string{}} }

func (f *fakeStore) Setting(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.items[key]
	return value, ok, nil
}

func (f *fakeStore) SetSetting(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[key] = value
	return nil
}

// testServer serves a GitHub-style releases feed plus the two assets.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	assetName := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if assetName == "" {
		t.Skipf("platform %s/%s has no asset", runtime.GOOS, runtime.GOARCH)
	}
	payload := []byte("fake-binary-content")
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), assetName)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			type asset struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
				Size               int64  `json:"size"`
			}
			type release struct {
				TagName     string  `json:"tag_name"`
				Prerelease  bool    `json:"prerelease"`
				Draft       bool    `json:"draft"`
				PublishedAt string  `json:"published_at"`
				Body        string  `json:"body"`
				Assets      []asset `json:"assets"`
			}
			releaseURL := server.URL + "/releases/download/v0.0.0-build.2/" + assetName
			checkURL := server.URL + "/releases/download/v0.0.0-build.2/" + assetName + ".sha256"
			_ = json.NewEncoder(w).Encode([]release{
				// Newest is a draft and must be skipped.
				{TagName: "v0.0.0-build.3", Draft: true},
				{TagName: "v0.0.0-build.2", PublishedAt: "2026-08-01T00:00:00Z", Body: "second release",
					Assets: []asset{{Name: assetName, BrowserDownloadURL: releaseURL, Size: int64(len(payload))},
						{Name: assetName + ".sha256", BrowserDownloadURL: checkURL}}},
				{TagName: "v0.0.0-build.1", Prerelease: true},
			})
		case strings.Contains(r.URL.Path, assetName+".sha256"):
			w.Write([]byte(checksum))
		case strings.Contains(r.URL.Path, "/releases/download/"):
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestFetchLatestPicksNewestNonDraftAndChecksum(t *testing.T) {
	server := testServer(t)
	service := New(newFakeStore(), Options{APIBase: server.URL})
	release, err := service.fetchLatest(context.Background())
	if err != nil {
		t.Fatalf("fetchLatest: %v", err)
	}
	if release.Version != "v0.0.0-build.2" {
		t.Fatalf("Version = %q, want v0.0.0-build.2", release.Version)
	}
	if release.Checksum == "" || len(release.Checksum) != 64 {
		t.Fatalf("checksum not extracted: %q", release.Checksum)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("fake-binary-content")))
	if release.Checksum != want {
		t.Fatalf("checksum = %q, want %q", release.Checksum, want)
	}
}

func TestProxyRoutesRequestsThroughProxy(t *testing.T) {
	assetName := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if assetName == "" {
		t.Skipf("platform %s/%s has no asset", runtime.GOOS, runtime.GOARCH)
	}
	payload := []byte("fake-binary-content")
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), assetName)
	downloadBase := "http://github.invalid/repos/Rst307/emby-service-portal/releases/download/v0.0.0-build.2"

	var mu sync.Mutex
	var hits []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forward-proxy semantics: the request line carries the absolute URL
		// and Host is the intended target, which stays unreachable without
		// the proxy.
		mu.Lock()
		hits = append(hits, r.URL.String())
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			type asset struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
				Size               int64  `json:"size"`
			}
			type release struct {
				TagName     string  `json:"tag_name"`
				Prerelease  bool    `json:"prerelease"`
				Draft       bool    `json:"draft"`
				PublishedAt string  `json:"published_at"`
				Body        string  `json:"body"`
				Assets      []asset `json:"assets"`
			}
			_ = json.NewEncoder(w).Encode([]release{{
				TagName: "v0.0.0-build.2", PublishedAt: "2026-08-01T00:00:00Z", Body: "second release",
				Assets: []asset{{
					Name: assetName, BrowserDownloadURL: downloadBase + "/" + assetName, Size: int64(len(payload)),
				}, {
					Name: assetName + ".sha256", BrowserDownloadURL: downloadBase + "/" + assetName + ".sha256",
				}},
			}})
		case strings.HasSuffix(r.URL.Path, assetName+".sha256"):
			w.Write([]byte(checksum))
		case strings.Contains(r.URL.Path, "/releases/download/"):
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(proxy.Close)

	// APIBase is an unroutable address: only requests that actually travel
	// through the configured proxy can succeed.
	service := New(newFakeStore(), Options{
		APIBase:      "http://127.0.0.1:1",
		DownloadBase: "http://github.invalid",
		Proxy:        proxy.URL,
	})
	if err := service.Check(context.Background()); err != nil {
		t.Fatalf("Check through proxy: %v", err)
	}
	release, err := service.fetchLatest(context.Background())
	if err != nil {
		t.Fatalf("fetchLatest through proxy: %v", err)
	}
	downloaded, err := service.downloadAndVerify(context.Background(), release)
	if err != nil {
		t.Fatalf("downloadAndVerify through proxy: %v", err)
	}
	defer os.Remove(downloaded)

	mu.Lock()
	defer mu.Unlock()
	// Without the proxy every request would dial 127.0.0.1:1 and fail; the
	// successful feed/checksum/asset round trips above already prove the
	// proxy is in use. The proxy log should show that all traffic was
	// relayed for the intended targets.
	var feed, sawChecksum, asset bool
	for _, hit := range hits {
		if strings.HasSuffix(hit, "/releases?per_page=10") || strings.HasSuffix(hit, "/releases") {
			feed = true
		}
		if strings.HasSuffix(hit, assetName+".sha256") {
			sawChecksum = true
		}
		if strings.Contains(hit, "/releases/download/") && !strings.HasSuffix(hit, assetName+".sha256") {
			asset = true
		}
	}
	if !feed || !sawChecksum || !asset {
		t.Fatalf("proxy did not relay all traffic; hits = %v", hits)
	}
}

func TestCheckAndStateLifecycle(t *testing.T) {
	server := testServer(t)
	store := newFakeStore()
	service := New(store, Options{APIBase: server.URL})
	ctx := context.Background()
	if err := service.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	state := service.Snapshot(ctx)
	if !state.Available() {
		t.Fatalf("update should be available: %+v", state)
	}
	if state.ApplyBlocked() != "" {
		t.Fatalf("unexpected ApplyBlocked: %s", state.ApplyBlocked())
	}
	// A failed check clears the availability and records the error.
	broken := New(newFakeStore(), Options{APIBase: "http://127.0.0.1:1"})
	if err := broken.Check(ctx); err == nil {
		t.Fatal("Check against dead source should fail")
	}
	brokenState := broken.Snapshot(ctx)
	if brokenState.Available() || brokenState.CheckError == "" {
		t.Fatalf("broken state should not be available: %+v", brokenState)
	}
}

func TestAutoFlagPersistenceAndSeeding(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	service := New(store, Options{AutoDefault: true})
	if err := service.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if !service.Auto(ctx) {
		t.Fatal("Ensure should seed the env default")
	}
	if err := service.SetAuto(ctx, false); err != nil {
		t.Fatal(err)
	}
	if service.Auto(ctx) {
		t.Fatal("SetAuto(false) should persist")
	}
	// Seeding does not overwrite an existing value.
	service.Ensure(ctx)
	if service.Auto(ctx) {
		t.Fatal("Ensure must not overwrite stored value")
	}
}

func TestApplyBlockedReasons(t *testing.T) {
	state := State{}
	if got := state.ApplyBlocked(); got == "" {
		t.Fatal("empty state should be blocked")
	}
	state.Latest = &Release{Version: "v1.0.0", Checksum: "ok"}
	state.CheckError = "network"
	if got := state.ApplyBlocked(); got != "最近一次检测失败" {
		t.Fatalf("unexpected blocked reason: %s", got)
	}
	state.CheckError = ""
	if got := state.ApplyBlocked(); got != "" {
		t.Fatalf("available state should not be blocked: %s", got)
	}
}

// TestAvailableRequiresNewerVersion guards the regression where a successful
// check that found the current binary already up to date still reported
// 可更新 in the admin panel.
func TestAvailableRequiresNewerVersion(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		blocked string
		want    bool
	}{
		{"same release", "v1.0.0", "v1.0.0", "", false},
		{"newer release", "v1.0.0", "v1.0.1", "", true},
		{"older release never happens but is not available", "v1.0.1", "v1.0.0", "", false},
		{"same release with missing checksum still blocked", "v1.0.0", "v1.0.0", "发布缺少校验和，已阻止自动安装", false},
		{"newer release missing checksum is blocked", "v1.0.0", "v1.0.1", "发布缺少校验和，已阻止自动安装", false},
		{"dev build treats any release as newer", "dev", "v1.0.0", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := State{CurrentVersion: tc.current, Latest: &Release{Version: tc.latest, Checksum: "ok"}}
			if tc.blocked != "" {
				state.Latest.Checksum = ""
			}
			if got := state.Available(); got != tc.want {
				t.Fatalf("Available() = %v, want %v (blocked=%q)", got, tc.want, state.ApplyBlocked())
			}
		})
	}
}
