package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// PermissionRequest is something a runtime asked to do in a mode other than full, waiting for a person (see the
// PermissionRequest schema).
type PermissionRequest struct {
	ID, AgentID uuid.UUID
	// RunID is the run of a regular Agent; TaskTurnID, TaskID and NodeRunID the task turn, task and node of a task
	// instance.
	RunID, TaskTurnID, TaskID, NodeRunID *uuid.UUID
	Runtime, Kind, Title                 string
	Detail                               *string
	CreatedAt                            time.Time
	State                                string // pending, approved, rejected, cancelled
	AnsweredAt                           *time.Time
	Reason                               *string
	QuestionID                           *uuid.UUID
}

// OwnerRun is the run or task turn that waits.
func (r PermissionRequest) OwnerRun() uuid.UUID {
	if r.RunID != nil {
		return *r.RunID
	}
	if r.TaskTurnID != nil {
		return *r.TaskTurnID
	}
	return uuid.Nil
}

// PermissionAnsweredError: the request was answered (or cancelled) already; Request is how it ended.
type PermissionAnsweredError struct{ Request PermissionRequest }

func (e *PermissionAnsweredError) Error() string {
	return fmt.Sprintf("permission request %s is %s already", e.Request.ID, e.Request.State)
}

const permissionColumns = `id, agent_id, run_id, task_turn_id, task_id, node_run_id, runtime, kind, title, detail, created_at, state,
	answered_at, reason, question_message_id`

func scanPermission(row db.Scanner) (PermissionRequest, error) {
	var r PermissionRequest
	err := row.Scan(&r.ID, &r.AgentID, &r.RunID, &r.TaskTurnID, &r.TaskID, &r.NodeRunID, &r.Runtime, &r.Kind, &r.Title, &r.Detail, &r.CreatedAt,
		&r.State, &r.AnsweredAt, &r.Reason, &r.QuestionID)
	return r, err
}

func (s *Store) permissionList(ctx context.Context, where string, args ...any) ([]PermissionRequest, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+permissionColumns+` FROM permission_requests WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	out, err := db.CollectRows(rows, func(row db.CollectableRow) (PermissionRequest, error) { return scanPermission(row) })
	if out == nil {
		out = []PermissionRequest{}
	}
	return out, err
}

// CreatePermissionRequest records a pending request.
func (s *Store) CreatePermissionRequest(ctx context.Context, r PermissionRequest) (PermissionRequest, error) {
	return scanPermission(s.Pool.QueryRow(ctx, `INSERT INTO permission_requests (id, agent_id, run_id, task_turn_id, task_id, node_run_id, runtime,
			kind, title, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+permissionColumns,
		r.ID, r.AgentID, r.RunID, r.TaskTurnID, r.TaskID, r.NodeRunID, r.Runtime, r.Kind, r.Title, r.Detail))
}

// GetPermissionRequest returns a request.
func (s *Store) GetPermissionRequest(ctx context.Context, id uuid.UUID) (PermissionRequest, error) {
	r, err := scanPermission(s.Pool.QueryRow(ctx, `SELECT `+permissionColumns+` FROM permission_requests WHERE id = $1`, id))
	return r, notFoundIfNoRows(err, "Permission request")
}

// PendingPermissionRequests are the pending requests of a regular Agent's runs, oldest first.
func (s *Store) PendingPermissionRequests(ctx context.Context, agentID uuid.UUID) ([]PermissionRequest, error) {
	return s.permissionList(ctx, `agent_id = $1 AND run_id IS NOT NULL AND state = 'pending'`, agentID)
}

// NodePendingPermissionRequests are the pending requests of a task node's turns, oldest first.
func (s *Store) NodePendingPermissionRequests(ctx context.Context, nodeRunID uuid.UUID) ([]PermissionRequest, error) {
	return s.permissionList(ctx, `node_run_id = $1 AND state = 'pending'`, nodeRunID)
}

// RunPermissionRequests are the requests of a run or a task turn, oldest first.
func (s *Store) RunPermissionRequests(ctx context.Context, runOrTurn uuid.UUID) ([]PermissionRequest, error) {
	return s.permissionList(ctx, `run_id = $1 OR task_turn_id = $1`, runOrTurn)
}

// LinkPermissionQuestionTx records the Inbox question that asks about a request, in the transaction that writes it.
func LinkPermissionQuestionTx(ctx context.Context, tx *db.Tx, requestID, questionID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE permission_requests SET question_message_id = $2 WHERE id = $1`, requestID, questionID)
	return err
}

// IsPermissionQuestionTx reports whether an Inbox question asks about a permission request.
func IsPermissionQuestionTx(ctx context.Context, tx *db.Tx, questionID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permission_requests WHERE question_message_id = $1)`, questionID).Scan(&ok)
	return ok, err
}

