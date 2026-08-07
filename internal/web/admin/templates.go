package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

//go:embed templates/*.html
var files embed.FS

type ViewData struct {
	CSRFToken     string
	Error         string
	Message       string
	Accounts      []sqlite.Account
	Account       sqlite.Account
	Invites       []sqlite.InviteCode
	NewInviteCode string
	AccountCount  int
	ActiveCount   int
	DisabledCount int
	ExpiredCount  int
	InviteCount   int
}

type Templates struct{ templates *template.Template }

func NewTemplates(location *time.Location) (*Templates, error) {
	if location == nil {
		location = time.UTC
	}
	functions := template.FuncMap{
		"formatTime": func(value time.Time, layout string) string { return value.In(location).Format(layout) },
		"timeZone":   func() string { return location.String() },
		"statusLabel": func(status string) string {
			switch status {
			case "active":
				return "活跃"
			case "disabled":
				return "已禁用"
			case "expired":
				return "已过期"
			default:
				return status
			}
		},
		"formatDuration": func(minutes int) string {
			switch {
			case minutes <= 0:
				return "0 分钟"
			case minutes%1440 == 0:
				return fmt.Sprintf("%d 天", minutes/1440)
			case minutes%60 == 0:
				return fmt.Sprintf("%d 小时", minutes/60)
			default:
				return fmt.Sprintf("%d 分钟", minutes)
			}
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			result := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", values[i])
				}
				result[key] = values[i+1]
			}
			return result, nil
		},
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
