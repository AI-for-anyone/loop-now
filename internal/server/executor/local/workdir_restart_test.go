package local

import (
	"testing"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// The working folder an instance's runtime started in is part of what the process stands for: a dispatch in another
// folder (a step of a parallel branch, which works in its branch's worktree) restarts the runtime there; the same folder
// keeps the process.
func TestWorkingFolderChangeRestarts(t *testing.T) {
	h := newHarness(t, nil, "")
	first, second := t.TempDir(), t.TempDir()
	id := h.agent(func(s *agentconfig.Snapshot) { cleanStep(s); s.TaskWorkspace = first })
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	pid := h.pid(id)
	if res, _ := h.exec(id, "again"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatal("the same folder keeps the process")
	}
	h.src.mu.Lock()
	h.src.snaps[id].TaskWorkspace = second
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "elsewhere"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("another working folder restarts the runtime")
	}
}
