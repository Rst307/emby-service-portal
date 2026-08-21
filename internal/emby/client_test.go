package emby

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDeleteUserTreatsMissingUserAsSuccessWhenEmbyDeleteReturnsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, "user not found", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteUser(context.Background(), "already-gone"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("DeleteUser error = %v, want missing-user error", err)
	}
}
func TestFindUserByUsernameUsesNarrowExactLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/Users" || r.URL.Query().Get("Name") != "alice" {
			t.Fatalf("unexpected lookup request: %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode([]User{{ID: "u1", Username: "Alice"}, {ID: "u2", Username: "other"}})
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	user, err := client.FindUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "u1" {
		t.Fatalf("found user = %#v", user)
	}
}

func TestCreateUserSetsPasswordWithDedicatedEndpoint(t *testing.T) {
	passwordSet := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/New":
			var input struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.Name != "alice" {
				t.Fatalf("name=%q", input.Name)
			}
			_ = json.NewEncoder(w).Encode(User{ID: "u1", Username: "alice"})
		case "/Users/u1/Password":
			var input struct {
				CurrentPw string
				NewPw     string
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.CurrentPw != "" || input.NewPw != "password123" {
				t.Fatalf("bad password request: %#v", input)
			}
			passwordSet = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL, "key")
	if _, err := client.CreateUser(context.Background(), "alice", "password123"); err != nil {
		t.Fatal(err)
	}
	if !passwordSet {
		t.Fatal("password endpoint was not called")
	}
}

func TestAnyProviderIDExistsUsesEmbyDotSyntaxAndItemTypeNames(t *testing.T) {
	var sawQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		sawQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		// Library has tmdb 155 as a Movie and tmdb 1399 as a Series.
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "m1", "Type": "Movie", "ProviderIds": map[string]any{"Tmdb": "155"}},
			{"Id": "s1", "Type": "Series", "ProviderIds": map[string]any{"Tmdb": "1399"}},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}

	// Empirically fails: a colon form ("tmdb:155") matches nothing on Emby, so
	// the query must be built with the dot form "tmdb.155".
	result, err := client.AnyProviderIDExists(context.Background(), []string{"movie", "tv"}, []int64{155, 1399, 99999})
	if err != nil {
		t.Fatal(err)
	}
	if got := sawQuery.Get("AnyProviderIdEquals"); got != "tmdb.155,tmdb.1399,tmdb.99999" {
		t.Fatalf("AnyProviderIdEquals = %q, want dot-separated tmdb.<id> values", got)
	}
	if got := sawQuery.Get("IncludeItemTypes"); got != "Movie,Series" {
		t.Fatalf("IncludeItemTypes = %q, want Emby type names Movie,Series", got)
	}
	if !result["movie:155"] {
		t.Fatalf("movie:155 should be marked present, got %v", result)
	}
	if !result["tv:1399"] {
		t.Fatalf("tv:1399 should be marked present, got %v", result)
	}
	if result["movie:99999"] || result["tv:99999"] {
		t.Fatalf("absent tmdb 99999 must not be marked")
	}
}
