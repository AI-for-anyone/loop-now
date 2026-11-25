package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"loop-now/internal/server/executor/local/runturn"
)

// BackgroundObserver is an Observer that also hears of background tasks: Grok moves a terminal command to the
// background (_x.ai/task_backgrounded) and reports its end later (_x.ai/task_completed).
type BackgroundObserver interface {
	TaskStarted(t Task)
	TaskEnded(e TaskEnd)
}

// Task is a background task as the agent reports it.
type Task struct {
	ID, ToolCallID, Command, Description string
}

// TaskEnd is how it ended: its exit code and output when given; Killed when it was killed (_x.ai/task/kill, or the
// agent's own kill).
type TaskEnd struct {
	ID       string
	ExitCode *int64
	Output   *string
	Killed   bool
}

// SetSelfTurns sets where the prompts the agent runs by itself go (Grok runs one after a background task ended, with
// no prompt of the client open); nil drops them.
func (c *Client) SetSelfTurns(f func() runturn.SelfTurn) {
	c.mu.Lock()
	c.selfTurns = f
	c.mu.Unlock()
}

// KillTask stops one background task of the session (Grok's _x.ai/task/kill); its end comes as usual.
func (c *Client) KillTask(ctx context.Context, sessionID, taskID string) error {
	var out struct {
		Error *string `json:"error"`
	}
	if err := c.Call(ctx, "_x.ai/task/kill", map[string]any{"sessionId": sessionID, "taskId": taskID}, &out); err != nil {
		return err
	}
	if out.Error != nil && *out.Error != "" {
		return errors.New("acp: the agent did not kill task " + taskID + ": " + *out.Error)
	}
	return nil
}

