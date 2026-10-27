package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"net/url"
	"time"
)

// ViewQuestion never trusts the persisted task reference as an authorization.
func (s *Service) ViewQuestion(ctx context.Context, p auth.Principal, ref taskmodel.Question) (taskmodel.Question, error) {
	if ref.ID != ref.MessageID {
		return ref, apperr.NotFound("原问题引用")
	}
	msg, err := s.St.GetInboxMessage(ctx, ref.MessageID, s.O.PreviewChars)
	if err != nil {
		return ref, err
	}
	binding, err := s.St.QuestionBinding(ctx, ref.ID)
	if err != nil {
		return ref, err
	}
	if msg.ThreadID != ref.ThreadID || msg.Question == nil || binding == nil || msg.From.RuntimeID != binding.RuntimeID {
		return ref, apperr.NotFound("原问题引用")
	}
	if p.Kind == auth.KindAgent && p.RuntimeID != uuid.Nil && p.RuntimeID != binding.RuntimeID && p.RuntimeID != msg.To.RuntimeID {
		ref.CanAnswer = false
	} else {
		ref.CanAnswer = Party(p) == msg.To.Party
	}
	ref.Status = msg.Question.State
	ref.Text = msg.Text
	ref.CanAnswer = ref.CanAnswer && ref.Status == "open" && msg.Question.ExpiresAt.After(time.Now())
	return ref, nil
}

// SystemNotice finds the task by the durable message ID. The caller's earlier
// Manager selection is a hint; project/role/link are reread under the commit lock.
func (s *Service) SystemNotice(ctx context.Context, _ uuid.UUID, messageID uuid.UUID, _ string) error {
	var project, task uuid.UUID
	if err := s.St.Pool.QueryRow(ctx, `SELECT t.project_id,n.task_id FROM task_notifications n JOIN task_records t ON t.id=n.task_id WHERE n.message_id=$1`, messageID).Scan(&project, &task); err != nil {
		return err
	}
	// Lock the project first to discover the Manager, then enter the shared
	// transaction with the same project lock; avoid a check/use window by keeping
	// role validation within that transaction and retry on a changed hint.
	for attempts := 0; attempts < 4; attempts++ {
		var hint *uuid.UUID
		if err := s.St.Pool.QueryRow(ctx, `SELECT manager_agent_id FROM projects WHERE id=$1`, project).Scan(&hint); err != nil {
			return err
		}
		if hint == nil {
			return apperr.Conflict("task_conflict", "项目未指定Manager")
		}
		to := store.AgentParty(*hint)
		err := s.St.InboxTransaction(ctx, store.InboxLockPlan{Projects: []uuid.UUID{project}, Parties: []store.Party{to}, MessageID: messageID}, func(scope *store.InboxTransaction) error {
			var manager *uuid.UUID
			var name string
			if err := scope.Tx.QueryRow(ctx, `SELECT manager_agent_id,name FROM projects WHERE id=$1`, project).Scan(&manager, &name); err != nil {
				return err
			}
			if manager == nil || *manager != *hint {
				return errManagerChanged
			}
			exists, err := store.InboxMessageExistsTx(ctx, scope.Tx, messageID)
			if err != nil || exists {
				return err
			}
			var text string
			if err = scope.Tx.QueryRow(ctx, `SELECT text FROM task_notifications WHERE message_id=$1`, messageID).Scan(&text); err != nil {
				return err
			}
			msg := store.NewInboxMessage{ID: messageID, From: store.Party{Kind: "platform"}, To: to, Text: text + "\n/p/" + url.PathEscape(name) + "/tasks/" + task.String(), WhenBusy: "queue"}
			if err = store.InsertInboxMessage(ctx, scope.Tx, msg); err != nil {
				return err
			}
			return s.St.QueueInboxReminderTx(ctx, scope.Tx, msg)
		})
		if err == errManagerChanged {
			continue
		}
		if err == nil {
			s.inboxCommitted(ctx, to)
			if msg, e := s.St.GetInboxMessage(ctx, messageID, s.O.PreviewChars); e == nil {
				s.emitCreated(ctx, msg)
			}
		}
		return err
	}
	return errManagerChanged
}

var errManagerChanged = fmt.Errorf("任务通知Manager已改变，稍后重试")

