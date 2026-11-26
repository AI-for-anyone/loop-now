package ccstream

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
)

// wireLine holds the fields of an output line that the platform cares about; other fields and unknown types are
// ignored.
type wireLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Event           json.RawMessage `json:"event"`
	Message         json.RawMessage `json:"message"`
	RateLimitInfo   map[string]any  `json:"rate_limit_info"`
	Response        json.RawMessage `json:"response"`

	// system/init
	SessionID  string      `json:"session_id"`
	Model      string      `json:"model"`
	Version    string      `json:"claude_code_version"`
	Tools      []string    `json:"tools"`
	Skills     []string    `json:"skills"`
	MCPServers []MCPServer `json:"mcp_servers"`

	// result
	IsError        bool                      `json:"is_error"`
	Result         *string                   `json:"result"`
	NumTurns       *int                      `json:"num_turns"`
	TerminalReason string                    `json:"terminal_reason"`
	APIErrorStatus *int                      `json:"api_error_status"`
	Errors         []string                  `json:"errors"`
	ModelUsage     map[string]wireModelUsage `json:"model_usage_since_last_result"`

	// background tasks: system/background_tasks_changed, task_started, task_updated, task_notification
	Tasks          []wireTask `json:"tasks"`
	TaskID         string     `json:"task_id"`
	ToolUseID      string     `json:"tool_use_id"`
	TaskType       string     `json:"task_type"`
	Description    string     `json:"description"`
	IsBackgrounded *bool      `json:"is_backgrounded"`
	Patch          struct {
		Status         string `json:"status"`
		IsBackgrounded *bool  `json:"is_backgrounded"`
	} `json:"patch"`
	Status     string `json:"status"`
	OutputFile string `json:"output_file"`
	Summary    string `json:"summary"`
}

type wireTask struct {
	TaskID      string `json:"task_id"`
	ToolUseID   string `json:"tool_use_id"`
	TaskType    string `json:"task_type"`
	Description string `json:"description"`
}

type wireModelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	ThinkingTokens           int64 `json:"thinkingTokens"`
}

type wireEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type wireMessage struct {
	Content json.RawMessage `json:"content"`
}

type wireBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// turn is a turn in progress. text and tools are accessed only by the reader goroutine; interrupted, signalled,
// ackedAt, initTimeout and detached are guarded by Client.mu.
type turn struct {
	sink   runturn.Sink
	done   chan struct{}
	result runturn.Result

	finished bool
	detached bool // the caller no longer waits: sink is not called any more
	// self: Claude Code began this turn by itself; end is told how it ended; activity is nudged at each line while it
	// is open. settling: it began while settling background tasks an earlier process left running, and nothing of it is
	// recorded (it is self too, so a message waits for it).
	self        bool
	settling    bool
	end         func(runturn.Result)
	activity    chan struct{}
	interrupted bool
	signalled   bool
	ackedAt     time.Time
	initTimeout bool

	textMu sync.Mutex        // the goroutine that wraps up when the process exits also reads text
	text   strings.Builder   // text received so far for the current reply
	tools  map[string]string // tool call id -> tool name for started tool calls (accessed only by the reader goroutine)
}

func (t *turn) resetText() {
	t.textMu.Lock()
	t.text.Reset()
	t.textMu.Unlock()
}

func (t *turn) addText(s string) {
	t.textMu.Lock()
	t.text.WriteString(s)
	t.textMu.Unlock()
}

// partial returns the text received so far for the current reply.
func (t *turn) partial() string {
	t.textMu.Lock()
	defer t.textMu.Unlock()
	return t.text.String()
}

func newTurn(sink runturn.Sink) *turn {
	return &turn{sink: sink, done: make(chan struct{}), tools: map[string]string{}}
}

func (t *turn) finish(res runturn.Result) {
	if t.finished {
		return
	}
	t.finished = true
	t.result = res
	close(t.done)
	if t.end != nil {
		t.end(res)
	}
}

// beginsTurn reports whether a line with no turn open is the start of a turn Claude Code begins by itself.
func beginsTurn(m wireLine) bool {
	switch m.Type {
	case "system":
		return m.Subtype == "init"
	case "stream_event", "assistant":
		return m.ParentToolUseID == nil
	}
	return false
}

