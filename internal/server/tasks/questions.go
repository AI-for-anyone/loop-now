package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"

	"loop-now/internal/server/db"
)

// OpenQuestion is the entry point for the unified question service: in one transaction it creates the original question
// and binds it to the current node, guaranteeing at most one unanswered question per node. create may only write
// question/Inbox data; it must not call back into this service to take the task lock again.
func (s *Service) OpenQuestion(ctx context.Context, p auth.Principal, nodeID uuid.UUID, create func(*db.Tx) (taskmodel.Question, error)) (json.RawMessage, error) {
	return s.Store.MutateTask(ctx, p.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		n, e := nodeActor(p, m, nodeID)
		if e != nil {
			return nil, e
		}
		if e := s.Store.CheckTaskLarkRunTx(ctx, m, nodeID, p.RuntimeID, uuid.Nil); e != nil {
			return nil, e
		}
		if m.Record.Ended() || n.Status == "blocked" || n.Output != nil {
			return nil, conflict("The current node cannot ask a question: the task has ended, the node is blocked, or it has already submitted.")
		}
		if n.Question != nil && n.Question.Status == "open" {
			return nil, conflict("The current node already has an unanswered question.")
		}
		q, e := create(m.Tx)
		if e != nil {
			return nil, e
		}
		if q.ID == uuid.Nil || q.ThreadID == uuid.Nil || q.MessageID == uuid.Nil || q.Status != "open" {
			return nil, apperr.Invalid("question", "The question service returned an invalid reference to the original question.")
		}
		q.CanAnswer = false // computed per reader on read; never cache the creator's or console's permissions for others
		n.Question = &q
		return q, m.Emit("node.updated", map[string]any{"node": n})
	})
}

// QuestionChanged is called by the unified question service after its commit, and may run repeatedly. A late, older
// question only updates the history; it does not wake a newer node or end the task.
func (s *Service) QuestionChanged(ctx context.Context, taskID, questionID uuid.UUID, status string) error {
	if status != "answered" && status != "expired" && status != "closed" {
		return apperr.Invalid("status", "Must be answered, expired or closed.")
	}
	_, err := s.Store.MutateTask(ctx, taskID, nil, func(m *store.TaskMutation) (any, error) {
		var node *taskmodel.NodeRun
		for i := range m.Record.View.Nodes {
			n := &m.Record.View.Nodes[i]
			if n.Question != nil && n.Question.ID == questionID {
				node = n
				break
			}
		}
		if node == nil || node.Question.Status != "open" {
			m.Unchanged()
			return nil, nil
		}
		node.Question.Status = status
		node.Question.CanAnswer = false
		if !m.Record.Ended() && m.Record.ActiveNode(node.ID) != nil && node.Status != "blocked" {
			text := fmt.Sprintf("The question %s you asked is now %s. Read the original question and its replies from the Inbox, then continue the current node; do not treat ordinary messages as answers to the options.", questionID, status)
			turn, e := s.Store.EnqueueTaskTurnFor(ctx, m, node, text, "inbox_reminder", 1, &node.Question.MessageID)
			if e != nil {
				return nil, e
			}
			if e = store.MarkQuestionWakeupTx(ctx, m.Tx, turn.ID); e != nil {
				return nil, e
			}
		}
		return nil, m.Emit("node.updated", map[string]any{"node": node})
	})
	if err == nil {
		s.Kick(taskID)
	}
	return err
}

// QuestionViews gets the answer permission for the current reader from the unified question service; when that is not
// wired in, it safely shows the question as not answerable.
func (s *Service) QuestionViews(ctx context.Context, p auth.Principal, d taskmodel.Detail) (taskmodel.Detail, error) {
	for i := range d.Nodes {
		if q := d.Nodes[i].Question; q != nil {
			view, e := s.QuestionView(ctx, p, *q)
			if e != nil {
				return taskmodel.Detail{}, e
			}
			d.Nodes[i].Question = &view
		}
	}
	return d, nil
}
func (s *Service) QuestionView(ctx context.Context, p auth.Principal, q taskmodel.Question) (taskmodel.Question, error) {
	q.CanAnswer = false
	if q.Status != "open" || s.Source == nil || s.Source.ViewQuestion == nil {
		return q, nil
	}
	view, e := s.Source.ViewQuestion(ctx, p, q)
	if e != nil {
		return q, e
	}
	if view.ID != q.ID || view.ThreadID != q.ThreadID || view.MessageID != q.MessageID {
		return q, errors.New("the question service reference does not match")
	}
	switch view.Status {
	case "open":
	case "answered", "expired", "closed":
		view.CanAnswer = false
	default:
		return q, errors.New("the question service returned an invalid status")
	}
	return view, nil
}