// xai handles the agent's _x.ai notifications that matter: background tasks starting and ending, a prompt the agent
// begins by itself (_x.ai/queue/changed with a running prompt while no prompt of the client is open) and its end
// (turn_completed, with its usage).
func (c *Client) xai(method string, params json.RawMessage) {
	var p struct {
		SessionID       string          `json:"sessionId"`
		RunningPromptID *string         `json:"runningPromptId"`
		Update          json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var u struct {
		SessionUpdate string `json:"sessionUpdate"`
		ToolCallID    string `json:"tool_call_id"`
		TaskID        string `json:"task_id"`
		Command       string `json:"command"`
		Description   string `json:"description"`
		PromptID      string `json:"prompt_id"`
		StopReason    string `json:"stop_reason"`
		Snapshot      *struct {
			TaskID           string  `json:"task_id"`
			Output           *string `json:"output"`
			ExitCode         *int64  `json:"exit_code"`
			ExplicitlyKilled bool    `json:"explicitly_killed"`
		} `json:"task_snapshot"`
	}
	_ = json.Unmarshal(p.Update, &u)
	c.mu.Lock()
	obs, session := c.observer, c.session
	c.mu.Unlock()
	if session != "" && p.SessionID != session {
		return // a sub-agent's own session
	}
	bo, _ := obs.(BackgroundObserver)
	switch {
	case method == "_x.ai/task_backgrounded" && u.TaskID != "":
		if bo != nil {
			bo.TaskStarted(Task{ID: u.TaskID, ToolCallID: u.ToolCallID, Command: u.Command, Description: u.Description})
		}
	case method == "_x.ai/task_completed" && u.Snapshot != nil:
		if bo != nil {
			bo.TaskEnded(TaskEnd{ID: u.Snapshot.TaskID, ExitCode: u.Snapshot.ExitCode, Output: u.Snapshot.Output, Killed: u.Snapshot.ExplicitlyKilled})
		}
	case method == "_x.ai/queue/changed" && p.RunningPromptID != nil && *p.RunningPromptID != "":
		c.beginSelfTurn(p.SessionID, *p.RunningPromptID)
	case method == "_x.ai/session_notification" && u.SessionUpdate == "turn_completed" && u.PromptID != "":
		c.endSelfTurn(u.PromptID, u.StopReason, p.Update)
	}
}

// beginSelfTurn opens a turn for a prompt the agent runs by itself, when no prompt of the client is open.
func (c *Client) beginSelfTurn(sessionID, promptID string) {
	c.mu.Lock()
	hook := c.selfTurns
	open := c.turn != nil || c.closed || hook == nil
	c.mu.Unlock()
	if open {
		return
	}
	st := hook()
	t := &turn{sessionID: sessionID, sink: st, done: make(chan struct{}), changed: make(chan struct{}, 1), tools: map[string]openTool{},
		self: promptID, end: st.End}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn != nil {
		st.End(runturn.Result{Outcome: runturn.OutcomeCompleted})
		return
	}
	c.turn = t
}

// endSelfTurn ends the agent's own prompt promptID at its turn_completed, reporting its usage.
func (c *Client) endSelfTurn(promptID, stopReason string, update json.RawMessage) {
	c.mu.Lock()
	t := c.turn
	if t == nil || t.self != promptID {
		c.mu.Unlock()
		return
	}
	c.turn = nil
	obs := c.observer
	c.mu.Unlock()
	if u, ok := parseUsage(update); ok && obs != nil {
		if u.StopReason == "" {
			u.StopReason = stopReason
		}
		obs.Usage(u)
	}
	switch {
	case stopReason == "cancelled":
		t.finish(runturn.OutcomeAborted, "", "")
	case strings.HasPrefix(stopReason, "refusal"):
		t.finish(runturn.OutcomeFailed, "runtime_error", "The agent refused to answer.")
	default:
		t.finish(runturn.OutcomeCompleted, "", "")
	}
}

// bgTool is a tool call that went on in the background when its prompt ended: its end goes to that prompt's sink.
type bgTool struct {
	id, name, command, sessionID string
	sink                         runturn.Sink
}

// handOver moves the tools of a prompt that ended normally that go on in the background (Background.Holds) to the
// client, reporting each as a background task; the other open tools are closed with the turn.
func (c *Client) handOver(t *turn) {
	bg := c.o.Background
	if bg == nil {
		return
	}
	t.mu.Lock()
	var moved []*bgTool
	for id, tool := range t.tools {
		if tool.holds {
			moved = append(moved, &bgTool{id: id, name: tool.name, command: tool.command, sessionID: t.sessionID, sink: t.sink})
			delete(t.tools, id)
		}
	}
	t.mu.Unlock()
	if len(moved) == 0 {
		return
	}
	c.mu.Lock()
	for _, b := range moved {
		c.background[b.id] = b
	}
	obs := c.observer
	c.mu.Unlock()
	if bo, ok := obs.(BackgroundObserver); ok {
		for _, b := range moved {
			bo.TaskStarted(Task{ID: b.id, ToolCallID: b.id, Command: b.command})
		}
	}
}

// backgroundEnded ends a tool running in the background when the agent reports its end: on the prompt that started it,
// and as the end of its background task, with the exit code and output agy gives in rawOutput. False when the update is
// not about one.
func (c *Client) backgroundEnded(u sessionUpdate) bool {
	c.mu.Lock()
	b := c.background[u.ToolCallID]
	delete(c.background, u.ToolCallID)
	obs := c.observer
	c.mu.Unlock()
	if b == nil {
		return false
	}
	var out struct {
		Output   *string `json:"output"`
		ExitCode *int64  `json:"exit_code"`
	}
	_ = json.Unmarshal(u.RawOutput, &out)
	failed := u.Status == "failed" || out.ExitCode != nil && *out.ExitCode != 0
	b.sink.ToolEnded(b.id, b.name, failed)
	if bo, ok := obs.(BackgroundObserver); ok {
		bo.TaskEnded(TaskEnd{ID: b.id, ExitCode: out.ExitCode, Output: out.Output})
	}
	return true
}

// ownReply returns the turn an update belongs to: the one open, else, with Background, a reply the agent begins by
// itself (its first message or tool call with no turn open), which then stays open until Settle after its usage_update.
func (c *Client) ownReply(u sessionUpdate) *turn {
	c.mu.Lock()
	t, hook, session := c.turn, c.selfTurns, c.session
	begins := t == nil && hook != nil && !c.closed && c.o.Background != nil && u.SessionID == session &&
		(u.Kind == "agent_message_chunk" || u.Kind == "agent_thought_chunk" || u.Kind == "tool_call")
	c.mu.Unlock()
	if !begins {
		return t
	}
	st := hook()
	nt := &turn{sessionID: u.SessionID, sink: st, done: make(chan struct{}), changed: make(chan struct{}, 1), tools: map[string]openTool{},
		self: ownReply, end: st.End}
	c.mu.Lock()
	if c.turn != nil {
		t = c.turn
		c.mu.Unlock()
		st.End(runturn.Result{Outcome: runturn.OutcomeCompleted})
		return t
	}
	c.turn = nt
	c.mu.Unlock()
	go c.followOwnReply(nt)
	return nt
}

// followOwnReply ends a reply the agent gives by itself: Settle after its usage_update with nothing more, or
// ReplyQuietLimit after its last message.
func (c *Client) followOwnReply(t *turn) {
	bg := c.o.Background
	timer := time.NewTimer(bg.ReplyQuietLimit)
	defer timer.Stop()
	for {
		select {
		case <-t.changed:
			t.mu.Lock()
			wait := bg.ReplyQuietLimit
			if t.usageSeen {
				wait = bg.Settle
			}
			t.mu.Unlock()
			timer.Reset(wait)
		case <-timer.C:
			c.mu.Lock()
			if c.turn == t {
				c.turn = nil
			}
			c.mu.Unlock()
			t.finish(runturn.OutcomeCompleted, "", "")
			return
		case <-t.done:
			return
		}
	}
}

// CancelBackground ends the session's commands running in the background with session/cancel (Antigravity's only
// means; it ends all of them and says nothing about them): each is closed without a result on the prompt that started
// it and reported killed. With a turn open it refuses: the cancel would stop that turn too.
func (c *Client) CancelBackground(sessionID string) error {
	c.mu.Lock()
	if c.turn != nil {
		c.mu.Unlock()
		return errors.New("acp: a reply is in progress; its background commands cannot be stopped on their own")
	}
	c.mu.Unlock()
	if err := c.notify("session/cancel", map[string]any{"sessionId": sessionID}); err != nil {
		return err
	}
	c.backgroundCancelled(sessionID)
	return nil
}

// backgroundCancelled ends the session's background commands after a session/cancel (which ends them without a word).
func (c *Client) backgroundCancelled(sessionID string) {
	if c.o.Background == nil {
		return
	}
	c.mu.Lock()
	var ended []*bgTool
	for id, b := range c.background {
		if b.sessionID == sessionID {
			ended = append(ended, b)
			delete(c.background, id)
		}
	}
	obs := c.observer
	c.mu.Unlock()
	bo, _ := obs.(BackgroundObserver)
	for _, b := range ended {
		if s, ok := b.sink.(interface{ ToolNoResult(id, name string) }); ok {
			s.ToolNoResult(b.id, b.name)
		}
		if bo != nil {
			bo.TaskEnded(TaskEnd{ID: b.id, Killed: true})
		}
	}
}
