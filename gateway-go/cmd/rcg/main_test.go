package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func TestMaintenanceSubprocess(t *testing.T) {
	if os.Getenv("RCG_TEST_MAINTENANCE") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			main()
			return
		}
	}
	t.Fatal("missing subprocess arguments")
}

func TestMaintenancePreservesLiveTurns(t *testing.T) {
	for _, mode := range []string{"doctor", "backup"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			s, err := store.Open(filepath.Join(root, "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			session, err := s.CreateSession(store.Session{Runtime: "claude", Workspace: root, Mode: "ask"})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := s.Enqueue(session.ID, "pending approval", "idem", "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Claim(); err != nil {
				t.Fatal(err)
			}
			approval, err := s.NewApproval(turn, map[string]string{"method": "requestApproval"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(map[string]any{"listen": "127.0.0.1:0", "data_dir": root, "token": strings.Repeat("t", 32), "roots": []string{root}})
			if err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(root, "config.json")
			if err = os.WriteFile(cfg, b, 0600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-test.run=^TestMaintenanceSubprocess$", "--", "-config", cfg, "-" + mode}
			backup := filepath.Join(root, "snapshot.db")
			if mode == "backup" {
				args = append(args, backup)
			}
			cmd := exec.Command(exe, args...)
			cmd.Env = append(os.Environ(), "RCG_TEST_MAINTENANCE=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("maintenance: %v %s", err, out)
			}
			got, err := s.Turn(turn.ID)
			if err != nil || got.State != "running" {
				t.Fatalf("maintenance changed live turn: %+v %v", got, err)
			}
			gotApproval, err := s.Approval(approval.ID)
			if err != nil || gotApproval.State != approval.State {
				t.Fatalf("maintenance changed approval: %+v %v", gotApproval, err)
			}
			if mode == "backup" {
				copy, err := store.Open(backup)
				if err != nil {
					t.Fatal(err)
				}
				defer copy.DB.Close()
				copied, err := copy.Turn(turn.ID)
				if err != nil || copied.State != "running" {
					t.Fatalf("backup changed live turn: %+v %v", copied, err)
				}
				if err = copy.Check(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
