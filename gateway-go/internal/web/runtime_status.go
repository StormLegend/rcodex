package web

import (
	"net/http"
	"os/exec"
)

// Report installation availability without spawning a CLI, reading credentials,
// or making paid model requests. A completed turn verifies authentication.
func (s *Server) runtimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type runtimeInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Available bool   `json:"available"`
	}
	runtimes := []runtimeInfo{}
	for _, item := range []struct{ id, name, command string }{
		{"codex", "Codex", s.Cfg.CodexCommand},
		{"claude", "Claude Code", s.Cfg.ClaudeCommand},
	} {
		_, err := exec.LookPath(item.command)
		runtimes = append(runtimes, runtimeInfo{ID: item.id, Name: item.name, Available: err == nil})
	}
	modes := []string{"readonly", "ask", "auto"}
	if s.Cfg.AllowFull {
		modes = append(modes, "full")
	}
	s.write(w, http.StatusOK, map[string]any{"runtimes": runtimes, "workspaces": s.Cfg.Roots, "modes": modes})
}
