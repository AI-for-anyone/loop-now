package codexapp

import (
	"context"
	"encoding/json"
	"strings"

	"loop-now/internal/server/executor"
)

// Approval is something Codex asks to do: run a command, change files, or get more permissions than its sandbox gives.
type Approval struct {
	Kind   string // executor.PermissionCommand, PermissionFileChange, PermissionNetwork or PermissionOther
	Title  string
	Detail string
}

// ApprovalFunc answers approval requests. It runs on its own goroutine and may wait for a person; ctx ends when the
// turn ends or is interrupted, or the process exits (its cause is an *executor.PermissionCancel).
type ApprovalFunc func(ctx context.Context, a Approval) (approved bool)

// SetApprovals sets who answers approval requests; nil grants them, as with approval policy never.
func (c *Client) SetApprovals(fn ApprovalFunc) {
	c.mu.Lock()
	c.approve = fn
	c.mu.Unlock()
}

// approvalMethods are the server requests that ask for an approval.
var approvalMethods = map[string]bool{"item/commandExecution/requestApproval": true, "item/fileChange/requestApproval": true,
	"item/permissions/requestApproval": true, "execCommandApproval": true, "applyPatchApproval": true}

// askApproval answers an approval request through the ApprovalFunc, on its own goroutine; false when no ApprovalFunc is
// set (the request is then granted as before).
func (c *Client) askApproval(id json.RawMessage, method string, params json.RawMessage) bool {
	c.mu.Lock()
	fn, closed := c.approve, c.closed
	c.mu.Unlock()
	if fn == nil || closed {
		return false
	}
	a := approvalOf(method, params)
	ctx, cancel := context.WithCancelCause(context.Background())
	key := string(id)
	c.mu.Lock()
	if c.asking == nil {
		c.asking = map[string]context.CancelCauseFunc{}
	}
	c.asking[key] = cancel
	c.mu.Unlock()
	go func() {
		approved := fn(ctx, a)
		c.mu.Lock()
		delete(c.asking, key)
		c.mu.Unlock()
		cancelled := ctx.Err() != nil
		cancel(nil)
		_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": approvalResult(method, params, approved, cancelled)})
	}()
	return true
}

// endApprovals cancels the requests still waiting: the turn ended (stopped, when interrupted) or the process exited.
func (c *Client) endApprovals(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, cancel := range c.asking {
		delete(c.asking, key)
		cancel(cause)
	}
}

// approvalResult is Codex's answer: accept or decline (cancel when the request was cancelled); for more permissions,
// the ones asked for or none.
func approvalResult(method string, params json.RawMessage, approved, cancelled bool) any {
	switch method {
	case "item/permissions/requestApproval":
		granted := json.RawMessage(`{}`)
		if approved {
			var p struct {
				Permissions json.RawMessage `json:"permissions"`
			}
			if json.Unmarshal(params, &p) == nil && len(p.Permissions) > 0 {
				granted = p.Permissions
			}
		}
		return map[string]any{"permissions": granted, "scope": "turn"}
	case "execCommandApproval", "applyPatchApproval":
		switch {
		case approved:
			return map[string]any{"decision": "approved"}
		case cancelled:
			return map[string]any{"decision": "abort"}
		}
		return map[string]any{"decision": "denied"}
	}
	switch {
	case approved:
		return map[string]any{"decision": "accept"}
	case cancelled:
		return map[string]any{"decision": "cancel"}
	}
	return map[string]any{"decision": "decline"}
}

// approvalOf is how an approval request is shown.
func approvalOf(method string, params json.RawMessage) Approval {
	var p struct {
		Command  any             `json:"command"`
		Cwd      string          `json:"cwd"`
		Reason   string          `json:"reason"`
		Changes  json.RawMessage `json:"changes"`
		FileDiff json.RawMessage `json:"fileChanges"`
		Perms    json.RawMessage `json:"permissions"`
		Network  json.RawMessage `json:"networkApprovalContext"`
	}
	_ = json.Unmarshal(params, &p)
	command := commandText(p.Command)
	switch method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		if len(p.Network) > 0 && string(p.Network) != "null" {
			return Approval{Kind: executor.PermissionNetwork, Title: firstLine("Network access for " + command), Detail: joinDetail(command, string(p.Network), p.Reason)}
		}
		return Approval{Kind: executor.PermissionCommand, Title: firstLine("Run " + command), Detail: joinDetail(command, p.Reason)}
	case "item/fileChange/requestApproval", "applyPatchApproval":
		title := "Change files"
		if p.Reason != "" {
			title += ": " + p.Reason
		}
		changes := p.Changes
		if len(changes) == 0 {
			changes = p.FileDiff
		}
		return Approval{Kind: executor.PermissionFileChange, Title: firstLine(title), Detail: joinDetail(string(changes))}
	}
	title := "More permissions"
	if p.Reason != "" {
		title += ": " + p.Reason
	}
	return Approval{Kind: executor.PermissionOther, Title: firstLine(title), Detail: joinDetail(string(p.Perms))}
}

// commandText is a command given as a string or as an argument list.
func commandText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, a := range c {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func joinDetail(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" && p != "null" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "\n\n")
}

// firstLine is the first line of s, at most 200 characters.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}
