package web

import (
	"embed"
	"net/http"
)

//go:embed static/app.css static/app.js
var assets embed.FS

func (s *Server) script(w http.ResponseWriter, _ *http.Request) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		http.Error(w, "script unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(script)
}

func (s *Server) stylesheet(w http.ResponseWriter, _ *http.Request) {
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		http.Error(w, "stylesheet unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(css)
}
