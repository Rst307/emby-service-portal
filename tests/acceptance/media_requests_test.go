package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/app"
	"github.com/Rst307/emby-service-portal/internal/config"
)

// TestMediaRequestFlow covers the full 求剧 journey: portal search marks the
// live Emby library and the account's own requests, submission records the
// request, and the administrator sees and fulfills it.
func TestMediaRequestFlow(t *testing.T) {
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/multi"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": 11111, "media_type": "movie", "title": "已在库电影", "original_title": "InLibrary", "release_date": "2020-01-01"},
					{"id": 1399, "media_type": "tv", "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国纷争", "poster_path": "/g.jpg", "first_air_date": "2011-04-17"},
					{"id": 157336, "media_type": "movie", "title": "星际穿越", "original_title": "Interstellar", "overview": "地球荒芜", "poster_path": "/i.jpg", "release_date": "2014-11-05"},
				},
			})
		case r.URL.Path == "/tv/1399":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1399, "name": "权力的游戏", "original_name": "Game of Thrones", "overview": "七国纷争", "poster_path": "/g.jpg", "first_air_date": "2011-04-17"})
		case r.URL.Path == "/movie/157336":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 157336, "title": "星际穿越", "original_title": "Interstellar", "overview": "地球荒芜", "poster_path": "/i.jpg", "release_date": "2014-11-05"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tmdb.Close)

	emby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/AuthenticateByName":
			var credentials struct {
				Username string `json:"Username"`
				Password string `json:"Pw"`
			}
			_ = json.NewDecoder(r.Body).Decode(&credentials)
			if credentials.Username != "portal-user" || credentials.Password != "password123" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"User": map[string]string{"Id": "emby-portal-user", "Name": credentials.Username}})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/New":
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "emby-portal-user", "Name": "portal-user"})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/emby-portal-user/Password":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/emby/Users/emby-portal-user":
			_ = json.NewEncoder(w).Encode(map[string]any{"Policy": map[string]any{"IsDisabled": false, "IsAdministrator": false}})
		case r.Method == http.MethodPost && r.URL.Path == "/emby/Users/emby-portal-user/Policy":
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
		TmdbAPIKey: "test-tmdb-key", TmdbBaseURL: tmdb.URL,
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	response := postJSON(t, http.DefaultClient, server.URL+"/api/v1/accounts", `{"username":"portal-user","password":"password123","expires_at":"2030-01-01T00:00:00Z"}`, "integration-key")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create portal account status = %d: %s", response.StatusCode, body(t, response))
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response = get(t, client, server.URL+"/portal/login")
	response = postForm(t, client, server.URL+"/portal/login", url.Values{"username": {"portal-user"}, "password": {"password123"}, "csrf_token": {csrf(t, body(t, response))}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("portal login status = %d", response.StatusCode)
	}

	// Search marks the in-library title and offers request buttons for the rest.
	response = get(t, client, server.URL+"/portal/request?q=%E6%9D%83%E5%8A%9B")
	searchPage := body(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("request page status = %d", response.StatusCode)
	}
	for _, marker := range []string{"搜索影视", "已在库电影", "权力的游戏", "星际穿越", "已在库", `value="1399"`, `value="157336"`, "type=\"submit\">求剧"} {
		if !strings.Contains(searchPage, marker) {
			t.Fatalf("request search page missing %q: %s", marker, searchPage)
		}
	}

	// Submit a request for the TV show (not in the library).
	response = postForm(t, client, server.URL+"/portal/request", url.Values{
		"csrf_token": {csrf(t, searchPage)}, "media_type": {"tv"}, "tmdb_id": {"1399"},
	})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("request submit status = %d: %s", response.StatusCode, body(t, response))
	}

	// The own request now appears instead of the request button.
	response = get(t, client, server.URL+"/portal/request?q=%E6%9D%83%E5%8A%9B")
	searchPage = body(t, response)
	if !strings.Contains(searchPage, "已求剧") {
		t.Fatalf("own request not marked on search page: %s", searchPage)
	}

	// The portal now shows the own request history with an 未处理 badge.
	response = get(t, client, server.URL+"/portal/request")
	historyPage := body(t, response)
	for _, marker := range []string{"我的求剧记录", "权力的游戏", "未处理", "1 条"} {
		if !strings.Contains(historyPage, marker) {
			t.Fatalf("portal history missing %q: %s", marker, historyPage)
		}
	}

	// Administrator sees the request with user, title and TMDB id.
	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, adminClient, server.URL)
	response = get(t, adminClient, server.URL+"/admin/requests")
	requestsPage := body(t, response)
	for _, marker := range []string{"求剧管理", "权力的游戏", "portal-user", "#1399", "待处理", "标记已入库"} {
		if !strings.Contains(requestsPage, marker) {
			t.Fatalf("admin requests page missing %q: %s", marker, requestsPage)
		}
	}
	match := regexp.MustCompile(`/admin/requests/(\d+)/fulfill`).FindStringSubmatch(requestsPage)
	if match == nil {
		t.Fatalf("request fulfill action missing: %s", requestsPage)
	}
	requestID := match[1]

	// Fulfill the request; the record is marked 已入库.
	response = postForm(t, adminClient, server.URL+"/admin/requests/"+requestID+"/fulfill", url.Values{"csrf_token": {csrf(t, requestsPage)}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("fulfill status = %d: %s", response.StatusCode, body(t, response))
	}
	response = get(t, adminClient, server.URL+"/admin/requests")
	if !strings.Contains(body(t, response), "已入库") {
		t.Fatalf("fulfilled request not marked: %s", body(t, response))
	}

	// The portal history reflects the fulfilled status.
	response = get(t, client, server.URL+"/portal/request")
	afterPage := body(t, response)
	if !strings.Contains(afterPage, "已处理") {
		t.Fatalf("portal history not marked fulfilled: %s", afterPage)
	}
}
