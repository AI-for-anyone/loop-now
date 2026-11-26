// Package permissions keeps what an Agent's runtime asks to do in a permission mode other than full, asks the console
// about it (the Agent's chat, the task node, and an Inbox question), and gives the runtime the first answer.
package permissions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// Inbox asks the console about a request and answers that question as the console.
type Inbox interface {
	AskPermission(ctx context.Context, agentID, requestID uuid.UUID, text string) (uuid.UUID, error)
	AnswerPermission(ctx context.Context, questionID uuid.UUID, approve bool, reason string) error
}

// Service implements executor.PermissionAsker.
type Service struct {
	Store *store.Store
	// Inbox asks the console; nil: requests show only in the chat.
	Inbox Inbox
	// Changed is told after a request is made, answered or cancelled (the Agent's or the node's stream); may be nil.
	Changed func(r store.PermissionRequest)
	Log     *slog.Logger
	// Poll is how often a waiting request is read again (an answer from the Inbox reaches it this way); 0 is 500ms.
	Poll time.Duration

	mu      sync.Mutex
	waiting map[uuid.UUID]chan struct{}
}

var _ executor.PermissionAsker = (*Service)(nil)

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.DiscardHandler)
}

// AskPermission records the request, asks the console and waits for the first answer, or cancels the request when ctx
// ends.
func (s *Service) AskPermission(ctx context.Context, q executor.PermissionAsk) executor.PermissionAnswer {
	req, err := s.create(ctx, q)
	if err != nil {
		s.log().Warn("could not record a permission request", "agentId", q.AgentID, "runId", q.RunID, "err", err)
		return executor.PermissionAnswer{Reason: "The request could not be recorded, so no one could answer it."}
	}
	wake := s.register(req.ID)
	defer s.unregister(req.ID)
	s.changed(req)
	if s.Inbox != nil {
		if _, err := s.Inbox.AskPermission(context.WithoutCancel(ctx), req.AgentID, req.ID, questionText(req)); err != nil {
			s.log().Warn("could not ask the console about a permission request", "request", req.ID, "err", err)
		}
	}
	poll := s.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			r, changed, err := s.Store.CancelPermissionRequest(context.WithoutCancel(ctx), req.ID, executor.CancelReason(ctx))
			if err != nil {
				s.log().Warn("could not cancel a permission request", "request", req.ID, "err", err)
				return executor.PermissionAnswer{}
			}
			if changed {
				s.changed(r)
			}
			return answerOf(r)
		case <-wake:
		case <-tick.C:
		}
		r, err := s.Store.GetPermissionRequest(context.WithoutCancel(ctx), req.ID)
		if err != nil {
			s.log().Warn("could not read a permission request", "request", req.ID, "err", err)
			continue
		}
		if r.State != "pending" {
			s.changed(r)
			return answerOf(r)
		}
	}
}

// create records the request: on the run of a regular Agent, or on the task turn (and its task and node) of a task
// instance, whose Agent is the one the instance runs.
func (s *Service) create(ctx context.Context, q executor.PermissionAsk) (store.PermissionRequest, error) {
	r := store.PermissionRequest{ID: uuid.Must(uuid.NewV7()), AgentID: q.AgentID, Runtime: q.Runtime, Kind: q.Kind, Title: q.Title}
	if r.Title == "" {
		r.Title = "Use a tool"
	}
	if q.Detail != "" {
		detail := executor.ClipDetail(q.Detail)
		r.Detail = &detail
	}
	inst, err := s.Store.TaskInstance(ctx, q.AgentID)
	switch {
	case store.IsNotFound(err):
		run := q.RunID
		r.RunID = &run
	case err != nil:
		return r, err
	default:
		turn, err := s.Store.TaskTurn(ctx, q.RunID)
		if err != nil {
			return r, err
		}
		id, task := turn.ID, inst.TaskID
		r.AgentID, r.TaskTurnID, r.TaskID = inst.AgentID, &id, &task
		if turn.NodeRunID != uuid.Nil {
			node := turn.NodeRunID
			r.NodeRunID = &node
		}
	}
	return s.Store.CreatePermissionRequest(ctx, r)
}

