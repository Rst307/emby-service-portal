package admin

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

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

func NewTemplates() (*Templates, error) {
	t, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Templates{templates: t}, nil
}

func (t *Templates) Render(w http.ResponseWriter, name string, data ViewData) {
	var body bytes.Buffer
	if err := t.templates.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}
