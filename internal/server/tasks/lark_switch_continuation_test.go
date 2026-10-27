package tasks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/taskmodel"
)

func TestLarkSwitchContinuationPreservesHistoryAndDoesNotPrepare(t *testing.T) {
	task, node, runtime, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	old := taskmodel.NodeRun{ID: node, TaskID: task, RuntimeID: runtime, AgentID: agent, StepID: "arbitrary-step", Visit: 3, Attempt: 2, Status: "blocked", Inputs: map[string]any{"original": "full step input"}, Output: map[string]any{"already": "deployed"}, RunIDs: []uuid.UUID{uuid.New()}}
	artifact := taskmodel.Artifact{ID: uuid.New(), TaskID: task, NodeRunID: node, Name: "original outcome", SHA256: strings.Repeat("a", 64)}
	record := &taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: task, Status: "blocked", CurrentNodeRunID: &node}, Nodes: []taskmodel.NodeRun{old}, Artifacts: []taskmodel.Artifact{artifact}}}
	before, _ := json.Marshal(record)
	action, run := uuid.New(), uuid.New()
	next, prompt, err := newTaskLarkContinuation(record, action, run, 7, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == node || next.TaskID != task || next.StepID != old.StepID || next.Visit != old.Visit || next.Attempt != old.Attempt+1 || next.RuntimeID != runtime || next.AgentID != agent || len(next.RunIDs) != 1 || next.RunIDs[0] != run || next.Output != nil || next.Question != nil || next.Sleep != nil {
		t.Fatal("continuation reused old execution result or changed scope")
	}
	for _, ref := range []string{action.String(), node.String(), next.ID.String(), artifact.ID.String(), artifact.SHA256, "禁止重新运行build/deploy", "必须由本轮真实提交"} {
		if !strings.Contains(prompt, ref) {
			t.Fatal("continuation lacks original provenance or recovery boundary")
		}
	}
	if strings.Contains(prompt, "full step input") {
		t.Fatal("continuation reconstructed whole-step initial prompt")
	}
	next.Inputs["original"] = "new value"
	after, _ := json.Marshal(record)
	if string(before) != string(after) {
		t.Fatal("new attempt changed old record")
	}
}
