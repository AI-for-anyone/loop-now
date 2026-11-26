package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
)

// TurnOptions are the settings of one turn. Model and effort are sent with every turn: Codex keeps a turn's overrides
// for the turns after it.
type TurnOptions struct {
	ThreadID string
	Model    string
	// Effort is the reasoning effort; empty leaves the model's default.
	Effort string
	Images []messageimage.Content
}

// TurnError is the error a failed turn reports.
type TurnError struct {
	Message        string          `json:"message"`
	CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
}

// Kind returns the error's kind (codexErrorInfo): a plain value such as unauthorized or usageLimitExceeded, or the
// name of an object variant such as httpConnectionFailed; empty when there is none.
func (e TurnError) Kind() string {
	var s string
	if json.Unmarshal(e.CodexErrorInfo, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(e.CodexErrorInfo, &m) == nil {
		for k := range m {
			return k
		}
	}
	return ""
}

// turn is a turn in progress. Its fields are guarded by mu: notifications arrive on the reader goroutine, Interrupt
// on another.
type turn struct {
	c        *Client
	sink     runturn.Sink
	threadID string
	done     chan struct{}

	mu              sync.Mutex
	id              string
	interruptWanted bool
	interruptSent   bool
	finished        bool
	result          runturn.Result
	text            strings.Builder
	lastMessage     string          // the agent message item text was last streamed for
	streamed        map[string]bool // agent message items that streamed deltas
	tools           map[string]string
	commands        map[string]Command // command items in progress, by item id
	failure         *TurnError
}

// Turn sends one message (turn/start) and waits until the turn ends (turn/completed). It returns ErrExited when the
// process has already exited; when the process exits during the turn the result is failed with runtime_exited. When
// ctx ends it stops waiting.
func (c *Client) Turn(ctx context.Context, text string, o TurnOptions, sink runturn.Sink) (runturn.Result, error) {
	input := []map[string]any{{"type": "text", "text": text}}
	for _, img := range o.Images {
		input = append(input, map[string]any{"type": "image", "url": "data:" + img.MimeType + ";base64," + img.Data})
	}
	params := map[string]any{"threadId": o.ThreadID, "input": input}
	if o.Model != "" {
		params["model"] = o.Model
	}
	if o.Effort != "" {
		params["effort"] = o.Effort
	}
	return c.runTurn(ctx, o.ThreadID, sink, "turn/start", params)
}

// Compact compacts the thread's context (thread/compact/start); Codex runs it as a turn.
func (c *Client) Compact(ctx context.Context, threadID string) (runturn.Result, error) {
	return c.runTurn(ctx, threadID, nil, "thread/compact/start", map[string]any{"threadId": threadID})
}

func (c *Client) runTurn(ctx context.Context, threadID string, sink runturn.Sink, method string, params map[string]any) (runturn.Result, error) {
	t := &turn{c: c, sink: sink, threadID: threadID, done: make(chan struct{}), streamed: map[string]bool{}, tools: map[string]string{}, commands: map[string]Command{}}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return runturn.Result{}, ErrExited
	}
	if c.turn != nil {
		c.mu.Unlock()
		return runturn.Result{}, errors.New("codexapp: a turn is already in progress")
	}
	c.turn = t
	c.mu.Unlock()
	defer func() {
		t.mu.Lock()
		started := t.id != ""
		t.mu.Unlock()
		c.mu.Lock()
		if c.turn == t {
			c.turn = nil
		}
		c.lastStarted = started
		c.mu.Unlock()
	}()
	ch, err := c.request(method, params)
	if err != nil {
		return runturn.Result{}, err
	}
	for {
		select {
		case r := <-ch:
			ch = nil
			if errors.Is(r.err, ErrExited) {
				continue // the turn ends with the process (t.exited)
			}
			if r.err != nil {
				t.fail(executor.ErrRuntimeError, "Codex refused the message: "+rpcMessage(r.err))
				continue
			}
			var resp struct {
				Turn struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if json.Unmarshal(r.result, &resp) == nil && resp.Turn.ID != "" {
				t.started(resp.Turn.ID)
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

// Interrupt stops the turn in progress (turn/interrupt). A turn that has not reported its id yet is stopped as soon
// as it does: an interrupt sent before that would be lost. With no turn in progress it does nothing.
func (c *Client) Interrupt() error {
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
	t.interruptWanted = true
	t.mu.Unlock()
	t.sendInterrupt()
	return nil
}

// LastTurnStarted reports whether the latest turn started: Codex gave it an id, so the thread is saved.
func (c *Client) LastTurnStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastStarted
}

// ThreadSaved reports whether a turn of this client has started, the one in progress or the latest: Codex then has the
// thread saved.
func (c *Client) ThreadSaved() bool {
	c.mu.Lock()
	t, started := c.turn, c.lastStarted
	c.mu.Unlock()
	if started || t == nil {
		return started
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id != ""
}

// InTurn reports whether a turn is in progress.
func (c *Client) InTurn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn != nil
}

func (t *turn) started(id string) {
	t.mu.Lock()
	if t.id == "" {
		t.id = id
	}
	t.mu.Unlock()
	t.sendInterrupt()
}

// sendInterrupt sends turn/interrupt once, when it is wanted and the turn's id is known.
func (t *turn) sendInterrupt() {
	t.mu.Lock()
	if !t.interruptWanted || t.interruptSent || t.id == "" || t.finished {
		t.mu.Unlock()
		return
	}
	t.interruptSent = true
	params := map[string]any{"threadId": t.threadID, "turnId": t.id}
	t.mu.Unlock()
	_, _ = t.c.request("turn/interrupt", params)
}

func (t *turn) notification(c *Client, method string, params json.RawMessage) {
	var p struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		ItemID   string          `json:"itemId"`
		Delta    string          `json:"delta"`
		Item     json.RawMessage `json:"item"`
		Turn     struct {
			ID     string     `json:"id"`
			Status string     `json:"status"`
			Error  *TurnError `json:"error"`
		} `json:"turn"`
		Error     *TurnError `json:"error"`
		WillRetry bool       `json:"willRetry"`
	}
	if json.Unmarshal(params, &p) != nil || p.ThreadID != t.threadID {
		return
	}
	switch method {
	case "turn/started":
		t.started(p.Turn.ID)
	case "item/agentMessage/delta":
		t.delta(p.ItemID, p.Delta)
	case "item/started":
		t.item(p.Item, false)
	case "item/completed":
		t.item(p.Item, true)
	case "error":
		if p.Error != nil && !p.WillRetry {
			t.mu.Lock()
			t.failure = p.Error
			t.mu.Unlock()
		}
	case "turn/completed":
		if p.Turn.Status == "interrupted" {
			c.endApprovals(executor.CancelReplyStopped)
		} else {
			c.endApprovals(executor.CancelReplyEnded)
		}
		t.completed(p.Turn.Status, p.Turn.Error)
	}
}

func (t *turn) delta(itemID, delta string) {
	if delta == "" {
		return
	}
	t.mu.Lock()
	var sep string
	if itemID != t.lastMessage && t.text.Len() > 0 {
		sep = "\n\n"
	}
	t.lastMessage = itemID
	t.streamed[itemID] = true
	t.text.WriteString(sep + delta)
	sink := t.sink
	t.mu.Unlock()
	if sink != nil {
		sink.TextDelta(sep + delta)
	}
}

// toolName is how a tool item shows in the run record; empty for items that are not tool calls.
func toolName(kind string, item map[string]any) string {
	switch kind {
	case "commandExecution":
		return "shell"
	case "fileChange":
		return "apply_patch"
	case "mcpToolCall":
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		return server + "." + tool
	case "dynamicToolCall":
		tool, _ := item["tool"].(string)
		return tool
	case "webSearch":
		return "web_search"
	case "imageGeneration":
		return "image_generation"
	case "collabAgentToolCall":
		return "agent"
	}
	return ""
}

func (t *turn) item(raw json.RawMessage, completed bool) {
	var item map[string]any
	if json.Unmarshal(raw, &item) != nil {
		return
	}
	kind, _ := item["type"].(string)
	id, _ := item["id"].(string)
	if kind == "agentMessage" {
		text, _ := item["text"].(string)
		t.mu.Lock()
		streamed := t.streamed[id]
		t.mu.Unlock()
		if completed && !streamed {
			t.delta(id, text) // a message that came whole, without deltas
		}
		return
	}
	name := toolName(kind, item)
	if name == "" || t.sink == nil {
		return
	}
	if !completed {
		t.mu.Lock()
		t.tools[id] = name
		if kind == "commandExecution" {
			command, _ := item["command"].(string)
			pid, _ := item["processId"].(string)
			t.commands[id] = Command{ItemID: id, Command: command, ProcessID: pid}
		}
		t.mu.Unlock()
		t.sink.ToolStarted(id, name)
		if command, _ := item["command"].(string); command != "" {
			if s, ok := t.sink.(interface {
				ToolCommand(id, name, command string)
			}); ok {
				s.ToolCommand(id, name, command)
			}
		}
		return
	}
	t.mu.Lock()
	delete(t.tools, id)
	delete(t.commands, id)
	t.mu.Unlock()
	status, _ := item["status"].(string)
	failed := status == "failed" || status == "declined"
	if code, ok := item["exitCode"].(float64); ok && code != 0 {
		failed = true
	}
	if success, ok := item["success"].(bool); ok && !success {
		failed = true
	}
	t.sink.ToolEnded(id, name, failed)
}

func (t *turn) completed(status string, turnErr *TurnError) {
	t.background()
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	text := t.text.String()
	wanted := t.interruptWanted
	failure := turnErr
	if failure == nil {
		failure = t.failure
	}
	t.mu.Unlock()
	switch {
	case status == "interrupted", status == "completed" && wanted:
		// A turn that completes right after a stop counts as stopped, as the person asked.
		t.finish(runturn.Result{Outcome: runturn.OutcomeAborted, Text: text})
	case status == "completed":
		t.finish(runturn.Result{Outcome: runturn.OutcomeCompleted, Text: text})
	default:
		code, msg := executor.ErrRuntimeError, "Codex ended this turn with an error."
		if failure != nil {
			if failure.Message != "" {
				msg = failure.Message
			}
			if failure.Kind() == "unauthorized" {
				code = executor.ErrRuntimeLoginRequired
			}
		}
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, Text: text, ErrorCode: code, ErrorMessage: msg})
	}
}

// background hands the commands still running as the turn completes over to the client: their ends come later, with
// no turn, and go to this turn's sink. Each is reported as running in the background.
func (t *turn) background() {
	t.mu.Lock()
	var moved []*bgCommand
	for id, cmd := range t.commands {
		moved = append(moved, &bgCommand{Command: cmd, threadID: t.threadID, name: t.tools[id], sink: t.sink})
		delete(t.commands, id)
		delete(t.tools, id)
	}
	t.mu.Unlock()
	if len(moved) == 0 || t.sink == nil {
		return
	}
	t.c.mu.Lock()
	for _, b := range moved {
		t.c.background[b.ItemID] = b
	}
	obs := t.c.observer
	t.c.mu.Unlock()
	if bo, ok := obs.(BackgroundObserver); ok {
		for _, b := range moved {
			bo.CommandBackgrounded(b.Command)
		}
	}
}

func (t *turn) fail(code, msg string) {
	t.mu.Lock()
	text := t.text.String()
	t.mu.Unlock()
	t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, Text: text, ErrorCode: code, ErrorMessage: msg})
}

// exited ends the turn when the process exits during it.
func (t *turn) exited() {
	t.fail(executor.ErrRuntimeExited, "The Codex process exited.")
}

func (t *turn) finish(r runturn.Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.finished = true
	t.result = r
	close(t.done)
}

func rpcMessage(err error) string {
	var e *RPCError
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