// Option ids of a permission question.
const (
	PermissionOptionApprove = "1"
	PermissionOptionReject  = "2"
)

// AnswerPermissionQuestionTx answers the request a question asks about, in the transaction that answers the question:
// the Approve option approves it; the Reject option, or a typed reply, rejects it (a typed reply is the reason). A
// request no longer pending is an error.
func AnswerPermissionQuestionTx(ctx context.Context, tx *db.Tx, questionID uuid.UUID, optionID *string, text string) error {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM permission_requests WHERE question_message_id = $1`, questionID).Scan(&id); err != nil {
		if IsNotFound(notFoundIfNoRows(err, "")) {
			return nil // not a permission question
		}
		return err
	}
	state, reason := "rejected", (*string)(nil)
	switch {
	case optionID != nil && *optionID == PermissionOptionApprove:
		state = "approved"
	case optionID == nil && text != "":
		reason = &text
	}
	_, err := answerPermissionTx(ctx, tx, id, state, reason)
	return err
}

// answerPermissionTx moves a pending request to state; *PermissionAnsweredError when it is not pending.
func answerPermissionTx(ctx context.Context, tx *db.Tx, id uuid.UUID, state string, reason *string) (PermissionRequest, error) {
	r, err := scanPermission(tx.QueryRow(ctx, `UPDATE permission_requests SET state = $2, reason = $3, answered_at = $4
		WHERE id = $1 AND state = 'pending' RETURNING `+permissionColumns, id, state, reason, tx.Now()))
	if IsNotFound(notFoundIfNoRows(err, "")) {
		cur, gerr := scanPermission(tx.QueryRow(ctx, `SELECT `+permissionColumns+` FROM permission_requests WHERE id = $1`, id))
		if gerr != nil {
			return PermissionRequest{}, notFoundIfNoRows(gerr, "Permission request")
		}
		return cur, &PermissionAnsweredError{Request: cur}
	}
	return r, err
}

// AnswerPermissionRequest approves or rejects a pending request that has no open Inbox question; *PermissionAnsweredError
// when it is not pending.
func (s *Store) AnswerPermissionRequest(ctx context.Context, id uuid.UUID, approve bool, reason *string) (PermissionRequest, error) {
	var out PermissionRequest
	err := s.inTx(ctx, func(tx *db.Tx) error {
		state := "rejected"
		if approve {
			state, reason = "approved", nil
		}
		var err error
		out, err = answerPermissionTx(ctx, tx, id, state, reason)
		return err
	})
	return out, err
}

// CancelPermissionRequest cancels a pending request for reason and ends its open Inbox question (expired, as a dialog
// whose turn ended); changed is false when it was no longer pending.
func (s *Store) CancelPermissionRequest(ctx context.Context, id uuid.UUID, reason string) (PermissionRequest, bool, error) {
	var out PermissionRequest
	changed := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var err error
		out, err = answerPermissionTx(ctx, tx, id, "cancelled", &reason)
		if _, done := err.(*PermissionAnsweredError); done {
			return nil
		}
		if err != nil {
			return err
		}
		changed = true
		return endPermissionQuestionTx(ctx, tx, out)
	})
	return out, changed, err
}

func endPermissionQuestionTx(ctx context.Context, tx *db.Tx, r PermissionRequest) error {
	if r.QuestionID == nil {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE inbox_questions SET state='expired', expired_reason='run_ended', notice_state='skipped'
		WHERE message_id = $1 AND state = 'open'`, *r.QuestionID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return QueueQuestionEventTx(ctx, tx, *r.QuestionID, "expired")
}

// CancelPendingPermissionRequests cancels every request still pending, for reason (no runtime waits for them any more:
// the server started again), and returns them.
func (s *Store) CancelPendingPermissionRequests(ctx context.Context, reason string) ([]PermissionRequest, error) {
	pending, err := s.permissionList(ctx, `state = 'pending'`)
	if err != nil {
		return nil, err
	}
	var out []PermissionRequest
	for _, r := range pending {
		c, changed, err := s.CancelPermissionRequest(ctx, r.ID, reason)
		if err != nil {
			return out, err
		}
		if changed {
			out = append(out, c)
		}
	}
	return out, nil
}

// PermissionDefaultMode is the permission mode new Agents get.
func (s *Store) PermissionDefaultMode(ctx context.Context) (string, error) {
	var mode string
	err := s.Pool.QueryRow(ctx, `SELECT default_mode FROM permission_settings`).Scan(&mode)
	return mode, err
}

// SetPermissionDefaultMode changes the permission mode new Agents get.
func (s *Store) SetPermissionDefaultMode(ctx context.Context, mode string) (string, error) {
	var out string
	err := s.Pool.QueryRow(ctx, `UPDATE permission_settings SET default_mode = $1, updated_at = now() RETURNING default_mode`, mode).Scan(&out)
	return out, err
}
