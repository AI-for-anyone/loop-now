package local

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// Every startup phase reports the runtime the run is being started on: pi for a Pi Agent, claude-code for a Claude
// Code Agent.
func TestPhasesReportRuntime(t *testing.T) {
	h := newClaudeHarness(t, nil)
	for want, id := range map[string]uuid.UUID{"pi": h.agent(nil), "claude-code": h.claudeAgent(nil)} {
		res, sink := h.exec(id, "hello")
		if res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("%s: %+v", want, res)
		}
		if !slices.Equal(sink.phases, []string{executor.PhasePreparing, executor.PhaseLaunching}) || !slices.Equal(sink.phaseRuntimes, []string{want, want}) {
			t.Fatalf("%s: phases %v, runtimes %v", want, sink.phases, sink.phaseRuntimes)
		}
	}
}
