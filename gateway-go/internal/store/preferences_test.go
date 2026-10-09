package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestMetadataBackfillArchiveAndRollbackCompatibility(t *testing.T) {
	home := t.TempDir()
	source, e := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	_, e = source.Exec(`CREATE TABLE threads(id TEXT,cwd TEXT,archived INTEGER,reasoning_effort TEXT);INSERT INTO threads VALUES('native','/original/project',1,'high')`)
	if e != nil {
		t.Fatal(e)
	}
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	// Emulate an old binary's eight-column insert, without new metadata.
	_, e = s.DB.Exec(`INSERT INTO sessions VALUES('import-native','codex','native','/safe/workspace','','readonly','old',1)`)
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.BackfillCodexMetadata(home)
	if e != nil || n != 1 {
		t.Fatalf("backfill %d %v", n, e)
	}
	v, e := s.Session("import-native")
	if e != nil || !v.Archived || v.SourceWorkspace != "/original/project" || v.Workspace != "/safe/workspace" || v.Effort != "high" {
		t.Fatalf("metadata %+v %v", v, e)
	}
	if _, e = s.Enqueue(v.ID, "new", "", "", 10); e == nil {
		t.Fatal("archived session accepted turn")
	}
	v.Archived = false
	v.Effort = "low"
	v, e = s.SaveSession(v)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.BackfillCodexMetadata(home)
	if e != nil || n != 0 {
		t.Fatalf("re-import %d %v", n, e)
	}
	v, _ = s.Session(v.ID)
	if v.Archived || v.Effort != "low" {
		t.Fatal("user settings overwritten")
	}
	turn, e := s.Enqueue(v.ID, "new", "stable", "", 10)
	if e != nil {
		t.Fatal(e)
	}
	v.Archived = true
	if _, e = s.SaveSession(v); !errors.Is(e, ErrBusy) {
		t.Fatalf("active settings update: %v", e)
	}
	if e = s.CancelQueued(turn.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteSession(v.ID); e != nil {
		t.Fatal(e)
	}
	var count int
	s.DB.QueryRow(`SELECT count(*) FROM session_preferences`).Scan(&count)
	if count != 0 {
		t.Fatal("orphan metadata")
	}
}
func TestCodexUsageCumulativeDuplicateResetAndTurnIsolation(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	v, _ := s.CreateSession(Session{Runtime: "codex"})
	add := func(turn string, total, last int64) {
		t.Helper()
		_, e := s.Event(v.ID, turn, "thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{"total": TokenCounts{Total: total, Input: total - 10, Output: 10}, "last": TokenCounts{Total: last, Input: last - 2, Output: 2}, "modelContextWindow": 1000}})
		if e != nil {
			t.Fatal(e)
		}
	}
	add("prior", 10000, 80)
	add("current", 10100, 100)
	add("current", 10300, 200)
	add("current", 10300, 200)
	u, e := s.TurnUsage(v.ID, "current")
	if e != nil || u.Tokens.Total != 300 || u.ContextTokens != 200 || u.ContextWindow != 1000 {
		t.Fatalf("usage %+v %v", u, e)
	}
	add("current", 50, 50)
	u, e = s.TurnUsage(v.ID, "current")
	if e != nil || u.Tokens.Total != 350 || u.ContextTokens != 50 {
		t.Fatalf("reset %+v %v", u, e)
	}
	if u, e = s.TurnUsage(v.ID, "empty"); e != nil || u != nil {
		t.Fatalf("unknown usage %+v %v", u, e)
	}
}
func TestClaudeUsageResultAndUnknownContext(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	v, _ := s.CreateSession(Session{Runtime: "claude"})
	for _, raw := range []string{`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":20}}}`, `{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":100,"output_tokens":8,"cache_read_input_tokens":20}}}`, `{"type":"result","usage":{"input_tokens":200,"output_tokens":18,"cache_read_input_tokens":40},"modelUsage":{"opus":{"contextWindow":1000}}}`} {
		if _, e = s.Event(v.ID, "turn", "claude/event", json.RawMessage(raw)); e != nil {
			t.Fatal(e)
		}
	}
	u, e := s.TurnUsage(v.ID, "turn")
	if e != nil || u.Tokens.Total != 258 || u.ContextTokens != 128 || u.ContextWindow != 1000 {
		t.Fatalf("usage %+v %v", u, e)
	}
}
