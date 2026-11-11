package ccstream

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/executor/local/runturn"
)

// wireLine 是一行输出中平台关心的字段；其余字段与不认识的类型忽略。
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

// turn 是进行中的一轮。text、tools 只由读协程访问；interrupted、signalled、ackedAt、initTimeout、detached 受 Client.mu 保护。
type turn struct {
	sink   runturn.Sink
	done   chan struct{}
	result runturn.Result

	finished    bool
	detached    bool // 调用方已不再等待：不再回调 sink
	interrupted bool
	signalled   bool
	ackedAt     time.Time
	initTimeout bool

	textMu sync.Mutex        // 进程退出时收尾的协程也要读 text
	text   strings.Builder   // 当前这条回复已收到的文字
	tools  map[string]string // 已开始的工具调用 id → 工具名（只由读协程访问）
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

// partial 返回当前这条回复已收到的文字。
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

// exited 在进程退出时结束这一轮：打断中的算打断；第一轮 init 之前退出是启动失败。
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

// handleLine 处理一行输出。只看顶层（parent_tool_use_id 为空）的文字与工具：子 Agent 的过程不转给 sink。
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
			c.init = &Init{SessionID: m.SessionID, Model: m.Model, Version: m.Version, Tools: m.Tools, MCPServers: m.MCPServers}
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
		if t == nil || m.ParentToolUseID != nil || !active {
			return
		}
		for _, b := range blocks(m.Message) {
			if b.Type != "tool_use" || b.Name != "Bash" {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
				if s, ok := t.sink.(interface {
					ToolCommand(id, name, command string)
				}); ok {
					// Claude Code 的 Bash 工具对应平台记录的 bash 命令。
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

// finishTurn 以 result 结束这一轮：先按模型报告用量，再给出结果。
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

// blocks 返回消息的内容块；内容是字符串时没有块。
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
