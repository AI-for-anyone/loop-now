package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/store"
)

// The baseline a task instance forks from is written to the platform's own task-baselines/<task>/<Agent>.jsonl as a
// copy of what the runtime's driver read (ending in one newline); a later change to the source does not reach it. What
// the drivers accept is tested in executor/local (fork_baseline_test.go).
func TestWriteBaseline(t *testing.T) {
	src := &Source{WorkRoot: t.TempDir(), MaxSessionBytes: 4096}
	inst := store.TaskInstance{ID: uuid.New(), AgentID: uuid.New(), TaskID: uuid.New()}
	valid := []byte("{\"type\":\"session\",\"id\":\"fixture\"}\n{\"type\":\"message\",\"id\":\"m\"}\n")
	read := bytes.Clone(valid)
	target, e := src.writeBaseline(inst, read)
	if e != nil || filepath.Dir(target) != filepath.Join(src.WorkRoot, "task-baselines", inst.TaskID.String()) || filepath.Base(target) != inst.AgentID.String()+".jsonl" {
		t.Fatal("baseline target", target, e)
	}
	copy, e := os.ReadFile(target)
	if e != nil || !bytes.Equal(copy, valid) {
		t.Fatal("baseline copy differs", e)
	}
	copy[0] = 'x'
	read[0] = 'x'
	if again, _ := os.ReadFile(target); !bytes.Equal(again, valid) {
		t.Fatal("baseline aliases source")
	}
}

// Archiving a task instance's session: Claude Code's session record is in claude's session record directory, and only
// that file in the subdirectory corresponding to this instance's workspace is accepted.
func TestArchiveClaudeSession(t *testing.T) {
	work, projects := t.TempDir(), t.TempDir()
	src := &Source{WorkRoot: work, MaxSessionBytes: 1 << 10, ClaudeProjects: func(context.Context) string { return projects }}
	inst := store.TaskInstance{ID: uuid.New(), TaskID: uuid.New(), AgentID: uuid.New(), SessionState: "active"}
	workspace := filepath.Join(work, "agents", inst.ID.String(), "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(dir string) string {
		p := filepath.Join(projects, dir, uuid.NewString()+".jsonl")
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("{\"type\":\"user\"}\n"), 0o600)
		return p
	}
	own := write(claudecli.ProjectDirName(workspace))
	inst.SessionFile = &own
	data, err := src.archiveSession(context.Background(), inst)
	var archived struct {
		Session []byte `json:"session"`
	}
	if err != nil || json.Unmarshal(data, &archived) != nil || string(archived.Session) != "{\"type\":\"user\"}\n" {
		t.Fatalf("archive: %s %v", data, err)
	}
	// Also accepted when claude encodes the working directory path as is (not verified on Windows).
	if names := claudecli.ProjectDirNames(workspace); len(names) > 1 {
		raw := write(names[1])
		inst.SessionFile = &raw
		if _, err := src.archiveSession(context.Background(), inst); err != nil {
			t.Fatalf("session record with the path encoded as is: %v", err)
		}
	}
	other := write("-some-other-workspace")
	inst.SessionFile = &other
	if _, err := src.archiveSession(context.Background(), inst); err == nil {
		t.Fatal("a session record from another workspace must not be archived for this instance")
	}
}