func (s *Service) ReconcileQuestions(ctx context.Context) error {
	if err := s.St.ExpireInboxQuestions(ctx); err != nil {
		return err
	}
	rows, err := s.St.Pool.Query(ctx, `SELECT id,question_message_id,state,message_id FROM inbox_question_outbox WHERE delivered_at IS NULL ORDER BY created_at,id LIMIT 100`)
	if err != nil {
		return err
	}
	type entry struct {
		id, qid, msg uuid.UUID
		state        string
	}
	items := []entry{}
	for rows.Next() {
		var item entry
		if err = rows.Scan(&item.id, &item.qid, &item.state, &item.msg); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	failures := []error{}
	for _, item := range items {
		if err = s.deliverQuestionEvent(ctx, item.id, item.qid, item.msg, item.state); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *Service) deliverQuestionEvent(ctx context.Context, id, qid, messageID uuid.UUID, state string) error {
	original, err := s.St.GetInboxMessage(ctx, qid, s.O.PreviewChars)
	if err != nil {
		return err
	}
	binding, err := s.St.QuestionBinding(ctx, qid)
	if err != nil {
		return err
	}
	plan := store.InboxLockPlan{MessageID: messageID}
	if binding != nil {
		plan.Tasks = []uuid.UUID{binding.TaskID}
		record, e := s.St.GetTask(ctx, binding.TaskID)
		if e != nil {
			return e
		}
		plan.AllowEndedTaskHistory = record.Ended()
	}
	if state == "expired" && original.From.Kind == "agent" && !original.From.Deleted {
		plan.Parties = append(plan.Parties, original.From.Party)
	}
	var recipient *store.Party
	err = s.St.InboxTransaction(ctx, plan, func(scope *store.InboxTransaction) error {
		var delivered *time.Time
		if err := scope.Tx.QueryRow(ctx, `SELECT delivered_at FROM inbox_question_outbox WHERE id=$1 FOR UPDATE`, id).Scan(&delivered); err != nil {
			return err
		}
		if delivered != nil {
			return nil
		}
		q, err := store.GetQuestionTx(ctx, scope.Tx, qid, true, s.O.PreviewChars)
		if err != nil {
			return err
		}
		if q == nil {
			return apperr.NotFound("原问题")
		}
		wake := false
		if binding != nil {
			bridge, ok := s.Tasks.(taskQuestionBridge)
			if !ok {
				return apperr.Conflict("task_conflict", "任务问题服务未装配")
			}
			wake, err = bridge.SyncQuestionTx(ctx, scope.Tasks[binding.TaskID], qid, state)
			if err != nil {
				return err
			}
		}
		if state != "expired" || original.From.Kind != "agent" {
			return nil
		}
		// 扩展对话到期时 Pi 已收到取消，不再通知 Agent。
		dialog, err := store.IsExtensionDialogTx(ctx, scope.Tx, qid)
		if err != nil {
			return err
		}
		if original.From.Deleted || dialog {
			_, err = scope.Tx.Exec(ctx, `UPDATE inbox_questions SET notice_state='skipped' WHERE message_id=$1`, qid)
			return err
		}
		if binding != nil && !wake {
			m := scope.Tasks[binding.TaskID]
			current := m.Record.Current()
			if m.Record.Ended() || current == nil || current.ID != binding.NodeRunID {
				_, err = scope.Tx.Exec(ctx, `UPDATE inbox_questions SET notice_state='skipped' WHERE message_id=$1`, qid)
				return err
			}
			// Current blocked attempts receive an Inbox conversation without workflow retry.
		}
		exists, err := store.InboxMessageExistsTx(ctx, scope.Tx, messageID)
		if err != nil || exists {
			return err
		}
		to := original.From.Party
		recipient = &to
		thread := original.ThreadID
		msg := store.NewInboxMessage{ID: messageID, ThreadID: &thread, InReplyTo: &qid, From: store.Party{Kind: "platform"}, To: to, Text: "平台通知：提问已到期：" + original.Preview, WhenBusy: "queue"}
		if err = store.InsertInboxMessage(ctx, scope.Tx, msg); err != nil {
			return err
		}
		notice, _ := json.Marshal(store.InboxNotice{Type: "question_expired", MessageID: qid, Reason: "deadline"})
		if _, err = scope.Tx.Exec(ctx, `UPDATE inbox_messages SET notice=$2 WHERE id=$1`, messageID, notice); err != nil {
			return err
		}
		if _, err = scope.Tx.Exec(ctx, `UPDATE inbox_questions SET notice_state='sent' WHERE message_id=$1`, qid); err != nil {
			return err
		}
		return s.queueInsertedTx(ctx, scope, msg)
	})
	if err != nil {
		return err
	}
	if recipient != nil {
		s.inboxCommitted(ctx, *recipient)
		if msg, e := s.St.GetInboxMessage(ctx, messageID, s.O.PreviewChars); e == nil {
			s.emitCreated(ctx, msg)
		}
	}
	// Redis carries a projection upsert. An append failure leaves the durable
	// outbox pending; retry may duplicate SSE updates but never messages or turns.
	latest, err := s.St.GetInboxMessage(ctx, qid, s.O.PreviewChars)
	if err != nil {
		return err
	}
	if s.Co != nil {
		if err = s.append(ctx, "inbox.question.updated", func(unread int) any {
			return apitypes.SseInboxMessageCreated{Message: apitypes.FromInboxSummary(latest), UnreadTotal: unread}
		}); err != nil {
			return err
		}
	}
	_, err = s.St.Pool.Exec(ctx, `UPDATE inbox_question_outbox SET delivered_at=coalesce(delivered_at,now()) WHERE id=$1`, id)
	return err
}
