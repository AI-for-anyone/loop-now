package acp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
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

// turn is a prompt in progress, and with Background the time after it while the agent goes on with work it reports
// on by itself.
type turn struct {
	sessionID string
	sink      runturn.Sink
	done      chan struct{}
	bg        *Background
	// changed is nudged when a message comes while the turn waits after its prompt.
	changed chan struct{}

	mu        sync.Mutex
	text      strings.Builder
	tools     map[string]openTool
	seq       int
	cancelled bool
	finished  bool
	result    runturn.Result
	// settled: the prompt has returned while a holding tool was open, and the turn waits (see Background). replying:
	// a reply of the agent's own has begun and its usage_update has not come; expecting: a holding tool has ended since
	// the agent last began a reply, so a reply is to come. last is when the agent last sent anything.
	settled, replying, expecting bool
	last                         time.Time
	// self is the id of the prompt the agent runs by itself that this turn is; end is told how it ended.
	self string
	end  func(runturn.Result)
}

// openTool is a tool call that has started and not ended; holds: it keeps the turn open after the prompt (see
// Background.Holds).
type openTool struct {
	id, name string
	seq      int
	holds    bool
}

// Prompt sends one message (session/prompt) and waits for its stop reason, streaming text and tool calls to sink; the
// usage its result reports goes to the observer (see SetObserver). With Background the turn can go on after the prompt
// returns (see Background). Tools still open when the turn ends are closed without a result. It returns ErrExited when
// the process has already exited; when the process exits during the turn the result is failed with runtime_exited. When
// ctx ends it stops waiting.
func (c *Client) Prompt(ctx context.Context, sessionID, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error) {
	blocks := []any{map[string]any{"type": "text", "text": text}}
	for _, img := range images {
		blocks = append(blocks, map[string]any{"type": "image", "data": img.Data, "mimeType": img.MimeType})
	}
	t := &turn{sessionID: sessionID, sink: sink, done: make(chan struct{}), bg: c.o.Background, changed: make(chan struct{}, 1), tools: map[string]openTool{}}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return runturn.Result{}, ErrExited
	}
	c.session = sessionID
	if c.turn != nil && c.turn.self != "" {
		// The agent runs a prompt of its own: this one only starts after it. Wait for it to end, so its output is not
		// taken for the reply to this one.
		self := c.turn
		c.mu.Unlock()
		select {
		case <-self.done:
		case <-ctx.Done():
			return runturn.Result{}, ctx.Err()
		}
		return c.Prompt(ctx, sessionID, text, sink, images...)
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
	wait := time.NewTimer(time.Hour)
	wait.Stop()
	defer wait.Stop()
	rearm := func() {
		at, _, _ := t.deadline()
		wait.Reset(time.Until(at))
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
				c.observe(r.result)
				if t.stopped(out.StopReason) {
					rearm()
				}
			}
		case <-t.changed:
			rearm()
		case <-wait.C:
			at, limit, quiet := t.deadline()
			if time.Now().Before(at) {
				wait.Reset(time.Until(at))
				continue
			}
			if s, ok := t.sink.(interface{ BackgroundWaitTimedOut(time.Duration) }); ok && quiet {
				s.BackgroundWaitTimedOut(limit)
			}
			t.finish(runturn.OutcomeCompleted, "", "")
		case <-t.done:
			t.mu.Lock()
			defer t.mu.Unlock()
			return t.result, nil
		case <-ctx.Done():
			return runturn.Result{}, ctx.Err()
		}
	}
}

// Cancel asks the agent to stop the prompt in progress (session/cancel); the prompt then ends as cancelled. A turn that
// waits after its prompt (see Background) ends as cancelled at once: the agent ends its background commands.
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
	settled := t.settled
	t.mu.Unlock()
	err := c.notify("session/cancel", map[string]any{"sessionId": t.sessionID})
	if settled {
		t.finish(runturn.OutcomeAborted, "", "")
	}
	return err
}

// InPrompt reports whether a turn is in progress, also while it waits after its prompt.
func (c *Client) InPrompt() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn != nil
}

// sessionUpdate is what the client uses of one session/update notification.
type sessionUpdate struct {
	SessionID, Kind, ToolCallID, Title, ToolKind, Status string
	// Text is the text of a message or thought chunk (its content is one ContentBlock; a tool call's is a list of
	// ToolCallContent, which the client does not use).
	Text     string
	RawInput json.RawMessage
}

