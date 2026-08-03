package admin

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderDoesNotCommitPartialTemplateOnExecutionFailure(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}partial {{.Missing}}{{end}}`))}
	response := httptest.NewRecorder()
	templates.Render(response, "page", ViewData{})

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "partial") {
		t.Fatalf("response leaked partial template: %q", response.Body.String())
	}
}
