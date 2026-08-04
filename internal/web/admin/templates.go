package admin

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

//go:embed templates/*.html
var files embed.FS

type ViewData struct {
	CSRFToken string
	Error     string
	Message   string
	Accounts  []sqlite.Account
	Account   sqlite.Account
	Invites   []sqlite.InviteCode
}

type Templates struct{ templates *template.Template }

func NewTemplates(location *time.Location) (*Templates, error) {
	if location == nil {
		location = time.UTC
	}
	functions := template.FuncMap{
		"formatTime": func(value time.Time, layout string) string { return value.In(location).Format(layout) },
		"timeZone":   func() string { return location.String() },
	}
	t, err := template.New("pages").Funcs(functions).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Templates{templates: t}, nil
}

func (t *Templates) Render(w http.ResponseWriter, name string, data ViewData) {
	t.RenderStatus(w, name, data, http.StatusOK)
}

func (t *Templates) RenderStatus(w http.ResponseWriter, name string, data ViewData, status int) {
	var body bytes.Buffer
	if err := t.templates.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
