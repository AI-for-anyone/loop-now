package tasks

import (
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

// An instance takes the prompt of the step it is doing: its current node's, otherwise its latest node's; an instance
// that has run no node yet, or a step without a prompt, has none.
func TestStepPromptOfTheInstancesStep(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	r := taskmodel.Record{View: taskmodel.Detail{Workflow: workflow.Definition{Steps: []workflow.Step{
		{ID: "build", Prompt: "Build briefing"}, {ID: "check", Prompt: "Check briefing"}, {ID: "pr", Prompt: "PR briefing"}, {ID: "bare"},
	}}}}
	add := func(step string, runtime uuid.UUID) {
		n := taskmodel.NodeRun{ID: uuid.New(), StepID: step, RuntimeID: runtime}
		r.View.Nodes = append(r.View.Nodes, n)
		r.SetCurrent(n)
	}
	if got := stepPrompt(&r, a); got != "" {
		t.Fatalf("no node yet: %q", got)
	}
	add("build", a)
	if got := stepPrompt(&r, a); got != "Build briefing" {
		t.Fatalf("current node: %q", got)
	}
	add("check", b)
	if got, other := stepPrompt(&r, a), stepPrompt(&r, b); got != "Build briefing" || other != "Check briefing" {
		t.Fatalf("not current: the instance's latest node: %q %q", got, other)
	}
	add("pr", a)
	if got := stepPrompt(&r, a); got != "PR briefing" {
		t.Fatalf("the instance moved to its next step: %q", got)
	}
	add("bare", a)
	if got := stepPrompt(&r, a); got != "" {
		t.Fatalf("a step without a prompt: %q", got)
	}
}
