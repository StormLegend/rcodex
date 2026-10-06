package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPendingAndResolvedApprovalReadback(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	session, err := s.CreateSession(Session{Runtime: "codex", Workspace: t.TempDir(), Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Enqueue(session.ID, "test", "approval-test", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.NewApproval(turn, map[string]string{"method": "requestApproval"})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"pending", "resolved"} {
		if state == "resolved" {
			if err = s.Resolve(a.ID, map[string]bool{"approved": false}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Approval(a.ID)
		if err != nil || got.State != state {
			t.Fatalf("approval=%+v error=%v", got, err)
		}
		list, err := s.Approvals(state, 10)
		if err != nil || len(list) != 1 || list[0].ID != a.ID {
			t.Fatalf("list=%+v error=%v", list, err)
		}
		if state == "pending" && len(got.Response) != 0 {
			t.Fatalf("pending response=%s", got.Response)
		}
		if state == "resolved" && !json.Valid(got.Response) {
			t.Fatalf("resolved response=%s", got.Response)
		}
	}
}
