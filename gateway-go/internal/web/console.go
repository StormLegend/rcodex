package web

import (
	"embed"
	"net/http"
)

//go:embed console.html
var consoleFS embed.FS

func console(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	data, err := consoleFS.ReadFile("console.html")
	if err != nil {
		http.Error(w, "console unavailable", 500)
		return
	}
	_, _ = w.Write(data)
}
