package acp

import (
	"context"
	"encoding/json"
)

// PermissionRequest is a session/request_permission from the agent: the tool call it wants to make.
type PermissionRequest struct {
	Title    string
	Kind     string // the ACP tool kind: read, edit, delete, move, search, execute, think, fetch, other
	RawInput json.RawMessage
	Content  json.RawMessage
}

// PermissionFunc answers permission requests. It runs on its own goroutine and may wait for a person; ctx ends when the
// prompt ends or is cancelled, or the process exits (its cause is an *executor.PermissionCancel).
type PermissionFunc func(ctx context.Context, r PermissionRequest) (approved bool)

// SetPermissions sets who answers permission requests; nil answers them by Options.RejectPermissions as before.
func (c *Client) SetPermissions(fn PermissionFunc) {
	c.mu.Lock()
	c.permit = fn
	c.mu.Unlock()
}

// SetMode switches the session's mode (session/set_mode) to one of Session.Modes.
func (c *Client) SetMode(ctx context.Context, sessionID, modeID string) error {
	return c.Call(ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": modeID}, nil)
}

type permissionParams struct {
	ToolCall struct {
		Title    string          `json:"title"`
		Kind     string          `json:"kind"`
		RawInput json.RawMessage `json:"rawInput"`
		Content  json.RawMessage `json:"content"`
	} `json:"toolCall"`
	Options []struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// askPermission answers a permission request through the PermissionFunc, on its own goroutine; false when none is set.
func (c *Client) askPermission(id json.RawMessage, params json.RawMessage) bool {
	c.mu.Lock()
	fn, closed := c.permit, c.closed
	c.mu.Unlock()
	if fn == nil || closed {
		return false
	}
	var p permissionParams
	_ = json.Unmarshal(params, &p)
	ctx, cancel := context.WithCancelCause(context.Background())
	key := string(id)
	c.mu.Lock()
	if c.asking == nil {
		c.asking = map[string]context.CancelCauseFunc{}
	}
	c.asking[key] = cancel
	c.mu.Unlock()
	go func() {
		approved := fn(ctx, PermissionRequest{Title: p.ToolCall.Title, Kind: p.ToolCall.Kind, RawInput: p.ToolCall.RawInput, Content: p.ToolCall.Content})
		c.mu.Lock()
		delete(c.asking, key)
		c.mu.Unlock()
		cancelled := ctx.Err() != nil
		cancel(nil)
		outcome := map[string]any{"outcome": "cancelled"}
		if !cancelled {
			// Once only: an "always" option would make the agent stop asking for good, whatever the mode later is.
			kinds := []string{"reject_once", "reject_always"}
			if approved {
				kinds = []string{"allow_once", "allow_always"}
			}
			if choice := pickOption(p, kinds); choice != "" {
				outcome = map[string]any{"outcome": "selected", "optionId": choice}
			}
		}
		_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"outcome": outcome}})
	}()
	return true
}

func pickOption(p permissionParams, kinds []string) string {
	for _, kind := range kinds {
		for _, o := range p.Options {
			if o.Kind == kind {
				return o.OptionID
			}
		}
	}
	return ""
}

// endPermissions cancels the requests still waiting: the prompt ended (stopped, when cancelled) or the process exited.
func (c *Client) endPermissions(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, cancel := range c.asking {
		delete(c.asking, key)
		cancel(cause)
	}
}
