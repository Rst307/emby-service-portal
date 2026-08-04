package admin

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

func TestRenderFormatsTimesInConfiguredLocation(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := NewTemplates(location)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	templates.Render(response, "accounts", ViewData{Accounts: []sqlite.Account{{ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}})
	page := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(page, "2030-01-01 08:00") || !strings.Contains(page, "value=\"2030-01-01T08:00\"") || !strings.Contains(page, "Asia/Shanghai") {
		t.Fatalf("configured time zone was not rendered: %s", page)
	}
}

func TestRenderStatusUsesRequestedStatusAfterSuccessfulExecution(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}complete{{end}}`))}
	response := httptest.NewRecorder()
	templates.RenderStatus(response, "page", ViewData{}, http.StatusUnauthorized)

	if response.Code != http.StatusUnauthorized || response.Body.String() != "complete" {
		t.Fatalf("response = (%d, %q), want (401, complete)", response.Code, response.Body.String())
	}
}

func TestRenderStatusReturns500InsteadOfRequestedStatusOnExecutionFailure(t *testing.T) {
	templates := &Templates{templates: template.Must(template.New("root").Parse(`{{define "page"}}partial {{.Missing}}{{end}}`))}
	response := httptest.NewRecorder()
	templates.RenderStatus(response, "page", ViewData{}, http.StatusBadRequest)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "partial") {
		t.Fatalf("response leaked partial template: %q", response.Body.String())
	}
}

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
