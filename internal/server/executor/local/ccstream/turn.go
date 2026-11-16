package ccstream

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

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
	TerminalReason string                    `json:"terminal_reason"`
	APIErrorStatus *int                      `json:"api_error_status"`
	Errors         []string                  `json:"errors"`
	ModelUsage     map[string]wireModelUsage `json:"model_usage_since_last_result"`
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

	finished    bool
	detached    bool // the caller no longer waits: sink is not called any more
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
}

// exited ends this turn when the process exits: if it was being interrupted it counts as interrupted; exiting before
// the first turn's init is a start failure.
func (t *turn) exited(started bool) {
	switch {
	case t.interrupted:
		t.finish(runturn.Result{Outcome: runturn.OutcomeAborted, Text: t.partial()})
	case !started && t.initTimeout:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code 启动超时：在时限内没有就绪"})
	case !started:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code 在第一轮开始前退出"})
	default:
		t.finish(runturn.Result{Outcome: runturn.OutcomeFailed, Text: t.partial(), ErrorCode: codeExited, ErrorMessage: "Claude Code 进程已退出"})
	}
}

// handleLine handles one line of output. Only top-level (empty parent_tool_use_id) text and tools are considered:
// sub-Agent activity is not passed to the sink.
func (c *Client) handleLine(line []byte) {
	var m wireLine
	if json.Unmarshal(line, &m) != nil {
		return
	}
	c.mu.Lock()
	t := c.turn
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
			if b.Name != "Bash" || !active {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
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
	c.mu.Lock()
	c.last = info
	if t == nil || c.turn != t {
		c.mu.Unlock()
		return
	}
	c.turn = nil
	interrupted := t.interrupted
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
			msg = "Claude Code 报告这一轮以错误结束（" + info.Subtype + "，" + info.TerminalReason + "）"
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
