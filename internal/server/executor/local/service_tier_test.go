package local

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	authhelper "loop-now/auth-helper"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

func TestServiceTierExecutorRebuildsAndResumesFixedParameter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private")
	if err = os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	_, entry, err := authhelper.Install(filepath.Join(private, "helper"))
	if err != nil {
		t.Fatal(err)
	}
	entry = filepath.Join(filepath.Dir(entry), "codex-service-tier.mjs")
	h := newHarness(t, func(o *Options) { o.CodexServiceTierExtension = entry }, "")
	dump := filepath.Join(root, "dump.json")
	h.env["FAKEPI_DUMP"] = dump
	tier := "priority"
	id := h.agent(func(s *agentconfig.Snapshot) { s.Provider = "openai-codex"; s.ServiceTier = &tier })
	if result, _ := h.exec(id, "first"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	firstPID := h.pid(id)
	first := readDump(t, dump)
	if first.Env["LOOP_NOW_CODEX_SERVICE_TIER"] != "priority" || !slices.Contains(first.Args, entry) {
		t.Fatal("captured speed missing from actual launch")
	}
	h.src.mu.Lock()
	session := h.src.recorded[id]
	h.src.mu.Unlock()
	if session == "" {
		t.Fatal("missing session")
	}
	// Saving standard changes configVersion, replacing the old process while
	// retaining the same runtime/session, rather than inheriting its old speed.
	h.src.update(id, func(s *agentconfig.Snapshot) { standard := "default"; s.ServiceTier = &standard })
	if result, _ := h.exec(id, "second"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	next := readDump(t, dump)
	if h.pid(id) == firstPID || next.Env["LOOP_NOW_CODEX_SERVICE_TIER"] != "default" || !slices.Contains(next.Args, session) {
		t.Fatal("standard reset failed to rebuild/resume")
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Provider = "anthropic"; s.ServiceTier = nil })
	if result, _ := h.exec(id, "third"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	nonCodex := readDump(t, dump)
	if _, present := nonCodex.Env["LOOP_NOW_CODEX_SERVICE_TIER"]; present || slices.Contains(nonCodex.Args, entry) {
		t.Fatal("non-Codex got speed injection")
	}
}

func TestServiceTierExecutorRejectsUnavailableAdapter(t *testing.T) {
	for _, tier := range []string{"priority", "invalid"} {
		t.Run(tier, func(t *testing.T) {
			h := newHarness(t, nil, "")
			id := h.agent(func(s *agentconfig.Snapshot) { s.Provider = "openai-codex"; s.ServiceTier = &tier })
			result, _ := h.exec(id, "never prompt")
			if result.ErrorCode != executor.ErrPiStartFailed || h.pid(id) != 0 {
				t.Fatal("missing/invalid adapter silently used standard", result)
			}
		})
	}
}
