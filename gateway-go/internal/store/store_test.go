package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestQueueRecoveryIdempotencyAndCursor(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	v, e := s.CreateSession(Session{Runtime: "codex", Workspace: "/tmp", Mode: "ask"})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Enqueue(v.ID, "one", "k1", "", 10)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Enqueue(v.ID, "other", "k2", "", 10); !errors.Is(e, ErrBusy) {
		t.Fatal("busy not enforced", e)
	}
	b, e := s.Enqueue(v.ID, "one", "k1", "", 10)
	if e != nil || b.ID != a.ID {
		t.Fatal("idempotency", e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Turn(a.ID); e != nil {
		t.Fatal(e)
	}
}
func TestEventsCursor(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	defer s.DB.Close()
	v, _ := s.CreateSession(Session{Runtime: "codex", Workspace: "/tmp", Mode: "ask"})
	for i := 0; i < 3; i++ {
		s.Event(v.ID, "t", "delta", map[string]int{"i": i})
	}
	e, err := s.Events(v.ID, 0, 0, 2)
	if err != nil || len(e) != 2 {
		t.Fatal(err, len(e))
	}
	if e[0].ID != 1 {
		t.Fatal(e)
	}
}

func TestFinishQueuesDurableNotification(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	v, _ := s.CreateSession(Session{Runtime: "codex", Workspace: "/tmp", Mode: "ask"})
	tn, _ := s.Enqueue(v.ID, "hello", "", `{"channel":"telegram","chat":"42"}`, 10)
	claimed, e := s.Claim()
	if e != nil || claimed.ID != tn.ID {
		t.Fatal(e)
	}
	if e = s.Finish(claimed, "done", nil); e != nil {
		t.Fatal(e)
	}
	d, e := s.Deliveries()
	if e != nil || len(d) != 1 {
		t.Fatalf("deliveries=%v err=%v", d, e)
	}
	if d[0].Destination.Channel != "telegram" || d[0].Text != "done" {
		t.Fatalf("delivery=%+v", d[0])
	}
}
