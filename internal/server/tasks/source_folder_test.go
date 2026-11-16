package tasks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/folders"
	"loop-now/internal/server/store"
)

// When preparation is retried, the workspace left by the last failure and the partial copy are deleted first, and then
// the base Agent's folder is copied in full.
func TestCopyWorkspaceReplacesLeftovers(t *testing.T) {
	work := t.TempDir()
	s := &Source{WorkRoot: work}
	inst := store.TaskInstance{ID: uuid.New(), AgentID: uuid.New(), TaskID: uuid.New()}
	base := folders.AgentWorkspace(work, inst.AgentID)
	if err := os.MkdirAll(filepath.Join(base, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "docs", "a.md"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := folders.AgentWorkspace(work, inst.ID)
	for _, p := range []string{filepath.Join(dst, "stale.txt"), filepath.Join(dst+".copying-abc", "half.txt")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("leftover"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.copyWorkspace(inst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "docs", "a.md")); err != nil || string(b) != "base" {
		t.Fatalf("should copy the base folder: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Fatal("workspace content left over from last time should be deleted")
	}
	if _, err := os.Stat(dst + ".copying-abc"); !os.IsNotExist(err) {
		t.Fatal("a half-finished copy should be deleted")
	}
}
