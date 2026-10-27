package tasks

import (
	"bytes"
	"context"
	"slices"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

// taskLarkOverlay runs only after decoding the original snapshot. It rechecks
// that exact snapshot and its frozen identity under the aggregate lock;
// it never resolves current Agent permissions or account current credentials.
func (s *Source) taskLarkOverlay(ctx context.Context, inst store.TaskInstance) (*clicredential.LarkBinding, int64, error) {
	snap, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return nil, 0, err
	}
	cfg := snap.Configuration()
	var binding *clicredential.LarkBinding
	var revision int64
	_, err = s.Store.MutateTask(ctx, inst.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var task, agent uuid.UUID
		var raw []byte
		var sha string
		var prepared, retired bool
		if err := m.Tx.QueryRow(ctx, `SELECT task_id,agent_id,snapshot,snapshot_sha,prepared,retired FROM task_instances WHERE id=$1 FOR UPDATE`, inst.ID).Scan(&task, &agent, &raw, &sha, &prepared, &retired); err != nil {
			return nil, err
		}
		if task != inst.TaskID || agent != inst.AgentID || !prepared || retired || m.Record.Ended() || !bytes.Equal(raw, inst.Snapshot) {
			return nil, agentconfig.ErrCredentialUnavailable
		}
		if cfg.Prompts != nil && cfg.Prompts.ProjectID != nil && *cfg.Prompts.ProjectID != m.Record.View.Task.ProjectID {
			return nil, agentconfig.ErrCredentialUnavailable
		}
		var err error
		binding, revision, err = s.Store.TaskLarkEffectiveBindingTx(ctx, m, store.TaskLarkMemberCapture{AgentID: agent, RuntimeID: inst.ID, BaseSnapshotSHA: sha})
		if err != nil {
			return nil, err
		}
		if binding != nil && !slices.Contains(cfg.CLIs, "lark-cli") {
			return nil, agentconfig.ErrCredentialUnavailable
		}
		return nil, nil
	})
	return binding, revision, err
}

// EnableLarkOverlay installs the single concrete task-switch resolver at
// composition time. Production/all migrations must install it; fixtures capped
// below migration 21 deliberately omit it. Runtime SQL errors are never ignored.
func (s *Source) EnableLarkOverlay() { s.LarkOverlay = s.taskLarkOverlay }
