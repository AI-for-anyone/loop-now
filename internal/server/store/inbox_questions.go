package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"time"
)

type QuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type QuestionAnswer struct {
	MessageID   uuid.UUID       `json:"messageId"`
	OptionID    *string         `json:"optionId"`
	OptionLabel *string         `json:"optionLabel"`
	TextPreview string          `json:"textPreview"`
	By          json.RawMessage `json:"by"`
	At          time.Time       `json:"at"`
}
type InboxQuestion struct {
	Options   []QuestionOption `json:"options"`
	ExpiresAt time.Time        `json:"expiresAt"`
	State     string           `json:"state"`
	Answer    *QuestionAnswer  `json:"answer"`
}
type InboxAnswerTo struct {
	MessageID   uuid.UUID `json:"messageId"`
	OptionID    *string   `json:"optionId"`
	OptionLabel *string   `json:"optionLabel"`
}
type InboxNotice struct {
	Type      string    `json:"type"`
	MessageID uuid.UUID `json:"messageId"`
	Reason    string    `json:"reason"`
}
type QuestionBinding struct{ TaskID, RuntimeID, NodeRunID uuid.UUID }

func (s *Store) QuestionBinding(ctx context.Context, id uuid.UUID) (*QuestionBinding, error) {
	var b QuestionBinding
	err := s.Pool.QueryRow(ctx, `SELECT task_id,runtime_id,node_run_id FROM task_questions WHERE question_message_id=$1`, id).Scan(&b.TaskID, &b.RuntimeID, &b.NodeRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &b, err
}
func GetQuestionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool, preview ...int) (*InboxQuestion, error) {
	size := 200
	if len(preview) > 0 {
		size = preview[0]
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF q"
	}
	var q InboxQuestion
	var opts, by []byte
	var answerID *uuid.UUID
	var optionID *string
	var at *time.Time
	var textPreview *string
	err := tx.QueryRow(ctx, `SELECT q.options,q.expires_at,q.state,q.answer_message_id,q.answer_option_id,q.answered_by,q.answered_at,left(a.text,$2) FROM inbox_questions q LEFT JOIN inbox_messages a ON a.id=q.answer_message_id WHERE q.message_id=$1`+suffix, id, size).Scan(&opts, &q.ExpiresAt, &q.State, &answerID, &optionID, &by, &at, &textPreview)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(opts, &q.Options); err != nil {
		return nil, err
	}
	if answerID != nil && at != nil {
		q.Answer = &QuestionAnswer{MessageID: *answerID, OptionID: optionID, By: by, At: *at}
		if textPreview != nil {
			q.Answer.TextPreview = *textPreview
		}
		if optionID != nil {
			for _, o := range q.Options {
				if o.ID == *optionID {
					label := o.Label
					q.Answer.OptionLabel = &label
				}
			}
		}
	}
	return &q, nil
}
func QueueQuestionEventTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, state string) error {
	_, err := tx.Exec(ctx, `INSERT INTO inbox_question_outbox(id,question_message_id,state,message_id)VALUES($1,$2,$3,$4) ON CONFLICT(question_message_id,state) DO NOTHING`, uuid.New(), id, state, uuid.New())
	return err
}
func CloseInboxQuestion(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) (string, error) {
	if reason != "task_retry" && reason != "task_cancel" && reason != "node_completed" {
		return "", apperr.Invalid("reason", "问题关闭原因无效")
	}
	q, err := GetQuestionTx(ctx, tx, id, true)
	if err != nil {
		return "", err
	}
	if q == nil {
		return "", apperr.NotFound("原问题")
	}
	if q.State != "open" {
		return q.State, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE inbox_questions SET state='closed',closed_at=now(),closed_reason=$2 WHERE message_id=$1`, id, reason); err != nil {
		return "", err
	}
	return "closed", QueueQuestionEventTx(ctx, tx, id, "closed")
}
func (s *Store) ExpireInboxQuestions(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH due AS (SELECT message_id FROM inbox_questions WHERE state='open' AND expires_at<=now() ORDER BY message_id FOR UPDATE SKIP LOCKED LIMIT 100) UPDATE inbox_questions SET state='expired',expired_reason='deadline',notice_state=CASE WHEN EXISTS(SELECT 1 FROM inbox_messages m WHERE m.id=inbox_questions.message_id AND m.from_kind='agent') AND NOT EXISTS(SELECT 1 FROM extension_dialogs d WHERE d.question_message_id=inbox_questions.message_id) THEN 'pending' ELSE 'none' END WHERE message_id IN(SELECT message_id FROM due) RETURNING message_id`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = QueueQuestionEventTx(ctx, tx, id, "expired"); err != nil {
				return err
			}
		}
		return nil
	})
}
