package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// Pi extension dialogs (plugins/SPEC.md §11): confirm, select and input are questions the Agent sends to the console,
// and extension_dialogs marks such questions.

// ExtensionDialog is the extension dialog behind a question.
type ExtensionDialog struct {
	RunID     uuid.UUID
	RequestID string
	Method    string
}

// InsertExtensionDialogTx marks a question as an extension dialog, in the transaction that writes the question.
func InsertExtensionDialogTx(ctx context.Context, tx *db.Tx, question uuid.UUID, d ExtensionDialog) error {
	_, err := tx.Exec(ctx, `INSERT INTO extension_dialogs(question_message_id,run_id,request_id,method) VALUES($1,$2,$3,$4)`, question, d.RunID, d.RequestID, d.Method)
	return err
}

// IsExtensionDialogTx reports whether a question is an extension dialog.
func IsExtensionDialogTx(ctx context.Context, tx *db.Tx, question uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM extension_dialogs WHERE question_message_id=$1)`, question).Scan(&ok)
	return ok, err
}

// DialogState is the current state of an extension dialog question: State is open, answered, expired or closed;
// an answered one carries the full options and answer.
type DialogState struct {
	State     string
	ExpiresAt time.Time
	OptionID  *string
	Text      string
}

// GetDialogState reads the state of an extension dialog question.
func (s *Store) GetDialogState(ctx context.Context, question uuid.UUID) (DialogState, error) {
	var d DialogState
	err := s.Pool.QueryRow(ctx, `SELECT q.state,q.expires_at,q.answer_option_id,coalesce(a.text,'') FROM inbox_questions q
		LEFT JOIN inbox_messages a ON a.id=q.answer_message_id WHERE q.message_id=$1`, question).Scan(&d.State, &d.ExpiresAt, &d.OptionID, &d.Text)
	return d, notFoundIfNoRows(err, "Question")
}

// EndExtensionDialog expires an extension dialog question that is still unanswered when this turn ends (or Pi stops
// waiting), with reason run_ended; changed means this call moved it from unanswered to expired.
func (s *Store) EndExtensionDialog(ctx context.Context, question uuid.UUID) (changed bool, err error) {
	err = s.inTx(ctx, func(tx *db.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE inbox_questions SET state='expired',expired_reason='run_ended',notice_state='skipped' WHERE message_id=$1 AND state='open'`, question)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		changed = true
		return QueueQuestionEventTx(ctx, tx, question, "expired")
	})
	return changed, err
}
