package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsInsecurePublicAndWeakSecrets(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "gateway.json")
	os.WriteFile(f, []byte(`{"listen":"0.0.0.0:8790","data_dir":"`+d+`/data","token":"short","roots":["`+d+`"]}`), 0600)
	if _, e := Load(f); e == nil {
		t.Fatal("weak token accepted")
	}
}
func TestLoadLocalConfig(t *testing.T) {
	d := t.TempDir()
	tok := strings.Repeat("x", 40)
	f := filepath.Join(d, "gateway.json")
	os.WriteFile(f, []byte(`{"listen":"127.0.0.1:8790","data_dir":"`+d+`/data","token":"`+tok+`","roots":["`+d+`"]}`), 0600)
	c, e := Load(f)
	if e != nil {
		t.Fatal(e)
	}
	if c.Listen != "127.0.0.1:8790" {
		t.Fatal(c.Listen)
	}
}

func TestLoadDefaultsAndProviderInventory(t *testing.T) {
	d := t.TempDir()
	tok := strings.Repeat("x", 40)
	f := filepath.Join(d, "gateway.json")
	os.WriteFile(f, []byte(`{"listen":"127.0.0.1:8790","data_dir":"`+d+`/data","token":"`+tok+`","read_token":"`+strings.Repeat("y", 40)+`","roots":["`+d+`"],"providers":{"test":{"runtime":"claude","models":["sonnet"],"enabled":true}}}`), 0600)
	c, e := Load(f)
	if e != nil {
		t.Fatal(e)
	}
	if c.RateLimitPerMinute != 120 || c.ReadRateLimitPerMinute != 600 || !c.Providers["test"].Enabled {
		t.Fatalf("config=%+v", c)
	}
}