// selfTurn starts a turn Claude Code began by itself, when there is a place for it and no turn is open; it returns the
// turn now open. A turn it begins while background tasks of an earlier process are not settled yet is a settling turn,
// which goes nowhere.
func (c *Client) selfTurn(m wireLine) *turn {
	c.mu.Lock()
	t, hook := c.turn, c.selfTurns
	begins := t == nil && !c.exiting && !c.closed && beginsTurn(m)
	if begins && c.stale > 0 {
		nt := newTurn(nopSink{})
		nt.self, nt.settling, nt.activity = true, true, make(chan struct{}, 1)
		c.turn = nt
		c.mu.Unlock()
		go c.watchSelfTurn(nt)
		return nt
	}
	start := begins && hook != nil
	c.mu.Unlock()
	if !start {
		return t
	}
	st := hook()
	nt := newTurn(st)
	nt.self, nt.end, nt.activity = true, st.End, make(chan struct{}, 1)
	c.mu.Lock()
	if c.turn != nil {
		// A message was sent meanwhile: what follows is its turn.
		t := c.turn
		c.mu.Unlock()
		st.End(runturn.Result{Outcome: runturn.OutcomeCompleted})
		return t
	}
	c.turn = nt
	c.mu.Unlock()
	go c.watchSelfTurn(nt)
	return nt
}

// watchSelfTurn ends a turn Claude Code began by itself once no output has come for SelfQuiet: completed with the text
// so far.
func (c *Client) watchSelfTurn(t *turn) {
	timer := time.NewTimer(c.o.SelfQuiet)
	defer timer.Stop()
	for {
		select {
		case <-t.activity:
			timer.Reset(c.o.SelfQuiet)
		case <-t.done:
			return
		case <-timer.C:
			c.mu.Lock()
			if c.turn != t {
				c.mu.Unlock()
				return
			}
			c.turn = nil
			c.mu.Unlock()
			t.finish(runturn.Result{Outcome: runturn.OutcomeCompleted, Text: t.partial()})
			return
		}
	}
}

// background handles the system messages about background tasks (on the reader goroutine).
func (c *Client) background(m wireLine, obs Observer) {
	bo, _ := obs.(BackgroundObserver)
	started := func(t Task) {
		if c.tasks[t.ID] || c.ended[t.ID] || t.ID == "" {
			return
		}
		c.tasks[t.ID] = true
		if t.Command == "" && t.ToolUseID != "" {
			t.Command = c.commands[t.ToolUseID]
		}
		if bo != nil {
			bo.TaskStarted(t)
		}
	}
	switch m.Subtype {
	case "background_tasks_changed":
		// The list says what kind each task is; a task is reported when task_started (or task_updated) says it went to
		// the background, which names the tool call that started it.
		for _, t := range m.Tasks {
			if f, ok := c.foreground[t.TaskID]; ok && f.Type == "" {
				f.Type = t.TaskType
				c.foreground[t.TaskID] = f
			} else if !ok && !c.tasks[t.TaskID] {
				c.foreground[t.TaskID] = Task{ID: t.TaskID, ToolUseID: t.ToolUseID, Type: t.TaskType, Description: t.Description}
			}
		}
	case "task_started":
		t := Task{ID: m.TaskID, ToolUseID: m.ToolUseID, Type: m.TaskType, Description: m.Description}
		if prev, ok := c.foreground[m.TaskID]; ok && t.Type == "" {
			t.Type = prev.Type
		}
		if m.IsBackgrounded != nil && *m.IsBackgrounded {
			delete(c.foreground, m.TaskID)
			started(t)
		} else {
			c.foreground[m.TaskID] = t
		}
	case "task_updated":
		if m.Patch.IsBackgrounded != nil && *m.Patch.IsBackgrounded {
			t, ok := c.foreground[m.TaskID]
			if !ok {
				t = Task{ID: m.TaskID}
			}
			delete(c.foreground, m.TaskID)
			started(t)
		}
		switch m.Patch.Status {
		case "completed", "failed", "killed":
			delete(c.foreground, m.TaskID)
		}
	case "task_notification":
		_, fg := c.foreground[m.TaskID]
		delete(c.foreground, m.TaskID)
		if !c.tasks[m.TaskID] || c.ended[m.TaskID] {
			if !fg && !c.ended[m.TaskID] && m.TaskID != "" && !c.staleSeen[m.TaskID] {
				// A task this process never started: an earlier process left it running (the session was continued
				// with --resume), and Claude Code settles it with a turn of its own before reading the next message.
				c.staleSeen[m.TaskID] = true
				c.stale++
			}
			return
		}
		delete(c.tasks, m.TaskID)
		c.ended[m.TaskID] = true
		if bo != nil {
			bo.TaskEnded(TaskEnd{ID: m.TaskID, Status: m.Status, OutputFile: m.OutputFile, Summary: m.Summary})
		}
	}
}

