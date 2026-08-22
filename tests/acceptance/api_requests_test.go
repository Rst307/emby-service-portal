package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/app"
	"github.com/Rst307/emby-service-portal/internal/config"
)

// TestMediaRequestAPI covers the workflow-facing REST surface of 求剧:
// list with filters, workflow submission (TMDB re-fetch + Emby in-library
// guard, aggregation per title), and status transitions that close the loop.
func TestMediaRequestAPI(t *testing.T) {
	tmdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/movie/157336":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 157336, "title": "星际穿越", "original_title": "Interstellar", "overview": "地球荒芜", "poster_path": "/i.jpg", "release_date": "2014-11-05"})
		case "/movie/11111":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 11111, "title": "已在库电影", "original_title": "InLibrary", "release_date": "2020-01-01"})
		case "/tv/1399":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1399, "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国纷争", "poster_path": "/g.jpg", "first_air_date": "2011-04-17"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tmdbServer.Close)

	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/New":
			var input struct {
				Name string `json:"Name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "emby-" + input.Name, "Name": input.Name})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/Password"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/emby/Users/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Policy": map[string]any{"IsDisabled": false, "IsAdministrator": false}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/Policy"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/emby/Items":
			// Library inventory: TMDB 11111 exists as a movie.
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Type": "Movie", "ProviderIds": map[string]string{"Tmdb": "11111"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(emby.Close)

	application, err := app.New(context.Background(), config.Config{
		ListenAddr: ":0", DatabasePath: filepath.Join(t.TempDir(), "manager.db"),
		EmbyBaseURL: emby.URL + "/emby", EmbyAPIKey: "test-key", APIKey: "integration-key",
		CredentialMasterKey: "test-credential-master-key-that-is-long-enough",
		AdminUsername:       "admin", AdminPassword: "correct horse battery staple",
		CookieSecure: false, SessionTTL: time.Hour, TimeZone: "UTC",
		TmdbAPIKey: "test-tmdb-key", TmdbBaseURL: tmdbServer.URL,
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	client := http.DefaultClient
	base := server.URL

	createAccount := func(username string) int64 {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, base+"/api/v1/accounts", strings.NewReader(`{"username":"`+username+`","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-API-Key", "integration-key")
		request.Header.Set("Idempotency-Key", "acceptance-"+username)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create account %s status = %d: %s", username, response.StatusCode, body(t, response))
		}
		var account struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(body(t, response)), &account); err != nil {
			t.Fatal(err)
		}
		return account.ID
	}
	aliceID := createAccount("api-alice")
	bobID := createAccount("api-bob")

	t.Run("rejects missing API key", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodGet, base+"/api/v1/requests", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.StatusCode)
		}
		response.Body.Close()
	})

	t.Run("creates and aggregates requests by title", func(t *testing.T) {
		response := postJSON(t, client, base+"/api/v1/requests", `{"media_type":"movie","tmdb_id":157336,"account_id":`+fmt.Sprint(aliceID)+`}`, "integration-key")
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", response.StatusCode, body(t, response))
		}
		created := decodeRequest(t, body(t, response))
		if created.Title != "星际穿越" || created.MediaType != "movie" || created.TmdbID != 157336 ||
			created.Status != "pending" || created.Kind != "full" {
			t.Fatalf("unexpected created request: %+v", created)
		}
		if created.RequesterCount != 1 || created.Requesters[0].AccountUsername != "api-alice" {
			t.Fatalf("created requesters = %+v", created.Requesters)
		}

		// A second user asking for the same title joins the same row.
		joined := postJSON(t, client, base+"/api/v1/requests", `{"media_type":"movie","tmdb_id":157336,"account_id":`+fmt.Sprint(bobID)+`}`, "integration-key")
		if joined.StatusCode != http.StatusCreated {
			t.Fatalf("join status = %d, body = %s", joined.StatusCode, body(t, joined))
		}
		joinedRequest := decodeRequest(t, body(t, joined))
		if joinedRequest.ID != created.ID || joinedRequest.RequesterCount != 2 ||
			joinedRequest.Requesters[1].AccountUsername != "api-bob" {
			t.Fatalf("joined request = %+v", joinedRequest)
		}

		// A workflow submission without account_id records no requester.
		response = postJSON(t, client, base+"/api/v1/requests", `{"media_type":"tv","tmdb_id":1399}`, "integration-key")
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("tv status = %d, body = %s", response.StatusCode, body(t, response))
		}
		anonymous := decodeRequest(t, body(t, response))
		if anonymous.RequesterCount != 0 || len(anonymous.Requesters) != 0 {
			t.Fatalf("anonymous request should have no requesters: %+v", anonymous)
		}
	})

	t.Run("rejects invalid input and catalog/library guards", func(t *testing.T) {
		response := postJSON(t, client, base+"/api/v1/requests", `{"media_type":"music","tmdb_id":1}`, "integration-key")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad media_type status = %d", response.StatusCode)
		}
		response = postJSON(t, client, base+"/api/v1/requests", `{"media_type":"movie","tmdb_id":99999}`, "integration-key")
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown tmdb status = %d, body = %s", response.StatusCode, body(t, response))
		}
		response = postJSON(t, client, base+"/api/v1/requests", `{"media_type":"movie","tmdb_id":11111}`, "integration-key")
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("in-library status = %d, want 409, body = %s", response.StatusCode, body(t, response))
		}
		response = postJSON(t, client, base+"/api/v1/requests", `{"media_type":"movie","tmdb_id":157336,"account_id":424242}`, "integration-key")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown account status = %d, want 400, body = %s", response.StatusCode, body(t, response))
		}
	})

	t.Run("lists with filters and aggregated requesters", func(t *testing.T) {
		page := decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests", "integration-key")))
		if page.Total != 2 || page.Pending != 2 || page.TotalPages != 1 {
			t.Fatalf("full list = %+v", page)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?tmdb_id=157336", "integration-key")))
		if page.Total != 1 || len(page.Requests) != 1 || page.Requests[0].RequesterCount != 2 ||
			len(page.Requests[0].Requesters) != 2 || page.Requests[0].Requesters[1].AccountUsername != "api-bob" {
			t.Fatalf("aggregated list = %+v", page)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?status=fulfilled", "integration-key")))
		if page.Total != 0 {
			t.Fatalf("fulfilled filter = %+v", page)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?tmdb_id=1399", "integration-key")))
		if page.Total != 1 || page.Requests[0].RequesterCount != 0 {
			t.Fatalf("workflow request = %+v", page)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?q=api-bob", "integration-key")))
		if page.Total != 1 || page.Requests[0].TmdbID != 157336 {
			t.Fatalf("requester keyword filter = %+v", page)
		}
	})

	t.Run("fulfill closes the loop and reject works", func(t *testing.T) {
		page := decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?tmdb_id=1399", "integration-key")))
		tvID := page.Requests[0].ID
		response := postJSON(t, client, fmt.Sprintf("%s/api/v1/requests/%d/reject", base, tvID), "", "integration-key")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("reject status = %d, body = %s", response.StatusCode, body(t, response))
		}
		var outcome struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(body(t, response)), &outcome); err != nil || outcome.Status != "rejected" {
			t.Fatalf("reject outcome = %+v, err = %v", outcome, err)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?tmdb_id=157336", "integration-key")))
		movieID := page.Requests[0].ID
		response = postJSON(t, client, fmt.Sprintf("%s/api/v1/requests/%d/fulfill", base, movieID), "", "integration-key")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("fulfill status = %d, body = %s", response.StatusCode, body(t, response))
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?status=fulfilled", "integration-key")))
		if page.Total != 1 || page.Requests[0].TmdbID != 157336 {
			t.Fatalf("fulfilled list = %+v", page)
		}
		page = decodePage(t, body(t, getWithKey(t, client, base+"/api/v1/requests?status=rejected", "integration-key")))
		if page.Total != 1 || page.Requests[0].TmdbID != 1399 {
			t.Fatalf("rejected list = %+v", page)
		}
		response = postJSON(t, client, base+"/api/v1/requests/99999/fulfill", "", "integration-key")
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("missing id status = %d, want 404", response.StatusCode)
		}
	})
}

type apiRequester struct {
	AccountID       int64  `json:"account_id"`
	AccountUsername string `json:"account_username"`
}

type apiRequest struct {
	ID             int64          `json:"id"`
	Requesters     []apiRequester `json:"requesters"`
	RequesterCount int            `json:"requester_count"`
	TmdbID         int64          `json:"tmdb_id"`
	MediaType      string         `json:"media_type"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Kind           string         `json:"kind"`
}

type apiRequestPage struct {
	Requests   []apiRequest `json:"requests"`
	Total      int          `json:"total"`
	Pending    int          `json:"pending"`
	Fulfilled  int          `json:"fulfilled"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
	TotalPages int          `json:"total_pages"`
}

func decodeRequest(t *testing.T, raw string) apiRequest {
	t.Helper()
	var request apiRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatalf("decode request %q: %v", raw, err)
	}
	return request
}

func decodePage(t *testing.T, raw string) apiRequestPage {
	t.Helper()
	var page apiRequestPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode page %q: %v", raw, err)
	}
	return page
}
