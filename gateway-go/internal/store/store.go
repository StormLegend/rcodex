package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

var ErrBusy = errors.New("session already has a pending turn")
var ErrNotFound = errors.New("not found")

type Store struct{ DB *sql.DB }
type Session struct {
	ID        string `json:"id"`
	Runtime   string `json:"runtime"`
	NativeID  string `json:"native_id,omitempty"`
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Mode      string `json:"mode"`
	Title     string `json:"title"`
	Created   int64  `json:"created"`
}
type Turn struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
	State     string `json:"state"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	Notify    string `json:"-"`
}
type Event struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	Created   int64           `json:"created"`
}
type Approval struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response,omitempty"`
	State     string          `json:"state"`
	Created   int64           `json:"created"`
}
type Notification struct {
	Channel string `json:"channel"`
	Chat    string `json:"chat"`
	Token   string `json:"token,omitempty"`
	AppID   string `json:"app_id,omitempty"`
}
type Delivery struct {
	ID          string
	Destination Notification
	Text        string
	Attempts    int
}

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func Now() int64 { return time.Now().UnixMilli() }
func JSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func Open(file string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	db, e := sql.Open("sqlite", file)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,runtime TEXT NOT NULL,native_id TEXT NOT NULL DEFAULT '',workspace TEXT NOT NULL,model TEXT NOT NULL,mode TEXT NOT NULL,title TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS turns(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),prompt TEXT NOT NULL,state TEXT NOT NULL,result TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,updated INTEGER NOT NULL,notify TEXT NOT NULL DEFAULT '',idem TEXT UNIQUE);
 CREATE UNIQUE INDEX IF NOT EXISTS session_active ON turns(session_id) WHERE state IN ('queued','running');
 CREATE INDEX IF NOT EXISTS turn_queue ON turns(state,created);
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL REFERENCES sessions(id),turn_id TEXT NOT NULL,kind TEXT NOT NULL,data BLOB NOT NULL,created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS event_cursor ON events(session_id,id);
 CREATE TABLE IF NOT EXISTS approvals(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,turn_id TEXT NOT NULL,request BLOB NOT NULL,response BLOB,state TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS bindings(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id));
 CREATE TABLE IF NOT EXISTS outbox(id TEXT PRIMARY KEY,destination TEXT NOT NULL,text TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'queued');
 CREATE TABLE IF NOT EXISTS schedules(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),prompt TEXT NOT NULL,expression TEXT NOT NULL,next_at INTEGER NOT NULL,paused INTEGER NOT NULL DEFAULT 0);
 PRAGMA user_version=1;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Recover() error {
	_, e := s.DB.Exec(`UPDATE turns SET state='interrupted',error='gateway restarted during execution; explicit retry required',updated=? WHERE state='running'; UPDATE approvals SET state='expired' WHERE state='pending'`, Now())
	return e
}
func (s *Store) CreateSession(v Session) (Session, error) {
	if v.ID == "" {
		v.ID = ID()
	}
	v.Created = Now()
	_, e := s.DB.Exec(`INSERT INTO sessions VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Runtime, v.NativeID, v.Workspace, v.Model, v.Mode, v.Title, v.Created)
	return v, e
}
func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var v Session
	e := row.Scan(&v.ID, &v.Runtime, &v.NativeID, &v.Workspace, &v.Model, &v.Mode, &v.Title, &v.Created)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return v, e
}
func (s *Store) Session(id string) (Session, error) {
	return scanSession(s.DB.QueryRow(`SELECT * FROM sessions WHERE id=?`, id))
}
func (s *Store) Sessions(before int64, limit int) ([]Session, error) {
	if before == 0 {
		before = 1 << 62
	}
	rows, e := s.DB.Query(`SELECT * FROM sessions WHERE rowid<? ORDER BY rowid DESC LIMIT ?`, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		v, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Native(id, native string) error {
	_, e := s.DB.Exec(`UPDATE sessions SET native_id=? WHERE id=?`, native, id)
	return e
}
func scanTurn(row interface{ Scan(...any) error }) (Turn, error) {
	var t Turn
	var idem sql.NullString
	e := row.Scan(&t.ID, &t.SessionID, &t.Prompt, &t.State, &t.Result, &t.Error, &t.Created, &t.Updated, &t.Notify, &idem)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return t, e
}
func (s *Store) Turn(id string) (Turn, error) {
	return scanTurn(s.DB.QueryRow(`SELECT * FROM turns WHERE id=?`, id))
}
func (s *Store) Enqueue(session, prompt, key, notify string, capacity int) (Turn, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return Turn{}, e
	}
	defer tx.Rollback()
	if key != "" {
		t, e := scanTurn(tx.QueryRow(`SELECT * FROM turns WHERE idem=?`, session+":"+key))
		if e == nil {
			return t, nil
		}
		if !errors.Is(e, ErrNotFound) {
			return Turn{}, e
		}
	}
	var count int
	if e = tx.QueryRow(`SELECT count(*) FROM turns WHERE state='queued'`).Scan(&count); e != nil {
		return Turn{}, e
	}
	if count >= capacity {
		return Turn{}, errors.New("queue capacity reached")
	}
	var active int
	if e = tx.QueryRow(`SELECT count(*) FROM turns WHERE session_id=? AND state IN ('queued','running')`, session).Scan(&active); e != nil {
		return Turn{}, e
	}
	if active > 0 {
		return Turn{}, ErrBusy
	}
	t := Turn{ID: ID(), SessionID: session, Prompt: prompt, State: "queued", Created: Now(), Updated: Now(), Notify: notify}
	var idem any
	if key != "" {
		idem = session + ":" + key
	}
	_, e = tx.Exec(`INSERT INTO turns VALUES(?,?,?,?,?,?,?,?,?,?)`, t.ID, t.SessionID, t.Prompt, t.State, "", "", t.Created, t.Updated, notify, idem)
	if e != nil {
		return Turn{}, e
	}
	return t, tx.Commit()
}
func (s *Store) Claim() (Turn, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return Turn{}, e
	}
	defer tx.Rollback()
	t, e := scanTurn(tx.QueryRow(`SELECT * FROM turns WHERE state='queued' ORDER BY created,id LIMIT 1`))
	if e != nil {
		return t, e
	}
	_, e = tx.Exec(`UPDATE turns SET state='running',updated=? WHERE id=? AND state='queued'`, Now(), t.ID)
	if e != nil {
		return t, e
	}
	t.State = "running"
	return t, tx.Commit()
}
func (s *Store) Finish(t Turn, result string, runErr error) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	state, msg := "completed", ""
	if runErr != nil {
		state = "failed"
		msg = runErr.Error()
		if errors.Is(runErr, context.Canceled) {
			state = "cancelled"
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			state = "timed_out"
		}
	}
	_, e = tx.Exec(`UPDATE turns SET state=?,result=?,error=?,updated=? WHERE id=?`, state, result, msg, Now(), t.ID)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO events(session_id,turn_id,kind,data,created) VALUES(?,?,?,?,?)`, t.SessionID, t.ID, "turn.finished", JSON(map[string]any{"state": state, "error": msg}), Now())
	if e != nil {
		return e
	}
	_, e = tx.Exec(`UPDATE approvals SET state='expired' WHERE turn_id=? AND state='pending'`, t.ID)
	if e != nil {
		return e
	}
	if t.Notify != "" {
		text := result
		if state != "completed" {
			text = "Task " + state + ". Check gateway turn " + t.ID
		}
		if text == "" {
			text = "Task completed."
		}
		_, e = tx.Exec(`INSERT OR IGNORE INTO outbox(id,destination,text,next_at) VALUES(?,?,?,?)`, t.ID, t.Notify, text, Now())
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Event(session, turn, kind string, data any) (Event, error) {
	b := JSON(data)
	res, e := s.DB.Exec(`INSERT INTO events(session_id,turn_id,kind,data,created) VALUES(?,?,?,?,?)`, session, turn, kind, b, Now())
	if e != nil {
		return Event{}, e
	}
	id, e := res.LastInsertId()
	return Event{id, session, turn, kind, b, Now()}, e
}
func (s *Store) Events(id string, after, before int64, limit int) ([]Event, error) {
	q := `SELECT id,session_id,turn_id,kind,data,created FROM events WHERE session_id=? AND id>? ORDER BY id LIMIT ?`
	args := []any{id, after, limit}
	if before > 0 {
		q = `SELECT id,session_id,turn_id,kind,data,created FROM events WHERE session_id=? AND id<? ORDER BY id DESC LIMIT ?`
		args = []any{id, before, limit}
	}
	rows, e := s.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.ID, &v.SessionID, &v.TurnID, &v.Kind, &v.Data, &v.Created); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if before > 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, rows.Err()
}
func (s *Store) NewApproval(t Turn, request any) (Approval, error) {
	v := Approval{ID: ID(), SessionID: t.SessionID, TurnID: t.ID, Request: JSON(request), State: "pending", Created: Now()}
	_, e := s.DB.Exec(`INSERT INTO approvals(id,session_id,turn_id,request,state,created) VALUES(?,?,?,?,?,?)`, v.ID, v.SessionID, v.TurnID, v.Request, v.State, v.Created)
	return v, e
}
func (s *Store) Approval(id string) (Approval, error) {
	var v Approval
	e := s.DB.QueryRow(`SELECT * FROM approvals WHERE id=?`, id).Scan(&v.ID, &v.SessionID, &v.TurnID, &v.Request, &v.Response, &v.State, &v.Created)
	return v, e
}
func (s *Store) Approvals(state string, limit int) ([]Approval, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q := `SELECT * FROM approvals ORDER BY created DESC LIMIT ?`
	args := []any{limit}
	if state != "" {
		q = `SELECT * FROM approvals WHERE state=? ORDER BY created DESC LIMIT ?`
		args = []any{state, limit}
	}
	rows, e := s.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var v Approval
		if e = rows.Scan(&v.ID, &v.SessionID, &v.TurnID, &v.Request, &v.Response, &v.State, &v.Created); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Resolve(id string, response any) error {
	res, e := s.DB.Exec(`UPDATE approvals SET response=?,state='resolved' WHERE id=? AND state='pending'`, JSON(response), id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) CancelQueued(id string) error {
	_, e := s.DB.Exec(`UPDATE turns SET state='cancelled',updated=? WHERE id=? AND state='queued'`, Now(), id)
	return e
}
func (s *Store) Bind(key string, v Session) (Session, error) {
	var id string
	e := s.DB.QueryRow(`SELECT session_id FROM bindings WHERE id=?`, key).Scan(&id)
	if e == nil {
		return s.Session(id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	v.ID = ID()
	v.Created = Now()
	_, e = tx.Exec(`INSERT INTO sessions VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Runtime, "", v.Workspace, v.Model, v.Mode, v.Title, v.Created)
	if e != nil {
		return v, e
	}
	_, e = tx.Exec(`INSERT INTO bindings VALUES(?,?)`, key, v.ID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Store) Deliveries() ([]Delivery, error) {
	rows, e := s.DB.Query(`SELECT id,destination,text,attempts FROM outbox WHERE state='queued' AND next_at<=? ORDER BY next_at LIMIT 10`, Now())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var v Delivery
		var raw string
		if e = rows.Scan(&v.ID, &raw, &v.Text, &v.Attempts); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &v.Destination); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) DeliveryResult(id string, attempt int, ok bool) error {
	state := "queued"
	if ok {
		state = "sent"
	} else if attempt >= 8 {
		state = "dead"
	}
	delay := 1 << min(attempt, 8)
	_, e := s.DB.Exec(`UPDATE outbox SET state=?,attempts=?,next_at=? WHERE id=?`, state, attempt, Now()+int64(time.Second/time.Millisecond)*int64(delay), id)
	return e
}
func (s *Store) Stats() map[string]int64 {
	out := map[string]int64{}
	for _, table := range []string{"sessions", "events", "turns", "approvals", "outbox"} {
		var n int64
		if e := s.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e == nil {
			out[table] = n
		}
	}
	return out
}
func (s *Store) Backup(file string) error {
	if _, e := os.Stat(file); !os.IsNotExist(e) {
		return errors.New("backup target must not exist")
	}
	_, e := s.DB.Exec(`VACUUM INTO ?`, file)
	if e == nil {
		e = os.Chmod(file, 0600)
	}
	return e
}
func (s *Store) Check() error {
	var value string
	e := s.DB.QueryRow(`PRAGMA quick_check`).Scan(&value)
	if e == nil && value != "ok" {
		return fmt.Errorf("sqlite quick_check failed: %s", value)
	}
	return e
}
