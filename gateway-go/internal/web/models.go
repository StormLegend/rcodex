package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

type modelChoice struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Runtime       string   `json:"runtime"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	ContextWindow int64    `json:"context_window,omitempty"`
}

func (s *Server) modelCatalog() []modelChoice {
	out := []modelChoice{}
	home := s.Cfg.CodexHome
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	b, e := os.ReadFile(filepath.Join(home, "models_cache.json"))
	if e == nil {
		var cache struct {
			Models []struct {
				Slug             string
				DisplayName      string `json:"display_name"`
				Visibility       string
				DefaultReasoning string                    `json:"default_reasoning_level"`
				Context          int64                     `json:"context_window"`
				Levels           []struct{ Effort string } `json:"supported_reasoning_levels"`
			}
		}
		if json.Unmarshal(b, &cache) == nil {
			for _, m := range cache.Models {
				if m.Visibility != "list" || m.Slug == "" {
					continue
				}
				c := modelChoice{ID: m.Slug, Name: m.DisplayName, Runtime: "codex", DefaultEffort: m.DefaultReasoning, ContextWindow: m.Context}
				for _, v := range m.Levels {
					c.Efforts = append(c.Efforts, v.Effort)
				}
				out = append(out, c)
			}
		}
	}
	// CLI aliases follow the installed Claude Code's configured provider/version.
	for _, id := range []string{"sonnet", "opus", "haiku"} {
		out = append(out, modelChoice{ID: id, Name: id, Runtime: "claude", Efforts: []string{"low", "medium", "high", "xhigh", "max"}})
	}
	names := []string{}
	for name := range s.Cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := s.Cfg.Providers[name]
		if !p.Enabled {
			continue
		}
		for _, id := range p.Models {
			if !slices.ContainsFunc(out, func(m modelChoice) bool { return m.ID == id && m.Runtime == p.Runtime }) {
				out = append(out, modelChoice{ID: id, Name: id, Runtime: p.Runtime})
			}
		}
	}
	return out
}
func (s *Server) validateEffort(runtime, model, effort string) error {
	if effort == "" {
		return nil
	}
	allowed := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	if runtime == "claude" {
		allowed = []string{"low", "medium", "high", "xhigh", "max"}
	}
	for _, m := range s.modelCatalog() {
		if m.Runtime == runtime && m.ID == model && len(m.Efforts) > 0 {
			allowed = m.Efforts
			break
		}
	}
	if !slices.Contains(allowed, effort) {
		return fmt.Errorf("unsupported reasoning effort %q for model %q", effort, model)
	}
	return nil
}
