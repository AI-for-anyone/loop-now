package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
