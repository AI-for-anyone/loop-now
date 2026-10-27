package tasks

import (
	"bytes"
	"context"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/store"
)

// Preparation must not finish after a fence/revision change with an old
// normal-session fork. This does not prepare a followup as part of switching.
func (s *Source) taskPreparationCheckTx(ctx context.Context, m *store.TaskMutation, inst store.TaskInstance, expected *int64) (bool, int64, error) {
	m.Unchanged()
	var task, agent uuid.UUID
	var raw []byte
	var prepared, retired bool
	if err := m.Tx.QueryRow(ctx, `SELECT task_id,agent_id,snapshot,prepared,retired FROM task_instances WHERE id=$1 FOR UPDATE`, inst.ID).Scan(&task, &agent, &raw, &prepared, &retired); err != nil {
		return false, 0, err
	}
	if task != inst.TaskID || agent != inst.AgentID || task != m.Record.View.Task.ID || retired || m.Record.Ended() || !bytes.Equal(raw, inst.Snapshot) {
		return false, 0, agentconfig.ErrCredentialUnavailable
	}
	if s.LarkOverlay == nil {
		return prepared, 0, nil
	}
	if fenced, err := s.Store.TaskLarkRuntimeFencedTx(ctx, m, inst.ID); err != nil || fenced {
		if err != nil {
			return false, 0, err
		}
		return false, 0, conflict("原实例正在凭证交接，未准备新副本")
	}
	revision, err := s.Store.TaskLarkMemberRevisionTx(ctx, m, inst.ID)
	if err != nil {
		return false, 0, err
	}
	if expected != nil && revision != *expected {
		return false, 0, conflict("准备期间固定任务绑定已变化，旧准备结果未发布")
	}
	return prepared, revision, nil
}

func (s *Source) taskPreparationRevision(ctx context.Context, inst store.TaskInstance) (bool, int64, error) {
	var prepared bool
	var revision int64
	_, err := s.Store.MutateTask(ctx, inst.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		var e error
		prepared, revision, e = s.taskPreparationCheckTx(ctx, m, inst, nil)
		return nil, e
	})
	return prepared, revision, err
}

func (s *Source) commitTaskPreparation(ctx context.Context, inst store.TaskInstance, revision int64, base *string, sources []byte) error {
	_, err := s.Store.MutateTask(ctx, inst.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		prepared, _, err := s.taskPreparationCheckTx(ctx, m, inst, &revision)
		if err != nil || prepared {
			return nil, err
		}
		_, err = m.Tx.Exec(ctx, `UPDATE task_instances SET fork_source=$2,sources=$3,prepared=true WHERE id=$1 AND NOT retired`, inst.ID, base, sources)
		return nil, err
	})
	return err
}
