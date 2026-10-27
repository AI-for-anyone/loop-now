package inbox

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type QuestionInput struct {
	RunID     uuid.UUID // explicit current task turn for strict Inbox read/reply
	Options   []string
	OptionID  *string
	NodeRunID uuid.UUID
	ReplyTo   uuid.UUID
	// Channel 与 SenderName 只用于插件发件（SPEC §11）：渠道与这条消息上的发件名称。
	Channel    string
	SenderName string
	// ExpiresIn 与 Dialog 只用于 Pi 扩展的对话（AskDialog）：提问的期限与对话标记。
	ExpiresIn time.Duration
	Dialog    *store.ExtensionDialog
}
type taskQuestionBridge interface {
	CheckQuestionRunTx(context.Context, *store.TaskMutation, auth.Principal, uuid.UUID, uuid.UUID) error
	BindQuestionTx(context.Context, *store.TaskMutation, auth.Principal, uuid.UUID, taskmodel.Question) error
	SyncQuestionTx(context.Context, *store.TaskMutation, uuid.UUID, string) (bool, error)
	DeliverInboxTx(context.Context, pgx.Tx, *store.TaskMutation, store.NewInboxMessage) error
	InboxCommitted(context.Context, uuid.UUID)
}
type inboxCommitter interface {
	InboxCommitted(context.Context, uuid.UUID)
}

