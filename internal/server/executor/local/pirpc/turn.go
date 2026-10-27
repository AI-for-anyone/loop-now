package pirpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// responseFrame 是 Pi 的命令响应。
type responseFrame struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// wireMessage 是事件中消息字段的子集。
type wireMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
}

// handleLine 解析一行并分发；无法解析的行计数后跳过，未知类型忽略。
func (c *Client) handleLine(line []byte) {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		c.malformed.Add(1)
		return
	}
	if head.Type == "message_end" {
		c.observeMessageEnd(line)
	}
	c.observeLine(head.Type, line)
	switch head.Type {
	case "extension_ui_request":
		c.extensionUI(line)
	case "response":
		var r responseFrame
		if err := json.Unmarshal(line, &r); err != nil {
			c.malformed.Add(1)
			return
		}
		c.routeResponse(&r)
	case "message_start", "message_end":
		var ev struct {
			Message *wireMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			c.malformed.Add(1)
			return
		}
		if ev.Message == nil || ev.Message.Role != "assistant" {
			return
		}
		if t := c.activeTurn(); t != nil {
			if head.Type == "message_start" {
				t.assistantStarted()
			} else {
				t.assistantEnded(ev.Message)
			}
		}
	case "message_update":
		var ev struct {
			AssistantMessageEvent struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			c.malformed.Add(1)
			return
		}
		if ev.AssistantMessageEvent.Type != "text_delta" {
			return
		}
		if t := c.activeTurn(); t != nil {
			t.textDelta(ev.AssistantMessageEvent.Delta)
		}
	case "tool_execution_start", "tool_execution_end":
		var ev struct {
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			IsError    bool            `json:"isError"`
			Args       json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			c.malformed.Add(1)
			return
		}
		if t := c.activeTurn(); t != nil {
			t.tool(head.Type == "tool_execution_end", ev.ToolCallID, ev.ToolName, ev.IsError, ev.Args)
		}
	case "agent_settled":
		var ev struct {
			Aborted bool `json:"aborted"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			c.malformed.Add(1)
			return
		}
		if t := c.activeTurn(); t != nil {
			t.settled(ev.Aborted)
		}
	}
}

func (c *Client) routeResponse(r *responseFrame) {
	if r.ID == "" {
		return // 如 parse 错误响应，无法关联
	}
	c.mu.Lock()
	ch := c.pending[r.ID]
	delete(c.pending, r.ID)
	t := c.turn
	c.mu.Unlock()
	// 先在读协程内标记本轮已被接受，保证其后的事件按本轮处理。
	if t != nil && t.promptID == r.ID {
		t.responseReceived(r.Success)
	}
	if ch != nil {
		ch <- r
	}
}

// endedMessage 是一条已结束的助手消息。
type endedMessage struct {
	text         string
	stopReason   string
	errorMessage string
	superseded   bool // 出错后被自动重试的后续消息取代
}

// turn 是一轮 prompt 的状态，事件处理都在读协程中进行。
type turn struct {
	promptID  string
	sink      TurnSink
	responded chan struct{}   // 收到 prompt 响应或本轮结束时关闭
	result    chan TurnResult // 容量 1

	mu            sync.Mutex
	detached      bool
	accepted      bool
	finished      bool
	respondedShut bool
	ended         []endedMessage
	partial       strings.Builder // 当前尚未结束的助手消息的增量文本
}

func newTurn(promptID string, sink TurnSink) *turn {
	return &turn{
		promptID:  promptID,
		sink:      sink,
		responded: make(chan struct{}),
		result:    make(chan TurnResult, 1),
	}
}

func (t *turn) inactive() bool { return t.detached || t.finished }

// active 报告本轮是否仍在进行（用于判断用量是否属于本轮）。
func (t *turn) active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.inactive()
}

func (t *turn) closeResponded() {
	if !t.respondedShut {
		t.respondedShut = true
		close(t.responded)
	}
}

func (t *turn) detach() {
	t.mu.Lock()
	t.detached = true
	t.closeResponded()
	t.mu.Unlock()
}

func (t *turn) responseReceived(success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if success {
		t.accepted = true
	}
	t.closeResponded()
}

// supersedeErrors 把此前以 error 结束的助手消息标记为已被重试取代。
func (t *turn) supersedeErrors() {
	for i := range t.ended {
		if t.ended[i].stopReason == "error" {
			t.ended[i].superseded = true
		}
	}
}

func (t *turn) assistantStarted() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	t.supersedeErrors()
	t.partial.Reset()
}

func (t *turn) assistantEnded(m *wireMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	t.supersedeErrors()
	t.partial.Reset()
	t.ended = append(t.ended, endedMessage{
		text:         contentText(m.Content),
		stopReason:   m.StopReason,
		errorMessage: m.ErrorMessage,
	})
}

func (t *turn) textDelta(delta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	t.partial.WriteString(delta)
	t.sink.TextDelta(delta)
}

func (t *turn) tool(end bool, id, name string, isError bool, args json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	if end {
		t.sink.ToolEnded(id, name, isError)
	} else {
		// Only bash.command is observed; arbitrary tool arguments and results
		// never enter the public run-record path. Old sinks remain compatible.
		if name == "bash" {
			if s, ok := t.sink.(interface{ ToolCommand(string, string, string) }); ok {
				var input struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(args, &input) == nil && input.Command != "" {
					s.ToolCommand(id, name, input.Command)
				}
			}
		}
		t.sink.ToolStarted(id, name)
	}
}

func (t *turn) settled(aborted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	if !t.accepted {
		// prompt 响应之前的 agent_settled 属于上一次运行，丢弃已累积的状态。
		t.ended = nil
		t.partial.Reset()
		return
	}
	var res TurnResult
	switch {
	case aborted:
		res = TurnResult{Outcome: OutcomeAborted, Text: t.text(true)}
	case len(t.ended) > 0 && t.ended[len(t.ended)-1].stopReason == "error":
		msg := t.ended[len(t.ended)-1].errorMessage
		if msg == "" {
			msg = "Pi 报告本轮以错误结束"
		}
		res = TurnResult{Outcome: OutcomeFailed, Text: t.text(false), ErrorCode: ErrorCodePi, ErrorMessage: msg}
	default:
		res = TurnResult{Outcome: OutcomeCompleted, Text: t.text(false)}
	}
	t.finish(res)
}

// exited 在 stdout 结束时调用：本轮以 pi_exited 失败，保留已产生的文字。
func (t *turn) exited() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() {
		return
	}
	t.finish(TurnResult{
		Outcome:      OutcomeFailed,
		Text:         t.text(true),
		ErrorCode:    ErrorCodePiExited,
		ErrorMessage: "Pi 进程意外退出",
	})
}

func (t *turn) finish(res TurnResult) {
	t.finished = true
	t.result <- res
	t.closeResponded()
}

// text 按规则拼接本轮的回复文本：跳过被重试取代的出错消息；withPartial 时追加未结束消息的增量。
func (t *turn) text(withPartial bool) string {
	var parts []string
	for _, m := range t.ended {
		if m.superseded {
			continue
		}
		if s := strings.TrimSpace(m.text); s != "" {
			parts = append(parts, s)
		}
	}
	if withPartial {
		if s := strings.TrimSpace(t.partial.String()); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// contentText 拼接消息内容中的文本块；content 也可能是字符串。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