// rememberCommand keeps the Bash command of a tool call, to label the background task it may start.
func (c *Client) rememberCommand(id, command string) {
	if len(c.commands) >= 256 {
		clear(c.commands)
	}
	c.commands[id] = command
}

// exited ends this turn when the process exits: if it was being interrupted it counts as interrupted; exiting before
// the first turn's init is a start failure.
func (t *turn) exited(started bool) {
	switch {
	case t.interrupted:
		t.finish(runturn.Result{Outcome: runturn.OutcomeAborted, Text: t.partial()})
	case !started && t.initTimeout:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code timed out while starting: it was not ready within the time limit."})
	case !started:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code exited before the first turn started."})
	default:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, Text: t.partial(), ErrorCode: codeExited, ErrorMessage: "The Claude Code process exited."})
	}
}

// handleLine handles one line of output. Only top-level (empty parent_tool_use_id) text and tools are considered:
// sub-Agent activity is not passed to the sink.
func (c *Client) handleLine(line []byte) {
	var m wireLine
	if json.Unmarshal(line, &m) != nil {
		return
	}
	t := c.selfTurn(m)
	if t != nil && t.self {
		select {
		case t.activity <- struct{}{}:
		default:
		}
	}
	switch {
	case m.Type == "system" && m.Subtype == "init":
		c.turnOutput = false
	case (m.Type == "stream_event" || m.Type == "assistant") && m.ParentToolUseID == nil:
		// The turns that settle earlier tasks show nothing: once a turn shows output, settling is over.
		c.turnOutput, c.stale = true, 0
	}
	c.mu.Lock()
	active := t != nil && !t.detached
	obs := c.observer
	c.mu.Unlock()
	switch m.Type {
	case "system":
		if m.Subtype == "init" {
			c.mu.Lock()
			c.init = &Init{SessionID: m.SessionID, Model: m.Model, Version: m.Version, Tools: m.Tools, Skills: m.Skills, MCPServers: m.MCPServers}
			c.mu.Unlock()
		}
		c.background(m, obs)
	case "stream_event":
		if t == nil || m.ParentToolUseID != nil {
			return
		}
		var ev wireEvent
		if json.Unmarshal(m.Event, &ev) != nil {
			return
		}
		switch {
		case ev.Type == "message_start":
			t.resetText()
		case ev.Type == "content_block_start" && ev.ContentBlock.Type == "tool_use" && ev.ContentBlock.ID != "":
			t.tools[ev.ContentBlock.ID] = ev.ContentBlock.Name
			if active {
				t.sink.ToolStarted(ev.ContentBlock.ID, ev.ContentBlock.Name)
			}
		case ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta":
			t.addText(ev.Delta.Text)
			if active {
				t.sink.TextDelta(ev.Delta.Text)
			}
		}
	case "assistant":
		if t == nil || m.ParentToolUseID != nil {
			return
		}
		for _, b := range blocks(m.Message) {
			if b.Type != "tool_use" || b.ID == "" {
				continue
			}
			if _, seen := t.tools[b.ID]; !seen {
				// A tool call not registered from the stream events (when the output format has changed) is registered
				// from the full message, so the tool end still matches up.
				t.tools[b.ID] = b.Name
				if active {
					t.sink.ToolStarted(b.ID, b.Name)
				}
			}
			if active {
				toolTarget(t.sink, b.ID, b.Name, b.Input)
			}
			if b.Name != "Bash" {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
				c.rememberCommand(b.ID, in.Command)
			}
			if active && in.Command != "" {
				if s, ok := t.sink.(interface {
					ToolCommand(id, name, command string)
				}); ok {
					// Claude Code's Bash tool corresponds to the bash command the platform records.
					s.ToolCommand(b.ID, "bash", in.Command)
				}
			}
		}
	case "user":
		if t == nil || m.ParentToolUseID != nil {
			return
		}
		for _, b := range blocks(m.Message) {
			if b.Type != "tool_result" {
				continue
			}
			name, ok := t.tools[b.ToolUseID]
			if !ok {
				continue
			}
			delete(t.tools, b.ToolUseID)
			if active {
				t.sink.ToolEnded(b.ToolUseID, name, b.IsError)
			}
		}
	case "rate_limit_event":
		if obs != nil && m.RateLimitInfo != nil {
			obs.RateLimit(m.RateLimitInfo, t != nil)
		}
	case "control_request":
		c.controlRequest(line)
	case "control_cancel_request":
		var r struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(line, &r) == nil {
			c.controlCancel(r.RequestID)
		}
	case "control_response":
		var r struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(m.Response, &r) != nil {
			return
		}
		c.mu.Lock()
		if ch, ok := c.controls[r.RequestID]; ok {
			delete(c.controls, r.RequestID)
			ch <- r.Subtype == "success"
		}
		c.mu.Unlock()
	case "result":
		c.finishTurn(t, m, obs)
	}
}

