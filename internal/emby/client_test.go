package emby

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
