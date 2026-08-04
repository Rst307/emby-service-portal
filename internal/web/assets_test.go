package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticAssetsRequireRevalidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler func(*Server, *httptest.ResponseRecorder)
		marker  string
	}{
		{name: "script", handler: func(server *Server, response *httptest.ResponseRecorder) { server.script(response, nil) }, marker: "data-batch-status-filter"},
		{name: "stylesheet", handler: func(server *Server, response *httptest.ResponseRecorder) { server.stylesheet(response, nil) }, marker: "batch-status-tools"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handler(&Server{}, response)
			if response.Header().Get("Cache-Control") != "no-cache, must-revalidate" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
			if !strings.Contains(response.Body.String(), test.marker) {
				t.Fatalf("asset does not contain expected marker %q", test.marker)
			}
		})
	}
}
