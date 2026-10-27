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
	fmt.Fprintf(&prompt, "平台恢复通知：控制台于%s明确确认剩余QA恢复。\n来源actionId：%s；原taskId：%s；原nodeRunId：%s；新nodeRunId：%s；bindingRevision：%d。\n", confirmedAt.UTC().Format(time.RFC3339Nano), action, record.View.Task.ID, old.ID, next.ID, revision)
	prompt.WriteString("保留原任务、步骤、visit、runtime、目录及会话，保留已经完成的部署与产物。禁止重新运行build/deploy或重新发送已确认发送的提测；只继续用户已确认的剩余QA提测及同任务结果跟进。外部结果未知时先核原记录，不自动重发。\n本次是独立的新attempt/run/turn；原失败轮次、旧Output和旧提交不作为本轮完成依据，必须由本轮真实提交才能推进。后续失败沿既有任务规则处理，不返回前序部署。\n")
	prompt.WriteString("以下为原任务持久产物引用，不代表本轮已重新执行或验证：\n")
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
