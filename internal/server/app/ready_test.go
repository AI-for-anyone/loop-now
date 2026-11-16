package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
)

// The ready file is written whole (creating the directory if missing); writing it again replaces it entirely; no
// temporary files are left in the directory.
func TestWriteReadyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell", "ready.json")
	for _, url := range []string{"http://127.0.0.1:1", "http://127.0.0.1:2"} {
		if err := app.WriteReadyFile(path, app.Ready{URL: url, PID: 42, Version: "v"}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		var r app.Ready
		if err != nil || json.Unmarshal(b, &r) != nil || r != (app.Ready{URL: url, PID: 42, Version: "v"}) {
			t.Fatalf("unexpected ready file content: %s %v", b, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// The console directory must contain index.html; with no directory configured and nothing embedded in the binary, there
// is no console.
func TestConsoleFiles(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := app.ConsoleFiles(dir); err == nil || !strings.Contains(err.Error(), config.EnvWebDir) {
		t.Fatalf("a directory without index.html should be rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fsys, source, err := app.ConsoleFiles(dir); err != nil || fsys == nil || source != dir {
		t.Fatalf("%v %q %v", fsys, source, err)
	}
	if fsys, _, err := app.ConsoleFiles(""); err != nil || fsys != nil {
		t.Fatalf("a build without embedweb should have no console: %v %v", fsys, err)
	}
}

// Real server: the address in the ready file is the console port, which serves both the console and the API; the ready
// file is deleted after stopping.
func TestConsoleAndReadyFileOnRealServer(t *testing.T) {
	w := newWorld(t)
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<title>loop now console</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready.json")
	s := w.start(func(c *config.Config) { c.WebDir, c.ReadyFile = web, ready })
	var r app.Ready
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(ready); err == nil && json.Unmarshal(b, &r) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.URL != "http://"+s.a.Addr() || r.PID != os.Getpid() {
		t.Fatalf("unexpected ready file: %+v", r)
	}
	for path, want := range map[string]string{"/": "loop now console", "/agents/x": "loop now console", "/api/v1/context": `"project"`} {
		resp, err := http.Get(r.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d %.80s", path, resp.StatusCode, b)
		}
	}
	s.stop()
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("the ready file should be removed after stopping: %v", err)
	}
}
