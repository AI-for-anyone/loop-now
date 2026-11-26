package taskmodel

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/workflow"
)

func twoSteps() workflow.Definition {
	return workflow.Definition{StartStepID: "implement", Steps: []workflow.Step{{ID: "implement", Title: "实现"}, {ID: "verify", Title: "Verify"}}} // check-english: zh (a step title in the person's language)
}

// SetCurrent moves the step id and title together.
func TestSetCurrentGivesTheStepTitle(t *testing.T) {
	r := Record{View: Detail{Workflow: twoSteps()}}
	for _, step := range []struct{ id, title string }{{"implement", "实现"}, {"verify", "Verify"}} { // check-english: zh (a step title in the person's language)
		n := NodeRun{ID: uuid.New(), StepID: step.id, AgentID: uuid.New()}
		r.SetCurrent(n)
		task := r.View.Task
		if *task.CurrentNodeRunID != n.ID || *task.CurrentStepID != step.id || *task.CurrentAgentID != n.AgentID {
			t.Fatalf("current = %+v, want node %s on %s", task, n.ID, step.id)
		}
		if task.CurrentStepTitle == nil || *task.CurrentStepTitle != step.title {
			t.Fatalf("currentStepTitle = %v, want %q", task.CurrentStepTitle, step.title)
		}
	}
}

// A stored view gets the title from its workflow: views stored before the field existed, and a step id the workflow
// does not have (null); a view without a current step keeps null.
func TestDetailFillsCurrentStepTitle(t *testing.T) {
	wf, _ := json.Marshal(twoSteps())
	for _, c := range []struct {
		task string
		want *string
	}{
		{`{"currentStepId":"verify"}`, ptr("Verify")},
		{`{"currentStepId":"verify","currentStepTitle":"Old title"}`, ptr("Verify")},
		{`{"currentStepId":"gone"}`, nil},
		{`{"currentStepId":null}`, nil},
	} {
		var d Detail
		if err := json.Unmarshal([]byte(`{"task":`+c.task+`,"workflow":`+string(wf)+`}`), &d); err != nil {
			t.Fatal(err)
		}
		got := d.Task.CurrentStepTitle
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Fatalf("%s: currentStepTitle = %v, want %v", c.task, got, c.want)
		}
	}
}

func ptr(s string) *string { return &s }
