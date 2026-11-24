package codexapp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"loop-now/internal/server/executor/local/codexapp"
)

// TestRealAppServer checks the protocol assumptions against the Codex installed on this computer, without a
// conversation and without the user's login: a temporary Codex home, initialize, a thread start (prompts off, full
// access, which is refused in the granular form without the experimental API), and a thread that was never saved is
// not found. It runs only when LOOP_NOW_TEST_CODEX_BIN names the codex executable (CI skips it).
func TestRealAppServer(t *testing.T) {
	bin := os.Getenv("LOOP_NOW_TEST_CODEX_BIN")
	if bin == "" {
		t.Skip("LOOP_NOW_TEST_CODEX_BIN is not set")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	work, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(work+"/AGENTS.md", []byte("# Probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.Dir = work
	c, err := codexapp.Start(cmd, codexapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c.CloseStdin(); <-c.Done() }()
	init, err := c.Initialize(ctx(t), "loop-now-test", "0.0.0")
	if err != nil || init.CodexHome != home {
		t.Fatalf("initialize: %+v %v", init, err)
	}
	thread, err := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: work, DeveloperInstructions: "probe",
		Config: map[string]any{"mcp_servers.probe": map[string]any{"command": "/nonexistent/probe-mcp"}}})
	if err != nil || thread == "" {
		t.Fatalf("thread/start: %q %v", thread, err)
	}
	if err := c.SetSkillRoots(ctx(t), []string{work}); err != nil {
		t.Fatal(err)
	}
	if err := c.ResumeThread(ctx(t), thread, codexapp.ThreadOptions{Cwd: work}); err == nil {
		t.Log("resuming the loaded thread succeeded")
	}
	other := exec.Command(bin, "app-server")
	other.Env = cmd.Env
	c2, err := codexapp.Start(other, codexapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c2.CloseStdin(); <-c2.Done() }()
	if _, err := c2.Initialize(ctx(t), "loop-now-test", "0.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := c2.ResumeThread(ctx(t), thread, codexapp.ThreadOptions{Cwd: work}); !codexapp.IsThreadNotFound(err) {
		t.Fatalf("a thread without turns is not saved: %v", err)
	}
	if err := c2.Archive(ctx(t), thread); !codexapp.IsThreadNotFound(err) {
		t.Fatalf("archiving a thread that was never saved: %v", err)
	}
}