// parseUpdate reads a session/update notification field by field: a field of an unexpected type or shape (a tool
// call's content list, a title that is not a string) is left empty and the others are still used, so the status of a
// tool's end is never lost to its output. It fails only when there is no update object at all.
func parseUpdate(params json.RawMessage) (sessionUpdate, bool) {
	var p struct {
		SessionID json.RawMessage            `json:"sessionId"`
		Update    map[string]json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil || p.Update == nil {
		return sessionUpdate{}, false
	}
	str := func(raw json.RawMessage) string {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	u := sessionUpdate{SessionID: str(p.SessionID), Kind: str(p.Update["sessionUpdate"]), ToolCallID: str(p.Update["toolCallId"]),
		Title: str(p.Update["title"]), ToolKind: str(p.Update["kind"]), Status: str(p.Update["status"]), RawInput: p.Update["rawInput"]}
	switch u.Kind {
	case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
		var block map[string]json.RawMessage
		if json.Unmarshal(p.Update["content"], &block) == nil && str(block["type"]) == "text" {
			u.Text = str(block["text"])
		}
	}
	return u, true
}

func (c *Client) update(params json.RawMessage) {
	u, ok := parseUpdate(params)
	if !ok {
		return
	}
	c.mu.Lock()
	l, t := c.loading, c.turn
	if l != nil && l.sessionID == u.SessionID {
		switch u.Kind {
		case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan":
			l.count++
			l.last = time.Now()
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if t == nil || t.sessionID != u.SessionID || t.sink == nil {
		return
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	if t.settled {
		t.last = time.Now()
		switch u.Kind {
		case "agent_message_chunk", "agent_thought_chunk", "tool_call":
			t.replying, t.expecting = true, false
		case "usage_update":
			t.replying = false
		}
	}
	t.mu.Unlock()
	switch u.Kind {
	case "agent_message_chunk":
		if u.Text != "" {
			t.mu.Lock()
			t.text.WriteString(u.Text)
			t.mu.Unlock()
			t.sink.TextDelta(u.Text)
		}
	case "tool_call":
		name := u.ToolKind
		if name == "" || name == "other" {
			name = u.Title
		}
		holds := t.bg != nil && t.bg.Holds != nil && t.bg.Holds(u.ToolKind, u.RawInput)
		t.mu.Lock()
		_, seen := t.tools[u.ToolCallID]
		if !seen {
			t.seq++
			t.tools[u.ToolCallID] = openTool{id: u.ToolCallID, name: name, seq: t.seq, holds: holds}
		}
		t.mu.Unlock()
		if !seen {
			t.sink.ToolStarted(u.ToolCallID, name)
			var in struct {
				Command any `json:"command"`
			}
			_ = json.Unmarshal(u.RawInput, &in)
			if command := commandText(in.Command); command != "" {
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
	t.mu.Lock()
	settled := t.settled
	t.mu.Unlock()
	if settled {
		select {
		case t.changed <- struct{}{}:
		default:
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

// toolEnded reports the end of an open tool; a holding tool that ends after the prompt means the agent will reply to
// it.
func (t *turn) toolEnded(id string, failed bool) {
	t.mu.Lock()
	tool, ok := t.tools[id]
	if ok {
		delete(t.tools, id)
		if t.settled && tool.holds {
			t.expecting = true
		}
	}
	t.mu.Unlock()
	if ok {
		t.sink.ToolEnded(id, tool.name, failed)
	}
}

// stopped ends the prompt with the agent's stop reason: cancelled (or anything after a cancel) is a stop; refusal is
// a failure; the rest (end_turn, max_tokens, max_turn_requests) complete with the text so far, unless a holding tool
// is still open: then the turn waits (see Background) and stopped reports true. Whether it was cancelled and whether it
// waits are decided together, so a cancel either comes first or finds the turn waiting (and ends it).
func (t *turn) stopped(reason string) bool {
	t.mu.Lock()
	cancelled := t.cancelled
	waits := !cancelled && reason != "cancelled" && reason != "refusal" && t.holding()
	if waits {
		t.settled, t.replying, t.expecting, t.last = true, false, false, time.Now()
	}
	t.mu.Unlock()
	switch {
	case waits:
		return true
	case reason == "cancelled", cancelled:
		t.finish(runturn.OutcomeAborted, "", "")
	case reason == "refusal":
		t.finish(runturn.OutcomeFailed, executor.ErrRuntimeError, "The agent refused to answer this message.")
	default:
		t.finish(runturn.OutcomeCompleted, "", "")
	}
	return false
}

// holding reports whether an open tool keeps the turn open after its prompt; t.mu is held.
func (t *turn) holding() bool {
	if t.bg == nil {
		return false
	}
	for _, tool := range t.tools {
		if tool.holds {
			return true
		}
	}
	return false
}

// deadline is when a waiting turn ends if nothing more comes, and whether that is a quiet limit (and which).
func (t *turn) deadline() (at time.Time, limit time.Duration, quiet bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.holding() {
		return t.last.Add(t.bg.QuietLimit), t.bg.QuietLimit, true
	}
	if t.replying || t.expecting {
		return t.last.Add(t.bg.ReplyQuietLimit), t.bg.ReplyQuietLimit, true
	}
	return t.last.Add(t.bg.Settle), 0, false
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

// finish ends the turn once: the tools still open are closed without a result, in the order they started.
func (t *turn) finish(outcome runturn.Outcome, code, msg string) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.finished = true
	open := slices.SortedFunc(maps.Values(t.tools), func(a, b openTool) int { return a.seq - b.seq })
	t.tools = map[string]openTool{}
	t.mu.Unlock()
	if s, ok := t.sink.(interface{ ToolNoResult(id, name string) }); ok {
		for _, tool := range open {
			s.ToolNoResult(tool.id, tool.name)
		}
	}
	t.mu.Lock()
	t.result = runturn.Result{Outcome: outcome, Text: t.text.String(), ErrorCode: code, ErrorMessage: msg}
	result := t.result
	close(t.done)
	t.mu.Unlock()
	if t.end != nil {
		t.end(result)
	}
}

func rpcMessage(err error) string {
	var e *RPCError
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
