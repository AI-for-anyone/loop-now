package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pi 扩展的对话（plugins/SPEC.md §11）：confirm、select、input 作为 Agent 发给控制台的提问，extension_dialogs 标出这类提问。

// ExtensionDialog 是提问对应的扩展对话。
type ExtensionDialog struct {
	RunID     uuid.UUID
	RequestID string
	Method    string
}

// InsertExtensionDialogTx 在写入提问的事务里标出它是扩展对话。
func InsertExtensionDialogTx(ctx context.Context, tx pgx.Tx, question uuid.UUID, d ExtensionDialog) error {
	_, err := tx.Exec(ctx, `INSERT INTO extension_dialogs(question_message_id,run_id,request_id,method) VALUES($1,$2,$3,$4)`, question, d.RunID, d.RequestID, d.Method)
	return err
}

// IsExtensionDialogTx 报告提问是不是扩展对话。
func IsExtensionDialogTx(ctx context.Context, tx pgx.Tx, question uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM extension_dialogs WHERE question_message_id=$1)`, question).Scan(&ok)
	return ok, err
}

// DialogState 是扩展对话提问的当前状态：State 为 open、answered、expired 或 closed；
// 回答了的带选项与回答的全文。
type DialogState struct {
	State     string
	ExpiresAt time.Time
	OptionID  *string
	Text      string
}

// GetDialogState 读取扩展对话提问的状态。
func (s *Store) GetDialogState(ctx context.Context, question uuid.UUID) (DialogState, error) {
	var d DialogState
	err := s.Pool.QueryRow(ctx, `SELECT q.state,q.expires_at,q.answer_option_id,coalesce(a.text,'') FROM inbox_questions q
		LEFT JOIN inbox_messages a ON a.id=q.answer_message_id WHERE q.message_id=$1`, question).Scan(&d.State, &d.ExpiresAt, &d.OptionID, &d.Text)
	return d, notFoundIfNoRows(err, "提问")
}

// EndExtensionDialog 在这一轮结束（或 Pi 不再等待）时让仍未回答的扩展对话提问到期，原因为 run_ended；changed 表示这次由未回答变为到期。
func (s *Store) EndExtensionDialog(ctx context.Context, question uuid.UUID) (changed bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE inbox_questions SET state='expired',expired_reason='run_ended',notice_state='skipped' WHERE message_id=$1 AND state='open'`, question)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		changed = true
		return QueueQuestionEventTx(ctx, tx, question, "expired")
	})
	return changed, err
}
