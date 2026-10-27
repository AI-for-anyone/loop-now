package tasks

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/store"
)

// ReconcileTaskLarkInbox drains only persisted messages for their original
// runtime after its handoff is conclusively terminal. Notification delivery
// and the marker commit together; no read, new message or Manager fallback.
func (s *Service) ReconcileTaskLarkInbox(ctx context.Context, task uuid.UUID) error {
	runtimes := map[uuid.UUID]bool{}
	_, err := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		if m.Record.Ended() {
			return nil, nil
		}
		rows, err := m.Tx.Query(ctx, `SELECT d.message_id,d.runtime_id,i.agent_id,msg.when_busy FROM task_lark_deferred_inbox d JOIN task_lark_switch_actions a ON a.actor_key=d.actor_key AND a.action_id=d.action_id AND a.task_id=d.task_id JOIN task_instances i ON i.id=d.runtime_id AND i.task_id=d.task_id JOIN inbox_messages msg ON msg.id=d.message_id AND msg.to_runtime_id=d.runtime_id AND msg.to_agent_id=i.agent_id WHERE d.task_id=$1 AND d.delivered_at IS NULL AND a.status IN ('succeeded','failed') AND NOT i.retired AND NOT EXISTS(SELECT 1 FROM task_lark_switch_fences f WHERE f.task_id=d.task_id AND f.runtime_id=d.runtime_id) ORDER BY msg.created_at,d.message_id FOR UPDATE OF d`, task)
		if err != nil {
			return nil, err
		}
		messages, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.NewInboxMessage, error) {
			var msg store.NewInboxMessage
			msg.To.Kind = "agent"
			err := row.Scan(&msg.ID, &msg.To.RuntimeID, &msg.To.ID, &msg.WhenBusy)
			return msg, err
		})
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			if err = s.DeliverInboxTx(ctx, m.Tx, m, msg); err != nil {
				return nil, err
			}
			if _, err = m.Tx.Exec(ctx, `UPDATE task_lark_deferred_inbox SET delivered_at=clock_timestamp() WHERE message_id=$1 AND delivered_at IS NULL`, msg.ID); err != nil {
				return nil, err
			}
			runtimes[msg.To.RuntimeID] = true
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	for runtime := range runtimes {
		s.InboxCommitted(ctx, runtime)
	}
	return nil
}
