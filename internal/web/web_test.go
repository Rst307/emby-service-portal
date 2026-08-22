package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/tmdb"
)

func TestSecurityHeadersProvideCloudflareCompatibleScriptPolicy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	securityHeaders(tmdb.PosterBaseHost(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)

	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self' 'nonce-") {
		t.Fatalf("CSP does not provide a per-response script nonce: %q", policy)
	}
	if !strings.Contains(policy, "https://static.cloudflareinsights.com") {
		t.Fatalf("CSP does not allow the Cloudflare Web Analytics script: %q", policy)
	}
	if !strings.Contains(policy, "connect-src 'self' https://cloudflareinsights.com") {
		t.Fatalf("CSP does not allow the Cloudflare analytics endpoint: %q", policy)
	}
	if !strings.Contains(policy, "img-src 'self' data: https://image.tmdb.org") {
		t.Fatalf("CSP does not allow the TMDB poster CDN: %q", policy)
	}
	if strings.Contains(policy, "emby.example.com") {
		t.Fatalf("CSP should not allow the Emby origin (images are proxied same-origin): %q", policy)
	}
	scriptPolicy := strings.SplitN(strings.SplitN(policy, "script-src ", 2)[1], ";", 2)[0]
	if strings.Contains(scriptPolicy, "'unsafe-inline'") {
		t.Fatalf("CSP weakens script policy with unsafe-inline: %q", policy)
	}
}

func TestParseAccountDateTimePreservesAmbiguousOriginalInstant(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	original := time.Date(2024, 11, 3, 6, 30, 0, 0, time.UTC) // second 01:30, after fall-back

	parsed, err := parseAccountDateTimeIn("2024-11-03T01:30", original.Format(time.RFC3339Nano), location)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(original) {
		t.Fatalf("parsed = %s, want original instant %s", parsed, original)
	}
}
