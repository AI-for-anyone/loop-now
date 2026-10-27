package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

func (s *Store) ReplyTaskNodeInbox(ctx context.Context, p Party, id, run uuid.UUID, text, whenBusy string) (InboxMessage, bool, error) {
	var out InboxMessage
	if whenBusy == "" {
		whenBusy = "queue"
	}
	handled, e := s.TaskNodeInboxAccess(ctx, p, id, run, func(m *TaskMutation, b TaskNodeInboxBinding) error {
		orig, e := scanInbox(m.Tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, id))
		if e != nil {
			return e
		}
		if p.Kind == "console" {
			if b.Inactive {
				return TaskConsoleConflict(m, b.Target, "chat_unavailable")
			}
			rec, _, e := s.enqueueTaskNodeInboxTx(ctx, m, b.Target, text, whenBusy, &orig)
			if e != nil {
				return e
			}
			// The enqueue projection already contains stable IDs; fetch this actual row.
			var wire struct {
				ID uuid.UUID `json:"id"`
			}
			if e = decodeTaskJSON(rec.Message, &wire); e != nil {
				return e
			}
			out, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, wire.ID))
			return e
		}
		reply := NewInboxMessage{ID: uuid.Must(uuid.NewV7()), ThreadID: &orig.ThreadID, InReplyTo: &id, From: p, To: ConsoleParty, Text: text}
		if e = InsertInboxMessage(ctx, m.Tx, reply); e != nil {
			return e
		}
		if _, e = m.Tx.Exec(ctx, `INSERT INTO task_node_inbox_messages(message_id,root_message_id,task_id,node_run_id,agent_id,runtime_id,execution_mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, reply.ID, b.RootID, b.Target.TaskID, b.Target.NodeRunID, b.Target.AgentID, b.Target.RuntimeID, b.Target.ExecutionMode); e != nil {
			return e
		}
		if e = s.LinkTaskNodeInboxRunTx(ctx, m, id, run, "reply"); e != nil {
			return e
		}
		if e = s.LinkTaskNodeInboxRunTx(ctx, m, reply.ID, run, "reply"); e != nil {
			return e
		}
		out, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, reply.ID))
		return e
	})
	return out, handled, e
}

// TaskNodeInboxThread reads the complete thread under the original task lock.
// mark only changes messages actually addressed to this participant. Console
// history may be read after target expiry and never changes Agent receipts.
func (s *Store) TaskNodeInboxThread(ctx context.Context, p Party, thread, run uuid.UUID, mark bool) ([]InboxMessage, bool, error) {
	var out []InboxMessage
	handled, e := s.TaskNodeInboxAccess(ctx, p, thread, run, func(m *TaskMutation, b TaskNodeInboxBinding) error {
		rows, e := m.Tx.Query(ctx, inboxSelect(160)+` WHERE m.thread_id=$1 ORDER BY m.created_at,m.id`, thread)
		if e != nil {
			return e
		}
		out, e = pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
		if e != nil {
			return e
		}
		if len(out) == 0 {
			return apperr.NotFound("线索")
		}
		if !mark {
			return nil
		}
		for i := range out {
			v := &out[i]
			if v.To.Party != p {
				continue
			}
			tag, e := m.Tx.Exec(ctx, `UPDATE inbox_messages SET read_at=clock_timestamp() WHERE id=$1 AND read_at IS NULL AND cancelled_at IS NULL`, v.ID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() > 0 {
				if e = s.voidReadTaskTurnTx(ctx, m, v.ID); e != nil {
					return e
				}
			}
			if p.Kind == "agent" {
				if e = s.LinkTaskNodeInboxRunTx(ctx, m, v.ID, run, "read"); e != nil {
					return e
				}
			} else {
				if e = s.EmitTaskNodeInboxTx(ctx, m, v.ID); e != nil {
					return e
				}
			}
		}
		// The caller uses real post-update receipts to emit inbox.message.read.
		rows, e = m.Tx.Query(ctx, inboxSelect(160)+` WHERE m.thread_id=$1 ORDER BY m.created_at,m.id`, thread)
		if e != nil {
			return e
		}
		out, e = pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
		return e
	})
	return out, handled, e
}