// finishTurn ends this turn with a result: it reports usage per model first, then gives the result.
func (c *Client) finishTurn(t *turn, m wireLine, obs Observer) {
	info := ResultInfo{Subtype: m.Subtype, IsError: m.IsError, TerminalReason: m.TerminalReason, APIErrorStatus: m.APIErrorStatus}
	if m.Result != nil {
		info.Result = *m.Result
	}
	if obs != nil {
		models := make([]string, 0, len(m.ModelUsage))
		for model := range m.ModelUsage {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			u := m.ModelUsage[model]
			out := Usage{Model: model, Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens}
			if u.ThinkingTokens > 0 {
				thinking := u.ThinkingTokens
				out.Reasoning = &thinking
			}
			obs.Usage(out, t != nil)
		}
	}
	empty := !info.IsError && info.Subtype == "success" && info.Result == "" && m.NumTurns != nil && *m.NumTurns == 0 && !c.turnOutput
	c.mu.Lock()
	if t != nil && c.turn == t && !t.self && empty && c.stale > 0 {
		// The empty turn Claude Code gives to settle background tasks an earlier process left running, before it reads
		// the message: the message's own turn follows.
		c.stale--
		c.mu.Unlock()
		return
	}
	if t != nil && t.settling {
		c.stale = max(c.stale-1, 0)
	} else {
		c.stale = 0
		c.last = info
	}
	if t == nil || c.turn != t {
		c.mu.Unlock()
		return
	}
	c.turn = nil
	interrupted := t.interrupted
	if interrupted {
		c.cancelAskingLocked(executor.CancelReplyStopped)
	} else {
		c.cancelAskingLocked(executor.CancelReplyEnded)
	}
	c.mu.Unlock()
	switch {
	case !info.IsError && info.Subtype == "success":
		text := info.Result
		if text == "" {
			text = t.partial()
		}
		t.finish(runturn.Result{Outcome: runturn.OutcomeCompleted, Text: text})
	case interrupted:
		t.finish(runturn.Result{Outcome: runturn.OutcomeAborted, Text: t.partial()})
	default:
		msg := strings.TrimSpace(info.Result)
		if msg == "" {
			msg = strings.TrimSpace(strings.Join(m.Errors, "; "))
		}
		if msg == "" {
			msg = "Claude Code reported that this turn ended with an error (" + info.Subtype + ", " + info.TerminalReason + ")."
		}
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, Text: t.partial(), ErrorCode: codeError, ErrorMessage: msg})
	}
}

// blocks returns the content blocks of a message; there are no blocks when the content is a string.
func blocks(raw json.RawMessage) []wireBlock {
	var msg wireMessage
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var out []wireBlock
	if json.Unmarshal(msg.Content, &out) != nil {
		return nil
	}
	return out
}

// toolTarget reports what a file or search tool of Claude Code works on: the file of Read, Edit, Write, MultiEdit and
// NotebookEdit, the pattern of Grep and Glob, the query of WebSearch. Nothing else of the input goes out.
func toolTarget(sink any, id, name string, input json.RawMessage) {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Pattern      string `json:"pattern"`
		Query        string `json:"query"`
	}
	if json.Unmarshal(input, &in) != nil {
		return
	}
	switch name {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		runturn.ToolFiles(sink, id, name, in.FilePath, in.NotebookPath)
	case "Grep", "Glob":
		runturn.ToolQuery(sink, id, name, in.Pattern)
	case "WebSearch":
		runturn.ToolQuery(sink, id, name, in.Query)
	}
}
