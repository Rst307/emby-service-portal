package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchMultiParsesMoviesAndSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/multi" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("api_key") != "test-key" || r.URL.Query().Get("query") != "星际穿越" {
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("language") != "zh-CN" {
			t.Fatalf("search language is not zh-CN: %s", r.URL.Query().Get("language"))
		}
		payload := map[string]any{
			"page": 1, "total_pages": 1, "total_results": 3,
			"results": []map[string]any{
				{"id": 157336, "media_type": "movie", "title": "星际穿越", "original_title": "Interstellar", "overview": "近未来地球荒芜", "poster_path": "/nCbkOyOMTEgEVqWOid1tom2zPEm.jpg", "release_date": "2014-11-05"},
				{"id": 1399, "media_type": "tv", "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国纷争", "poster_path": "/7WUHnWGx5G2S2U8e8GZ2w1z0u9k.jpg", "first_air_date": "2011-04-17"},
				{"id": 123, "media_type": "person", "name": "克里斯托弗·诺兰", "original_name": "Christopher Nolan"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL
	results, err := client.SearchMulti(context.Background(), "星际穿越", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (person filtered out)", len(results))
	}
	movie := results[0]
	if movie.ID != 157336 || movie.MediaType != "movie" || movie.Title != "星际穿越" || movie.OriginalTitle != "Interstellar" || movie.ReleaseDate != "2014-11-05" {
		t.Fatalf("movie = %+v", movie)
	}
	series := results[1]
	if series.ID != 1399 || series.MediaType != "tv" || series.Title != "权力的游戏" || series.OriginalTitle != "Game of Thrones" || series.ReleaseDate != "2011-04-17" {
		t.Fatalf("series = %+v", series)
	}
}

func TestSearchMultiRespectsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{
			"results": []map[string]any{
				{"id": 1, "media_type": "movie", "title": "A"},
				{"id": 2, "media_type": "movie", "title": "B"},
				{"id": 3, "media_type": "movie", "title": "C"},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL
	results, err := client.SearchMulti(context.Background(), "abc", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
}

func TestSearchMultiRejectsUnconfiguredClient(t *testing.T) {
	client := NewClient("")
	if client.Configured() {
		t.Fatal("empty key must leave the client unconfigured")
	}
	if _, err := client.SearchMulti(context.Background(), "anything", 5); err != ErrNotConfigured {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestDetailsReturnsNotFoundAsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/9999999" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL
	result, found, err := client.Details(context.Background(), "movie", 9999999)
	if err != nil {
		t.Fatal(err)
	}
	if found || result.ID != 0 {
		t.Fatalf("result = %+v, found = %v; want missing", result, found)
	}
}

func TestPosterURL(t *testing.T) {
	if PosterURL("") != "" {
		t.Fatal("empty poster path must produce empty URL")
	}
	if PosterURL("/abc.jpg") != "https://image.tmdb.org/t/p/w342/abc.jpg" {
		t.Fatalf("unexpected poster URL %q", PosterURL("/abc.jpg"))
	}
}

func TestPosterBaseURLOverridable(t *testing.T) {
	defer SetPosterBaseURL("https://image.tmdb.org/t/p/w342")

	SetPosterBaseURL("https://img.mirror.example.com/t/p/w342")
	if got := PosterURL("/x.jpg"); got != "https://img.mirror.example.com/t/p/w342/x.jpg" {
		t.Fatalf("overridden poster URL = %q", got)
	}
	if got := PosterBaseHost(); got != "https://img.mirror.example.com" {
		t.Fatalf("PosterBaseHost = %q, want mirror host", got)
	}
}

func TestSetProxyRejectsUnsupportedScheme(t *testing.T) {
	client := NewClient("test-key")
	if err := client.SetProxy("ftp://proxy.example.com:21"); err == nil {
		t.Fatal("ftp proxy must be rejected")
	}
	if err := client.SetProxy("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("socks5 proxy must be accepted: %v", err)
	}
}

func TestSetTimeoutPositiveOnly(t *testing.T) {
	client := NewClient("test-key")
	client.SetTimeout(0)
	if client.timeout != defaultTimeout {
		t.Fatalf("zero timeout must keep default, got %v", client.timeout)
	}
	client.SetTimeout(30 * time.Second)
	if client.timeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", client.timeout)
	}
}

func TestSearchMultiFallsBackToOfficialWhenMirrorFails(t *testing.T) {
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/multi" {
			t.Fatalf("unexpected mirror path %q", r.URL.Path)
		}
		http.Error(w, "upstream error", http.StatusInternalServerError)
	}))
	defer mirror.Close()

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/multi" {
			t.Fatalf("unexpected official path %q", r.URL.Path)
		}
		payload := map[string]any{
			"results": []map[string]any{
				{"id": 157336, "media_type": "movie", "title": "星际穿越", "poster_path": "/x.jpg"},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer official.Close()

	client := NewClient("test-key")
	client.SetBaseURL(mirror.URL)
	client.fallbackBaseURL = official.URL

	results, err := client.SearchMulti(context.Background(), "星际穿越", 5)
	if err != nil {
		t.Fatalf("search should succeed via official fallback: %v", err)
	}
	if len(results) != 1 || results[0].ID != 157336 {
		t.Fatalf("results = %+v, want the official record", results)
	}
}

func TestNoFallbackWithoutMirrorOverride(t *testing.T) {
	client := NewClient("test-key")
	if got := client.fallbackEndpoint("https://api.themoviedb.org/3/search/multi?x=1"); got != "" {
		t.Fatalf("fallback must be empty without a mirror override, got %q", got)
	}
}
