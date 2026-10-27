package tasks

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"time"
)

// CheckQuestionRunTx is the wait/question counterpart of submit/fail. It is
// invoked before writing a real question, under the existing Inbox task lock.
func (s *Service) CheckQuestionRunTx(ctx context.Context, m *store.TaskMutation, p auth.Principal, node, run uuid.UUID) error {
	if err := nodeActor(p, m, node); err != nil {
		return err
	}
	return s.Store.CheckTaskLarkRunTx(ctx, m, node, p.RuntimeID, run)
}

func (s *Service) BindQuestionTx(ctx context.Context, m *store.TaskMutation, p auth.Principal, nodeID uuid.UUID, q taskmodel.Question) error {
	if err := nodeActor(p, m, nodeID); err != nil {
		return err
	}
	n := m.Record.Current()
	if m.Record.Ended() || n.Status == "blocked" || n.Output != nil {
		return conflict("当前节点不能发起问题")
	}
	if n.Question != nil && n.Question.Status == "open" {
		old, err := store.GetQuestionTx(ctx, m.Tx, n.Question.ID, true, 0)
		if err != nil {
			return err
		}
		if old == nil {
			return conflict("当前节点已有未回答的问题")
		}
		if old.State == "open" && !old.ExpiresAt.After(time.Now()) {
			if _, err = m.Tx.Exec(ctx, `UPDATE inbox_questions SET state='expired',expired_reason='deadline',notice_state='pending' WHERE message_id=$1`, n.Question.ID); err != nil {
				return err
			}
			if err = store.QueueQuestionEventTx(ctx, m.Tx, n.Question.ID, "expired"); err != nil {
				return err
			}
			old.State = "expired"
		}
		if old.State == "open" {
			return conflict("当前节点已有未回答的问题")
		}
		n.Question.Status = old.State
		n.Question.CanAnswer = false
	}
	q.CanAnswer = false
	n.Question = &q
	m.Changed()
	return m.Emit("node.updated", map[string]any{"node": n})
}

// SyncQuestionTx updates history only; the same Inbox transaction owns the sole
// recovery reminder. It never recursively enters MutateTask.
func (s *Service) SyncQuestionTx(ctx context.Context, m *store.TaskMutation, id uuid.UUID, status string) (bool, error) {
	for i := range m.Record.View.Nodes {
		n := &m.Record.View.Nodes[i]
		if n.Question == nil || n.Question.ID != id {
			continue
		}
		if n.Question.Status != "open" {
			return false, nil
		}
		n.Question.Status = status
		n.Question.CanAnswer = false
		m.Changed()
		wake := !m.Record.Ended() && m.Record.Current() != nil && m.Record.Current().ID == n.ID && n.Status != "blocked" && status != "closed"
		return wake, m.Emit("node.updated", map[string]any{"node": n})
	}
	return false, nil
}
