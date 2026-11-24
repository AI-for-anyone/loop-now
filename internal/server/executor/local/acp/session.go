package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
)

// Agent is what the agent says about itself in initialize.
type Agent struct {
	ProtocolVersion int
	LoadSession     bool
	Images          bool
	// Meta is the agent's own _meta (for example the models Grok lists there).
	Meta json.RawMessage
}

// Initialize opens the connection. The client offers no file system and no terminal.
func (c *Client) Initialize(ctx context.Context, name, version string) (Agent, error) {
	var out struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession        bool `json:"loadSession"`
			PromptCapabilities struct {
				Image bool `json:"image"`
			} `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
		Meta json.RawMessage `json:"_meta"`
	}
	params := map[string]any{"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]any{"name": name, "version": version}}
	if err := c.Call(ctx, "initialize", params, &out); err != nil {
		return Agent{}, err
	}
	return Agent{ProtocolVersion: out.ProtocolVersion, LoadSession: out.AgentCapabilities.LoadSession, Images: out.AgentCapabilities.PromptCapabilities.Image, Meta: out.Meta}, nil
}

// MCPServer is an MCP server given to a session (the stdio kind, which every agent supports).
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

func mcpParams(servers []MCPServer) []any {
	out := []any{}
	for _, s := range servers {
		env := []any{}
		for k, v := range s.Env {
			env = append(env, map[string]any{"name": k, "value": v})
		}
		args := s.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, map[string]any{"name": s.Name, "command": s.Command, "args": args, "env": env})
	}
	return out
}

// Model is a model an agent offers in a session.
type Model struct {
	ID, Name string
	Meta     json.RawMessage
}

// Session is a session the agent opened or loaded.
type Session struct {
	ID     string
	Models []Model
	// Replayed is how many history updates the agent sent while loading the session (0 for a new one).
	Replayed int
}

type sessionResponse struct {
	SessionID string `json:"sessionId"`
	Models    struct {
		AvailableModels []struct {
			ModelID string          `json:"modelId"`
			Name    string          `json:"name"`
			Meta    json.RawMessage `json:"_meta"`
		} `json:"availableModels"`
	} `json:"models"`
}

func (r sessionResponse) session() Session {
	s := Session{ID: r.SessionID}
	for _, m := range r.Models.AvailableModels {
		s.Models = append(s.Models, Model{ID: m.ModelID, Name: m.Name, Meta: m.Meta})
	}
	return s
}

// NewSession opens a new session in cwd.
func (c *Client) NewSession(ctx context.Context, cwd string, mcp []MCPServer) (Session, error) {
	var out sessionResponse
	if err := c.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcpParams(mcp)}, &out); err != nil {
		return Session{}, err
	}
	if out.SessionID == "" {
		return Session{}, errors.New("acp: session/new returned no session id")
	}
	return out.session(), nil
}

// loading counts the history a session/load replays.
type loading struct {
	sessionID string
	count     int
	last      time.Time
}

// LoadSession continues a saved session; the agent replays its history as updates, which are counted and not passed
// on. Some agents never answer session/load: once history was replayed and the agent stayed quiet for LoadQuiet, the
// session counts as loaded.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string, mcp []MCPServer) (Session, error) {
	l := &loading{sessionID: sessionID, last: time.Now()}
	c.mu.Lock()
	c.loading = l
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.loading == l {
			c.loading = nil
		}
		c.mu.Unlock()
	}()
	ch, err := c.request("session/load", map[string]any{"sessionId": sessionID, "cwd": cwd, "mcpServers": mcpParams(mcp)})
	if err != nil {
		return Session{}, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case r := <-ch:
			if r.err != nil {
				return Session{}, r.err
			}
			var out sessionResponse
			_ = json.Unmarshal(r.result, &out)
			s := out.session()
			s.ID = sessionID
			c.mu.Lock()
			s.Replayed = l.count
			c.mu.Unlock()
			return s, nil
		case <-tick.C:
			c.mu.Lock()
			quiet := l.count > 0 && time.Since(l.last) >= c.o.LoadQuiet
			count := l.count
			c.mu.Unlock()
			if quiet {
				return Session{ID: sessionID, Replayed: count}, nil
			}
		case <-ctx.Done():
			return Session{}, ctx.Err()
		}
	}
}

// SetModel chooses the session's model (session/set_model).
func (c *Client) SetModel(ctx context.Context, sessionID, modelID string) error {
	return c.Call(ctx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": modelID}, nil)
}

// turn is a prompt in progress.
type turn struct {
	sessionID string
	sink      runturn.Sink
	done      chan struct{}

	mu        sync.Mutex
	text      strings.Builder
	tools     map[string]string
	cancelled bool
	finished  bool
	result    runturn.Result
}

// Prompt sends one message (session/prompt) and waits for its stop reason, streaming text and tool calls to sink.
// It returns ErrExited when the process has already exited; when the process exits during the prompt the result is
// failed with runtime_exited. When ctx ends it stops waiting.
func (c *Client) Prompt(ctx context.Context, sessionID, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error) {
	blocks := []any{map[string]any{"type": "text", "text": text}}
	for _, img := range images {
		blocks = append(blocks, map[string]any{"type": "image", "data": img.Data, "mimeType": img.MimeType})
	}
	t := &turn{sessionID: sessionID, sink: sink, done: make(chan struct{}), tools: map[string]string{}}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return runturn.Result{}, ErrExited
	}
	if c.turn != nil {
		c.mu.Unlock()
		return runturn.Result{}, errors.New("acp: a prompt is already in progress")
	}
	c.turn = t
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.turn == t {
			c.turn = nil
		}
		c.mu.Unlock()
	}()
	ch, err := c.request("session/prompt", map[string]any{"sessionId": sessionID, "prompt": blocks})
	if err != nil {
		return runturn.Result{}, err
	}
	for {
		select {
		case r := <-ch:
			ch = nil
			switch {
			case errors.Is(r.err, ErrExited):
				continue // the prompt ends with the process (t.exited)
			case r.err != nil:
				code := executor.ErrRuntimeError
				if IsAuthRequired(r.err) {
					code = executor.ErrRuntimeLoginRequired
				}
				t.finish(runturn.OutcomeFailed, code, rpcMessage(r.err))
			default:
				var out struct {
					StopReason string `json:"stopReason"`
				}
				_ = json.Unmarshal(r.result, &out)
				t.stopped(out.StopReason)
			}
		case <-t.done:
			t.mu.Lock()
			defer t.mu.Unlock()
			return t.result, nil
		case <-ctx.Done():
			return runturn.Result{}, ctx.Err()
		}
	}
}

// Cancel asks the agent to stop the prompt in progress (session/cancel); the prompt then ends as cancelled.
func (c *Client) Cancel() error {
	c.mu.Lock()
	t, closed := c.turn, c.closed
	c.mu.Unlock()
	if closed {
		return ErrExited
	}
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.cancelled = true
	t.mu.Unlock()
	return c.notify("session/cancel", map[string]any{"sessionId": t.sessionID})
}

// InPrompt reports whether a prompt is in progress.
func (c *Client) InPrompt() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn != nil
}

func (c *Client) update(params json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind    string `json:"sessionUpdate"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
			ToolKind   string `json:"kind"`
			Status     string `json:"status"`
			RawInput   struct {
				Command any `json:"command"`
			} `json:"rawInput"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	c.mu.Lock()
	l, t := c.loading, c.turn
	if l != nil && l.sessionID == p.SessionID {
		switch p.Update.Kind {
		case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan":
			l.count++
			l.last = time.Now()
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if t == nil || t.sessionID != p.SessionID || t.sink == nil {
		return
	}
	u := p.Update
	switch u.Kind {
	case "agent_message_chunk":
		if u.Content.Type == "text" && u.Content.Text != "" {
			t.mu.Lock()
			t.text.WriteString(u.Content.Text)
			t.mu.Unlock()
			t.sink.TextDelta(u.Content.Text)
		}
	case "tool_call":
		name := u.ToolKind
		if name == "" || name == "other" {
			name = u.Title
		}
		t.mu.Lock()
		_, seen := t.tools[u.ToolCallID]
		t.tools[u.ToolCallID] = name
		t.mu.Unlock()
		if !seen {
			t.sink.ToolStarted(u.ToolCallID, name)
			if command := commandText(u.RawInput.Command); command != "" {
				if s, ok := t.sink.(interface {
					ToolCommand(id, name, command string)
				}); ok {
					s.ToolCommand(u.ToolCallID, name, command)
				}
			}
		}
		if u.Status == "completed" || u.Status == "failed" {
			t.toolEnded(u.ToolCallID, u.Status == "failed")
		}
	case "tool_call_update":
		if u.Status == "completed" || u.Status == "failed" {
			t.toolEnded(u.ToolCallID, u.Status == "failed")
		}
	}
}

func commandText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func (t *turn) toolEnded(id string, failed bool) {
	t.mu.Lock()
	name, ok := t.tools[id]
	if ok {
		delete(t.tools, id)
	}
	t.mu.Unlock()
	if ok {
		t.sink.ToolEnded(id, name, failed)
	}
}

// stopped ends the prompt with the agent's stop reason: cancelled (or anything after a cancel) is a stop; refusal is
// a failure; the rest (end_turn, max_tokens, max_turn_requests) complete with the text so far.
func (t *turn) stopped(reason string) {
	t.mu.Lock()
	cancelled := t.cancelled
	t.mu.Unlock()
	switch {
	case reason == "cancelled", cancelled:
		t.finish(runturn.OutcomeAborted, "", "")
	case reason == "refusal":
		t.finish(runturn.OutcomeFailed, executor.ErrRuntimeError, "The agent refused to answer this message.")
	default:
		t.finish(runturn.OutcomeCompleted, "", "")
	}
}

// exited ends the prompt when the process exits: a stop when a cancel was asked for (the agent was killed after it),
// a failure otherwise.
func (t *turn) exited() {
	t.mu.Lock()
	cancelled := t.cancelled
	t.mu.Unlock()
	if cancelled {
		t.finish(runturn.OutcomeAborted, "", "")
		return
	}
	t.finish(runturn.OutcomeFailed, executor.ErrRuntimeExited, "The agent process exited.")
}

func (t *turn) finish(outcome runturn.Outcome, code, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.finished = true
	t.result = runturn.Result{Outcome: outcome, Text: t.text.String(), ErrorCode: code, ErrorMessage: msg}
	close(t.done)
}

func rpcMessage(err error) string {
	var e *RPCError
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
