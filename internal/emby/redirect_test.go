package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	client, err := NewHTTPClient(source.URL, "sensitive-emby-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListUsers(context.Background()); err == nil {
		t.Fatal("redirect response must not be accepted as an Emby result")
	}
	if redirected.Load() {
		t.Fatal("HTTP client followed a redirect to another origin")
	}
}
