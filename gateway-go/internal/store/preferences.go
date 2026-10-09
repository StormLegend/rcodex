package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// SaveSession serializes settings changes with enqueue, so an in-flight turn's
// model, permissions and effort never change underneath it.
func (s *Store) SaveSession(v Session) (Session, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var active int
	if e = tx.QueryRow(`SELECT count(*) FROM turns WHERE session_id=? AND state IN ('queued','running')`, v.ID).Scan(&active); e != nil {
		return v, e
	}
	if active > 0 {
		return v, ErrBusy
	}
	res, e := tx.Exec(`UPDATE sessions SET model=?,mode=?,title=? WHERE id=?`, v.Model, v.Mode, v.Title, v.ID)
	if e != nil {
		return v, e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return v, ErrNotFound
	}
	_, e = tx.Exec(`INSERT INTO session_preferences VALUES(?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET effort=excluded.effort,archived=excluded.archived,source_workspace=excluded.source_workspace`, v.ID, v.Effort, v.Archived, v.SourceWorkspace)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	return s.Session(v.ID)
}

// BackfillCodexMetadata restores presentation metadata for legacy imports once.
// It never writes to Codex, changes execution workspaces, or overwrites a user's
// choices in this gateway. Keeping preferences separate preserves v1 rollback.
func (s *Store) BackfillCodexMetadata(home string) (int, error) {
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return 0, e
		}
		home = filepath.Join(h, ".codex")
	}
	file := filepath.Join(home, "state_5.sqlite")
	if _, e := os.Stat(file); os.IsNotExist(e) {
		return 0, nil
	} else if e != nil {
		return 0, e
	}
	source, e := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: file, RawQuery: "mode=ro"}).String())
	if e != nil {
		return 0, e
	}
	defer source.Close()
	// Optional effort columns vary between Codex versions; directory/archive do not.
	columns := map[string]bool{}
	cr, e := source.Query(`PRAGMA table_info(threads)`)
	if e != nil {
		return 0, e
	}
	for cr.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if e = cr.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
			cr.Close()
			return 0, e
		}
		columns[name] = true
	}
	cr.Close()
	if !columns["cwd"] || !columns["archived"] {
		return 0, nil
	}
	effort := "''"
	if columns["reasoning_effort"] {
		effort = "COALESCE(reasoning_effort,'')"
	}
	model := "''"
	if columns["model"] {
		model = "COALESCE(model,'')"
	}
	rows, e := source.Query(`SELECT id,cwd,archived,` + effort + `,` + model + ` FROM threads`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	tx, e := s.DB.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	count := 0
	for rows.Next() {
		var id, cwd, eff, model string
		var archived bool
		if e = rows.Scan(&id, &cwd, &archived, &eff, &model); e != nil {
			return 0, e
		}
		res, e := tx.Exec(`INSERT OR IGNORE INTO session_preferences(session_id,source_workspace,archived,effort) SELECT id,?,?,? FROM sessions WHERE id=? AND native_id=?`, cwd, archived, eff, "import-"+id, id)
		if e != nil {
			return 0, fmt.Errorf("import metadata: %w", e)
		}
		n, _ := res.RowsAffected()
		count += int(n)
		if n > 0 && model != "" {
			if _, e = tx.Exec(`UPDATE sessions SET model=? WHERE id=? AND model=''`, model, "import-"+id); e != nil {
				return 0, e
			}
		}
	}
	if e = rows.Err(); e != nil {
		return 0, e
	}
	return count, tx.Commit()
}

// RecordRuntimeSession captures effective defaults reported by the runtime.
// Explicit user choices always take precedence.
func (s *Store) RecordRuntimeSession(id, native, model, effort string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`UPDATE sessions SET native_id=?,model=CASE WHEN model='' THEN ? ELSE model END WHERE id=?`, native, model, id)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO session_preferences(session_id,effort) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET effort=CASE WHEN session_preferences.effort='' THEN excluded.effort ELSE session_preferences.effort END`, id, effort)
	if e != nil {
		return e
	}
	return tx.Commit()
}
