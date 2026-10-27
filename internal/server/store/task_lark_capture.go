package store

import (
	"context"
	"os"
	"slices"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskruntime"
)

// TaskLarkMemberCapture contains only original, local authorization facts.
// It never resolves current Agent configuration or returns credential bytes.
type TaskLarkMemberCapture struct {
	AgentID, RuntimeID         uuid.UUID
	Role                       string
	PreviousRevision           int64
	BaseSnapshotSHA            string
	OriginalCredentialIdentity string
	Prepared                   bool
	SessionFile                *string
}

// CaptureTaskLarkMemberTx validates the original snapshot before any
// overlay is considered. A later overlay cannot repair it or add a CLI.
func (s *Store) CaptureTaskLarkMemberTx(ctx context.Context, m *TaskMutation, member TaskLarkFenceMember) (TaskLarkMemberCapture, error) {
	var out TaskLarkMemberCapture
	if member.Role != "publisher" && member.Role != "followup" {
		return out, apperr.Invalid("members", "任务成员角色无效")
	}
	var task, agent uuid.UUID
	var snapshot []byte
	var snapshotSHA string
	var retired bool
	var sessionState string
	err := m.Tx.QueryRow(ctx, `SELECT task_id,agent_id,snapshot,snapshot_sha,prepared,retired,session_state,session_file FROM task_instances WHERE id=$1 FOR UPDATE`, member.RuntimeID).Scan(&task, &agent, &snapshot, &snapshotSHA, &out.Prepared, &retired, &sessionState, &out.SessionFile)
	if err != nil {
		return out, notFoundIfNoRows(err, "任务实例")
	}
	deny := func() (TaskLarkMemberCapture, error) {
		return TaskLarkMemberCapture{}, apperr.Conflict("task_conflict", "原任务快照授权或会话不可用")
	}
	if task != m.Record.View.Task.ID || agent != member.AgentID || retired {
		return deny()
	}
	snap, err := taskruntime.Decode(snapshot)
	if err != nil || snap.Scope().TaskID() != task || snap.Scope().AgentID() != agent {
		return deny()
	}
	cfg := snap.Configuration()
	if !slices.Contains(cfg.CLIs, "lark-cli") {
		return deny()
	}
	if cfg.Prompts != nil && cfg.Prompts.ProjectID != nil && *cfg.Prompts.ProjectID != m.Record.View.Task.ProjectID {
		return deny()
	}
	if _, err := snap.Credentials(); err != nil {
		return deny()
	}
	var hadTurn bool
	if err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE task_id=$1 AND runtime_id=$2)`, task, member.RuntimeID).Scan(&hadTurn); err != nil {
		return out, err
	}
	// Source prepares all frozen directories before the workflow starts. That
	// does not create a Pi session for a future member. Only proven never-run
	// followups may retain this original none/nil state; prepared is not erased.
	future := member.Role == "followup" && !hadTurn && sessionState == "none" && out.SessionFile == nil
	if !future {
		if !out.Prepared || sessionState != "active" || out.SessionFile == nil || *out.SessionFile == "" {
			return deny()
		}
		info, err := os.Stat(*out.SessionFile)
		if err != nil || !info.Mode().IsRegular() {
			return deny()
		}
	}
	out.AgentID, out.RuntimeID = agent, member.RuntimeID
	out.Role = member.Role
	out.PreviousRevision, err = s.TaskLarkMemberRevisionTx(ctx, m, member.RuntimeID)
	if err != nil {
		return TaskLarkMemberCapture{}, err
	}
	out.BaseSnapshotSHA = snapshotSHA
	out.OriginalCredentialIdentity = cfg.CredentialIdentity
	return out, nil
}
