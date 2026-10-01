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
	data, err := consoleFS.ReadFile("console.html")
	if err != nil {
		http.Error(w, "console unavailable", 500)
		return
	}
	_, _ = w.Write(data)
}
