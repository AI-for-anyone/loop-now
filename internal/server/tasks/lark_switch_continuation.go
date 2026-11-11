package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/taskmodel"
)

// newTaskLarkContinuation creates a new execution boundary, not a general
// retry/prepareNode prompt. The caller publishes it only in final transaction C.
func newTaskLarkContinuation(record *taskmodel.Record, action, run uuid.UUID, revision int64, confirmedAt time.Time) (taskmodel.NodeRun, string, error) {
	var next taskmodel.NodeRun
	old := record.Current()
	if old == nil || record.Ended() || record.View.Task.Status != "blocked" || old.Status != "blocked" || action == uuid.Nil || run == uuid.Nil || revision < 1 || confirmedAt.IsZero() {
		return next, "", errors.New("原阻塞节点或恢复来源无效")
	}
	// Keep independent copies of captured input facts; old output, question,
	// sleep and execution results cannot qualify the new continuation.
	raw, err := json.Marshal(old)
	if err != nil {
		return next, "", err
	}
	if err = json.Unmarshal(raw, &next); err != nil {
		return next, "", err
	}
	next.ID = uuid.Must(uuid.NewV7())
	next.Attempt++
	next.Status = "queued"
	next.Output, next.EdgeID, next.Reason, next.Question = nil, nil, nil, nil
	next.Sleep, next.SleepRevision = nil, 0
	next.StartedAt, next.EndedAt = nil, nil
	next.RunIDs = []uuid.UUID{run}
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Platform resume notice: at %s the console explicitly confirmed resuming the remaining QA.\nSource actionId: %s; original taskId: %s; original nodeRunId: %s; new nodeRunId: %s; bindingRevision: %d.\n", confirmedAt.UTC().Format(time.RFC3339Nano), action, record.View.Task.ID, old.ID, next.ID, revision)
	prompt.WriteString("Keep the original task, step, visit, runtime, directories and session, and keep the deployments and artifacts already completed. Do not rerun build/deploy and do not resend QA submissions already confirmed as sent; only continue the remaining QA submissions the user confirmed and the follow-up of their results in the same task. When an external result is unknown, check the original records first and do not resend automatically.\nThis is a separate new attempt/run/turn; the original failed turn, the old Output and the old submissions are not evidence that this turn is complete, and only a real submission in this turn can advance it. Later failures follow the existing task rules; do not go back to the earlier deployment.\n")
	prompt.WriteString("Below are references to the original task's persisted artifacts; they do not mean this turn has re-executed or verified anything:\n")
	for _, artifact := range record.View.Artifacts {
		if artifact.TaskID != record.View.Task.ID {
			return taskmodel.NodeRun{}, "", errors.New("原产物作用域不匹配")
		}
		ref, err := json.Marshal(struct {
			ID, NodeRunID uuid.UUID
			Name, SHA256  string
		}{artifact.ID, artifact.NodeRunID, artifact.Name, artifact.SHA256})
		if err != nil {
			return taskmodel.NodeRun{}, "", err
		}
		fmt.Fprintf(&prompt, "%s\n", ref)
	}
	return next, prompt.String(), nil
}