func validateOptions(options []string) error {
	if options == nil {
		return nil
	}
	if len(options) < 1 || len(options) > 10 {
		return apperr.Invalid("options", "选项须为1至10个")
	}
	seen := map[string]bool{}
	for _, label := range options {
		if strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 50 || seen[label] {
			return apperr.Invalid("options", "选项须非空、不重复且不超过50字符")
		}
		seen[label] = true
	}
	return nil
}
func (s *Service) questionDeliver(ctx context.Context, p auth.Principal, msg store.NewInboxMessage, input QuestionInput) (store.InboxMessage, error) {
	if msg.ForwardTaskID == uuid.Nil {
		return s.questionDeliverOnce(ctx, p, msg, input)
	}
	for attempt := 0; attempt < 4; attempt++ {
		record, err := s.St.GetTask(ctx, msg.ForwardTaskID)
		if err != nil {
			return store.InboxMessage{}, err
		}
		if !record.Ended() {
			return store.InboxMessage{}, apperr.Conflict("task_conflict", "任务尚未结束")
		}
		project, err := s.St.GetProject(ctx, record.View.Task.ProjectID)
		if err != nil {
			return store.InboxMessage{}, err
		}
		if project.ManagerAgentID == nil {
			return store.InboxMessage{}, apperr.Conflict("task_conflict", "项目未指定Manager")
		}
		msg.To = store.AgentParty(*project.ManagerAgentID)
		saved, err := s.questionDeliverOnce(ctx, p, msg, input)
		if err == errManagerChanged || store.IsNotFound(err) {
			continue
		}
		return saved, err
	}
	return store.InboxMessage{}, errManagerChanged
}
func (s *Service) questionDeliverOnce(ctx context.Context, p auth.Principal, msg store.NewInboxMessage, input QuestionInput) (store.InboxMessage, error) {
	if msg.To.Kind != "agent" {
		msg.WhenBusy = ""
	}
	if err := validateOptions(input.Options); err != nil {
		return store.InboxMessage{}, err
	}
	plan := store.InboxLockPlan{Parties: []store.Party{msg.From, msg.To}, MessageID: msg.ID}
	if msg.ForwardTaskID != uuid.Nil {
		plan.Tasks = append(plan.Tasks, msg.ForwardTaskID)
	}
	if input.Options != nil && p.TaskID != uuid.Nil {
		plan.Tasks = append(plan.Tasks, p.TaskID)
		if input.NodeRunID == uuid.Nil {
			return store.InboxMessage{}, apperr.Invalid("nodeRunId", "任务实例提问须提供当前节点运行ID")
		}
	}
	binding, err := s.St.QuestionBinding(ctx, input.ReplyTo)
	if err != nil {
		return store.InboxMessage{}, err
	}
	if binding != nil {
		plan.Tasks = append(plan.Tasks, binding.TaskID)
	}
	readQuestion := false
	err = s.St.InboxTransaction(ctx, plan, func(scope *store.InboxTransaction) error {
		readQuestion = false
		if msg.ForwardTaskID != uuid.Nil {
			m := scope.Tasks[msg.ForwardTaskID]
			if m == nil || !m.Record.Ended() {
				return apperr.Conflict("task_conflict", "任务尚未结束")
			}
			if m.ManagerID == nil || msg.To != store.AgentParty(*m.ManagerID) {
				return errManagerChanged
			}
		}
		var answerer store.PartyView
		var answered, dialogAnswer bool
		var q *store.InboxQuestion
		if input.ReplyTo != uuid.Nil {
			var err error
			q, err = store.GetQuestionTx(ctx, scope.Tx, input.ReplyTo, true, s.O.PreviewChars)
			if err != nil {
				return err
			}
			original, err := store.GetInboxMessageTx(ctx, scope.Tx, input.ReplyTo, s.O.PreviewChars)
			if err != nil {
				return err
			}
			recipient := original.To.Party == msg.From
			answerer = original.To
			if input.OptionID != nil && (!recipient || q == nil) {
				return apperr.Invalid("optionId", "只有原问题收件人可回答有效选项")
			}
			if q != nil && q.State == "open" && !q.ExpiresAt.After(time.Now()) {
				if _, err = scope.Tx.Exec(ctx, `UPDATE inbox_questions SET state='expired',expired_reason='deadline' WHERE message_id=$1`, input.ReplyTo); err != nil {
					return err
				}
				q.State = "expired"
				if err = store.QueueQuestionEventTx(ctx, scope.Tx, input.ReplyTo, "expired"); err != nil {
					return err
				}
			}
			if q != nil && recipient {
				if q.State != "open" && (input.OptionID != nil || input.Options != nil) {
					code := map[string]string{"answered": "already_answered", "expired": "question_expired", "closed": "question_closed", "cancelled": "question_closed"}[q.State]
					return &apperr.Error{Status: 409, Code: code, Message: "原问题已结束", Details: map[string]any{"question": q}}
				}
				if q.State == "open" {
					if input.OptionID != nil {
						found := false
						for _, opt := range q.Options {
							if opt.ID == *input.OptionID {
								found = true
								if strings.TrimSpace(msg.Text) == "" {
									msg.Text = "选择：" + opt.Label
								}
							}
						}
						if !found {
							return apperr.Invalid("optionId", "选项不存在")
						}
					}
					answered = true
				}
			}
		}
		if err := s.checkText(msg.Text, msg.WhenBusy); err != nil {
			return err
		}
		if input.Options != nil {
			if msg.From.Kind == "platform" {
				return apperr.Forbidden("平台通知不能提出问题")
			}
			if p.TaskID != uuid.Nil {
				bridge, ok := s.Tasks.(taskQuestionBridge)
				if !ok {
					return apperr.Conflict("task_conflict", "任务问题服务未装配")
				}
				m := scope.Tasks[p.TaskID]
				if m == nil {
					return apperr.NotFound("任务")
				}
				thread := msg.ID
				if msg.ThreadID != nil {
					thread = *msg.ThreadID
				}
				ref := taskmodel.Question{ID: msg.ID, MessageID: msg.ID, ThreadID: thread, Text: msg.Text, Status: "open"}
				if err := bridge.CheckQuestionRunTx(ctx, m, p, input.NodeRunID, input.RunID); err != nil {
					return err
				}
				if err := bridge.BindQuestionTx(ctx, m, p, input.NodeRunID, ref); err != nil {
					return err
				}
			}
		}
		if answered {
			// 扩展对话的回答直接交还给 Pi（AskDialog 在等），不再作为消息提醒 Agent。
			var err error
			if dialogAnswer, err = store.IsExtensionDialogTx(ctx, scope.Tx, input.ReplyTo); err != nil {
				return err
			}
		}
		if err := store.InsertInboxMessage(ctx, scope.Tx, msg); err != nil {
			return err
		}
		if dialogAnswer {
			if _, err := scope.Tx.Exec(ctx, `UPDATE inbox_messages SET read_at=now() WHERE id=$1`, msg.ID); err != nil {
				return err
			}
		}
		if answered {
			by, err := json.Marshal(apitypes.FromParty(answerer))
			if err != nil {
				return err
			}
			if _, err = scope.Tx.Exec(ctx, `UPDATE inbox_questions SET state='answered',answer_message_id=$2,answer_option_id=$3,answered_by=$4,answered_at=now() WHERE message_id=$1`, input.ReplyTo, msg.ID, input.OptionID, by); err != nil {
				return err
			}
			if _, err = scope.Tx.Exec(ctx, `UPDATE inbox_messages SET answers_message_id=$2,answer_option_id=$3 WHERE id=$1`, msg.ID, input.ReplyTo, input.OptionID); err != nil {
				return err
			}
			if err = store.QueueQuestionEventTx(ctx, scope.Tx, input.ReplyTo, "answered"); err != nil {
				return err
			}
			// Answering implies having read it: the console's answer also marks the question read.
			if answerer.Kind == "console" {
				tag, err := scope.Tx.Exec(ctx, `UPDATE inbox_messages SET read_at=now() WHERE id=$1 AND read_at IS NULL AND cancelled_at IS NULL`, input.ReplyTo)
				if err != nil {
					return err
				}
				readQuestion = tag.RowsAffected() > 0
			}
			if binding != nil {
				if bridge, ok := s.Tasks.(taskQuestionBridge); ok {
					if _, err = bridge.SyncQuestionTx(ctx, scope.Tasks[binding.TaskID], input.ReplyTo, "answered"); err != nil {
						return err
					}
				}
			}
		}
		if input.Options != nil {
			options := make([]store.QuestionOption, len(input.Options))
			for i, label := range input.Options {
				options[i] = store.QuestionOption{ID: strconv.Itoa(i + 1), Label: label}
			}
			data, _ := json.Marshal(options)
			ttl := s.O.QuestionTTL
			if input.ExpiresIn > 0 {
				ttl = input.ExpiresIn
			}
			if ttl <= 0 {
				ttl = 7 * 24 * time.Hour
			}
			if _, err := scope.Tx.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at)VALUES($1,$2,$3)`, msg.ID, data, time.Now().UTC().Add(ttl)); err != nil {
				return err
			}
			if input.Dialog != nil {
				if err := store.InsertExtensionDialogTx(ctx, scope.Tx, msg.ID, *input.Dialog); err != nil {
					return err
				}
			}
			if p.TaskID != uuid.Nil {
				if _, err := scope.Tx.Exec(ctx, `INSERT INTO task_questions(question_message_id,task_id,runtime_id,node_run_id)VALUES($1,$2,$3,$4)`, msg.ID, p.TaskID, p.RuntimeID, input.NodeRunID); err != nil {
					return err
				}
			}
		}
		if dialogAnswer {
			return nil
		}
		return s.queueInsertedTx(ctx, scope, msg)
	})
	if err != nil {
		if apperr.Is(err, "question_expired") {
			_ = s.St.ExpireInboxQuestions(ctx)
		}
		return store.InboxMessage{}, err
	}
	s.inboxCommitted(ctx, msg.To)
	if readQuestion {
		if q, err := s.St.GetInboxMessage(ctx, input.ReplyTo, s.O.PreviewChars); err == nil {
			s.emitRead(ctx, q)
		}
	}
	saved, err := s.St.GetInboxMessage(ctx, msg.ID, s.O.PreviewChars)
	if err == nil {
		s.emitCreated(ctx, saved)
	}
	return saved, err
}
func (s *Service) queueInsertedTx(ctx context.Context, scope *store.InboxTransaction, msg store.NewInboxMessage) error {
	if msg.To.Kind != "agent" {
		return nil
	}
	if msg.To.RuntimeID == uuid.Nil {
		return s.St.QueueInboxReminderTx(ctx, scope.Tx, msg)
	}
	var taskID uuid.UUID
	if err := scope.Tx.QueryRow(ctx, `SELECT task_id FROM task_instances WHERE id=$1 AND agent_id=$2`, msg.To.RuntimeID, msg.To.ID).Scan(&taskID); err != nil {
		return err
	}
	bridge, ok := s.Tasks.(taskQuestionBridge)
	if !ok {
		return apperr.Conflict("task_conflict", "任务Inbox未装配")
	}
	return bridge.DeliverInboxTx(ctx, scope.Tx, scope.Tasks[taskID], msg)
}
func (s *Service) inboxCommitted(ctx context.Context, to store.Party) {
	if to.Kind != "agent" {
		return
	}
	if to.RuntimeID != uuid.Nil {
		if bridge, ok := s.Tasks.(taskQuestionBridge); ok {
			bridge.InboxCommitted(ctx, to.RuntimeID)
		}
	} else if conv, ok := s.Conv.(inboxCommitter); ok {
		conv.InboxCommitted(ctx, to.ID)
	}
}
