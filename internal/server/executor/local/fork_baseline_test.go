package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// The Pi driver reads a task fork baseline only from a complete session in the Agent's own sessions directory: the
// header first, every line complete JSON, a plain file (no symlink out of the directory), within the size limit.
// Moved from tasks with the code it tests; the checks are unchanged.
func TestBaselineCopiesCompleteSessionAndRejectsInvalidPaths(t *testing.T) {
	e := &Executor{o: Options{WorkRoot: t.TempDir()}}
	pd := piDriver{e: e}
	agentID := uuid.New()
	dir := e.dirs(agentID).sessions
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "source.jsonl")
	valid := []byte("{\"type\":\"session\",\"id\":\"fixture\"}\n{\"type\":\"message\",\"id\":\"m\"}\n")
	if err := os.WriteFile(session, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(path string) ([]byte, error) { return pd.forkBaseline(context.Background(), agentID, path, 4096) }
	if got, err := read(session); err != nil || !bytes.Equal(got, valid) {
		t.Fatal("baseline copy differs", err)
	}
	for _, invalid := range []string{"", " \n", "{\"type\":\"message\"}\n", "{\"type\":\"session\"}\n{broken"} {
		if err := os.WriteFile(session, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := read(session); err == nil {
			t.Fatal("invalid baseline accepted", invalid)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(outside); err == nil {
		t.Fatal("other directory accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := read(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.WriteFile(session, bytes.Repeat([]byte("a"), 4097), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(session); err == nil {
		t.Fatal("oversize baseline accepted")
	}
}

// The Claude Code driver reads a task fork baseline only from the <session ID>.jsonl one level below claude's session
// record directory; rejected when it is not there, its name is not a session ID, it has an incomplete line, or it
// exceeds the limit; rejected when the session record directory cannot be found (no readable home). Moved from tasks
// with the code it tests; the checks are unchanged.
func TestCopyClaudeBaseline(t *testing.T) {
	home := t.TempDir()
	e := &Executor{o: Options{WorkRoot: t.TempDir(), ClaudeBin: filepath.Join(t.TempDir(), "no-claude"), RealHome: func() (string, error) { return home, nil }}}
	cd := claudeDriver{e: e}
	projects := filepath.Join(home, ".claude", "projects")
	if got := e.ClaudeProjectsDir(context.Background()); got != projects {
		t.Fatalf("session record directory without claude: %q", got)
	}
	id := uuid.NewString()
	dir := filepath.Join(projects, "-work-agents-x-workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(session, []byte("{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(path string) ([]byte, error) {
		return cd.forkBaseline(context.Background(), uuid.New(), path, 1<<10)
	}
	if b, err := read(session); err != nil || string(b) != "{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n" {
		t.Fatalf("baseline content: %q %v", b, err)
	}
	outside := filepath.Join(t.TempDir(), id+".jsonl")
	_ = os.WriteFile(outside, []byte("{}\n"), 0o600)
	notID := filepath.Join(dir, "notes.jsonl")
	_ = os.WriteFile(notID, []byte("{}\n"), 0o600)
	broken := filepath.Join(dir, uuid.NewString()+".jsonl")
	_ = os.WriteFile(broken, []byte("{\"type\":\"user\"}\n{\"type\":"), 0o600)
	big := filepath.Join(dir, uuid.NewString()+".jsonl")
	_ = os.WriteFile(big, []byte(strings.Repeat("{}\n", 1000)), 0o600)
	for name, path := range map[string]string{"not in the session record directory": outside, "name is not a session ID": notID, "incomplete line": broken, "over the size limit": big, "nested too deep": filepath.Join(dir, "x", id+".jsonl")} {
		if _, err := read(path); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	e.o.RealHome = func() (string, error) { return "", errors.New("no home") }
	if _, err := read(session); err == nil {
		t.Fatal("should be rejected when no session record directory is configured")
	}
}

// ForkBaseline goes to the driver of the runtime it is given; a runtime with no driver is not supported.
func TestForkBaselineByRuntime(t *testing.T) {
	e := &Executor{o: Options{WorkRoot: t.TempDir()}}
	if _, err := e.ForkBaseline(context.Background(), "grok", uuid.New(), "/x.jsonl", 10); !errors.Is(err, executor.ErrUnsupportedRuntime) {
		t.Fatalf("unknown runtime: %v", err)
	}
	if _, err := e.ForkBaseline(context.Background(), "pi", uuid.New(), "/x.jsonl", 10); err == nil || errors.Is(err, executor.ErrUnsupportedRuntime) {
		t.Fatalf("pi should check the session itself: %v", err)
	}
}
