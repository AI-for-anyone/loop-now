package ccstream

import (
	"context"
	"encoding/json"

	"loop-now/internal/server/executor"
)

// ToolPermission is a can_use_tool request: with --permission-prompts host, Claude Code asks the host whether it may use
// a tool.
type ToolPermission struct {
	ToolName    string
	DisplayName string // how Claude Code names the tool, when it says
	Description string // what the call does, as Claude Code describes it
	Input       json.RawMessage
	ToolUseID   string
	// Reason is why Claude Code asks (decision_reason), or the path that is blocked (blocked_path), when it says.
	Reason string
}

// ToolDecision answers a ToolPermission: Allow, or a denial with Message (what Claude Code is told).
type ToolDecision struct {
	Allow   bool
	Message string
}

// PermissionFunc answers can_use_tool requests. It runs on its own goroutine and may wait for a person; ctx ends when
// the turn ends, is stopped, or the process exits (its cause is an *executor.PermissionCancel), or when Claude Code
// cancels the request.
type PermissionFunc func(ctx context.Context, p ToolPermission) ToolDecision

// SetPermissions sets who answers can_use_tool requests; nil denies them.
func (c *Client) SetPermissions(fn PermissionFunc) {
	c.mu.Lock()
	c.permit = fn
	c.mu.Unlock()
}

// wireControlRequest is a control_request Claude Code sends to the host.
type wireControlRequest struct {
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype            string          `json:"subtype"`
		ToolName           string          `json:"tool_name"`
		DisplayName        string          `json:"display_name"`
		Description        string          `json:"description"`
		Input              json.RawMessage `json:"input"`
		ToolUseID          string          `json:"tool_use_id"`
		DecisionReason     string          `json:"decision_reason"`
		DecisionReasonType string          `json:"decision_reason_type"`
		BlockedPath        string          `json:"blocked_path"`
	} `json:"request"`
}

// controlRequest answers a request from Claude Code: can_use_tool goes to the PermissionFunc (on its own goroutine, so
// the output keeps being read); anything else is answered with an error, so Claude Code does not wait for it.
func (c *Client) controlRequest(line []byte) {
	var r wireControlRequest
	if json.Unmarshal(line, &r) != nil || r.RequestID == "" {
		return
	}
	if r.Request.Subtype != "can_use_tool" {
		c.controlReply(r.RequestID, nil, "not supported by this host: "+r.Request.Subtype)
		return
	}
	p := ToolPermission{ToolName: r.Request.ToolName, DisplayName: r.Request.DisplayName, Description: r.Request.Description, Input: r.Request.Input,
		ToolUseID: r.Request.ToolUseID, Reason: r.Request.DecisionReason}
	switch {
	case p.Reason == "" && r.Request.BlockedPath != "":
		p.Reason = "Blocked path: " + r.Request.BlockedPath
	case p.Reason == "":
		p.Reason = r.Request.DecisionReasonType
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	c.mu.Lock()
	fn := c.permit
	if c.closed {
		c.mu.Unlock()
		cancel(executor.CancelProcessExited)
		return
	}
	if c.asking == nil {
		c.asking = map[string]context.CancelCauseFunc{}
	}
	c.asking[r.RequestID] = cancel
	c.mu.Unlock()
	go func() {
		d := ToolDecision{Message: "No one can answer permission requests here."}
		if fn != nil {
			d = fn(ctx, p)
		}
		c.mu.Lock()
		_, still := c.asking[r.RequestID]
		delete(c.asking, r.RequestID)
		c.mu.Unlock()
		cancel(nil)
		if !still {
			return // cancelled: Claude Code no longer waits, or the process is gone
		}
		resp := map[string]any{"behavior": "deny", "message": d.Message}
		if d.Allow {
			input := p.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			resp = map[string]any{"behavior": "allow", "updatedInput": input}
		}
		c.controlReply(r.RequestID, resp, "")
	}()
}

// controlCancel is Claude Code giving up a request it sent (control_cancel_request), as when the turn is interrupted.
func (c *Client) controlCancel(requestID string) {
	c.mu.Lock()
	cancel := c.asking[requestID]
	delete(c.asking, requestID)
	interrupted := c.turn != nil && c.turn.interrupted
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	if interrupted {
		cancel(executor.CancelReplyStopped)
	} else {
		cancel(executor.CancelReplyEnded)
	}
}

// cancelAskingLocked cancels every request still waiting, with cause; the caller holds c.mu.
func (c *Client) cancelAskingLocked(cause error) {
	for id, cancel := range c.asking {
		delete(c.asking, id)
		cancel(cause)
	}
}

// controlReply answers a control_request: with response on success, else with the error message.
func (c *Client) controlReply(requestID string, response map[string]any, errMsg string) {
	body := map[string]any{"subtype": "success", "request_id": requestID, "response": response}
	if errMsg != "" {
		body = map[string]any{"subtype": "error", "request_id": requestID, "error": errMsg}
	}
	b, err := json.Marshal(map[string]any{"type": "control_response", "response": body})
	if err == nil {
		_ = c.write(b)
	}
}