// Answer answers a pending request once, from the chat (or anything but the Inbox, which answers its question): it
// answers the request's Inbox question as the console would, so both show the same answer. A request no longer pending
// is a conflict carrying it.
func (s *Service) Answer(ctx context.Context, id uuid.UUID, approve bool, reason string) (store.PermissionRequest, error) {
	r, err := s.Store.GetPermissionRequest(ctx, id)
	if err != nil {
		return r, err
	}
	if r.State != "pending" {
		return r, Answered(r)
	}
	if r.QuestionID != nil && s.Inbox != nil {
		if err := s.Inbox.AnswerPermission(ctx, *r.QuestionID, approve, reason); err != nil {
			if cur, gerr := s.Store.GetPermissionRequest(ctx, id); gerr == nil && cur.State != "pending" {
				return cur, Answered(cur)
			}
			return r, err
		}
	} else {
		var why *string
		if reason = strings.TrimSpace(reason); reason != "" {
			why = &reason
		}
		if _, err := s.Store.AnswerPermissionRequest(ctx, id, approve, why); err != nil {
			var done *store.PermissionAnsweredError
			if errors.As(err, &done) {
				return done.Request, Answered(done.Request)
			}
			return r, err
		}
	}
	cur, err := s.Store.GetPermissionRequest(ctx, id)
	if err != nil {
		return r, err
	}
	if !s.wakeUp(id) {
		s.changed(cur) // nobody waits on it: tell the streams here
	}
	return cur, nil
}

// Answered is the conflict of answering a request that is no longer pending, carrying it as it is.
func Answered(r store.PermissionRequest) error {
	msg := "This permission request was already answered."
	if r.State == "cancelled" {
		msg = "This permission request was cancelled."
	}
	return &apperr.Error{Status: 409, Code: apperr.CodePermissionRequestAnswered, Message: msg,
		Details: apitypes.PermissionRequestAnsweredDetails{Request: apitypes.FromPermissionRequest(r)}}
}

// CancelLeftOver cancels the requests left pending by a server that stopped: no runtime waits for them any more.
func (s *Service) CancelLeftOver(ctx context.Context) error {
	_, err := s.Store.CancelPendingPermissionRequests(ctx, executor.CancelProcessExited.Reason)
	return err
}

func answerOf(r store.PermissionRequest) executor.PermissionAnswer {
	a := executor.PermissionAnswer{Approved: r.State == "approved"}
	if r.State == "rejected" && r.Reason != nil {
		a.Reason = *r.Reason
	}
	return a
}

func (s *Service) changed(r store.PermissionRequest) {
	if s.Changed != nil {
		s.Changed(r)
	}
}

func (s *Service) register(id uuid.UUID) chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.waiting == nil {
		s.waiting = map[uuid.UUID]chan struct{}{}
	}
	s.waiting[id] = ch
	s.mu.Unlock()
	return ch
}

func (s *Service) unregister(id uuid.UUID) {
	s.mu.Lock()
	delete(s.waiting, id)
	s.mu.Unlock()
}

// wakeUp tells the runtime waiting on a request to read it again; false when nothing waits on it.
func (s *Service) wakeUp(id uuid.UUID) bool {
	s.mu.Lock()
	ch := s.waiting[id]
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	return true
}

// questionText is the Inbox question about a request.
func questionText(r store.PermissionRequest) string {
	var b strings.Builder
	where := "in a reply"
	if r.TaskID != nil {
		where = "in a task step"
	}
	fmt.Fprintf(&b, "Permission request (%s, %s): %s", where, strings.ReplaceAll(r.Kind, "_", " "), r.Title)
	if r.Detail != nil && strings.TrimSpace(*r.Detail) != "" {
		detail := *r.Detail
		if len(detail) > 2000 {
			detail = executor.ClipDetail(detail[:2000]) + "\n…"
		}
		b.WriteString("\n\n" + detail)
	}
	b.WriteString("\n\nApprove or Reject. A typed reply rejects it and tells the Agent why.")
	return b.String()
}
