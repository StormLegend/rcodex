// rcg-admin contains offline maintenance commands. Restore and token rotation
// must be run against a stopped gateway; the command refuses nothing silently.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: rcg-admin backup|restore|rotate-token")
	}
	switch os.Args[1] {
	case "backup":
		backup(os.Args[2:])
	case "restore":
		restore(os.Args[2:])
	case "rotate-token":
		rotateToken(os.Args[2:])
	default:
		fail("unknown command")
	}
}

func backup(args []string) {
	f := flag.NewFlagSet("backup", flag.ExitOnError)
	cfgPath := f.String("config", "gateway.json", "gateway config")
	out := f.String("output", "", "backup database path")
	f.Parse(args)
	if *out == "" {
		fail("-output is required")
	}
	c := load(*cfgPath)
	s := open(c)
	defer s.DB.Close()
	if err := s.Check(); err != nil {
		fail(err.Error())
	}
	if err := s.Backup(*out); err != nil {
		fail(err.Error())
	}
	fmt.Println(*out)
}
func restore(args []string) {
	f := flag.NewFlagSet("restore", flag.ExitOnError)
	cfgPath := f.String("config", "gateway.json", "gateway config")
	in := f.String("input", "", "validated SQLite backup")
	f.Parse(args)
	if *in == "" {
		fail("-input is required")
	}
	c := load(*cfgPath)
	src, err := filepath.Abs(*in)
	if err != nil {
		fail(err.Error())
	}
	target := filepath.Join(c.DataDir, "gateway.db")
	target, _ = filepath.Abs(target)
	if src == target {
		fail("input and gateway database must differ")
	}
	checkDB(src)
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		fail(err.Error())
	}
	ts := time.Now().UTC().Format("20060102-150405.000000000")
	pre := target + ".before-restore-" + ts
	if _, err := os.Stat(target); err == nil {
		cur, e := store.Open(target)
		if e != nil {
			fail(e.Error())
		}
		if e = cur.Check(); e != nil {
			_ = cur.DB.Close()
			fail(e.Error())
		}
		if e = cur.Backup(pre); e != nil {
			_ = cur.DB.Close()
			fail(e.Error())
		}
		_ = cur.DB.Close()
		if e = os.Chmod(pre, 0600); e != nil {
			fail(e.Error())
		}
	}
	tmp, e := os.CreateTemp(c.DataDir, ".restore-*")
	if e != nil {
		fail(e.Error())
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if e = copyFile(tmp, src); e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e != nil {
		fail(e.Error())
	}
	if closeErr != nil {
		fail(closeErr.Error())
	}
	if e = os.Chmod(tmpName, 0600); e != nil {
		fail(e.Error())
	}
	if e = os.Rename(tmpName, target); e != nil {
		fail(e.Error())
	}
	checkDB(target)
	fmt.Printf("restored=%s\nprevious_backup=%s\n", target, pre)
}
func rotateToken(args []string) {
	f := flag.NewFlagSet("rotate-token", flag.ExitOnError)
	path := f.String("config", "gateway.json", "gateway config")
	field := f.String("field", "token", "token or read_token")
	f.Parse(args)
	if *field != "token" && *field != "read_token" {
		fail("-field must be token or read_token")
	}
	b, e := os.ReadFile(*path)
	if e != nil {
		fail(e.Error())
	}
	if bytesContainEnv(b) {
		fail("refusing to rewrite config containing environment placeholders; rotate the environment secret and restart")
	}
	var obj map[string]json.RawMessage
	if e = json.Unmarshal(b, &obj); e != nil {
		fail(e.Error())
	}
	secret := randomSecret()
	raw, _ := json.Marshal(secret)
	obj[*field] = raw
	out, e := json.MarshalIndent(obj, "", "  ")
	if e != nil {
		fail(e.Error())
	}
	ts := time.Now().UTC().Format("20060102-150405.000000000")
	bak := *path + ".before-token-" + ts
	if e = os.Rename(*path, bak); e != nil {
		fail(e.Error())
	}
	tmp, e := os.CreateTemp(filepath.Dir(*path), ".config-*")
	if e != nil {
		_ = os.Rename(bak, *path)
		fail(e.Error())
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, e = tmp.Write(append(out, '\n')); e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e != nil || closeErr != nil {
		_ = os.Rename(bak, *path)
		fail(first(e, closeErr).Error())
	}
	if e = os.Chmod(tmpName, 0600); e == nil {
		e = os.Rename(tmpName, *path)
	}
	if e != nil {
		_ = os.Rename(bak, *path)
		fail(e.Error())
	}
	fmt.Printf("field=%s\nsecret=%s\nbackup=%s\nrestart_required=true\n", *field, secret, bak)
}
func load(path string) config.Config {
	c, e := config.Load(path)
	if e != nil {
		fail(e.Error())
	}
	return c
}
func open(c config.Config) *store.Store {
	s, e := store.Open(filepath.Join(c.DataDir, "gateway.db"))
	if e != nil {
		fail(e.Error())
	}
	return s
}
func checkDB(path string) {
	s, e := store.Open(path)
	if e != nil {
		fail(e.Error())
	}
	defer s.DB.Close()
	if e = s.Check(); e != nil {
		fail(e.Error())
	}
}
func copyFile(dst *os.File, src string) error {
	f, e := os.Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = io.Copy(dst, f)
	return e
}
func randomSecret() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		fail(e.Error())
	}
	return hex.EncodeToString(b[:])
}
func bytesContainEnv(b []byte) bool {
	return strings.Contains(string(b), "${") || strings.Contains(string(b), "$RCG_")
}
func first(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
func fail(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(2) }
