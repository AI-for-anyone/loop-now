package pirpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"loop-now/internal/server/executor/local/runturn"
)

// responseFrame is a Pi command response.
type responseFrame struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// wireMessage is a subset of the message field in events.
type wireMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
}

// handleLine parses one line and dispatches it; unparseable lines are counted and skipped, unknown types are ignored.
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
		return // e.g. a parse error response, which cannot be matched to a command
	}
	c.mu.Lock()
	ch := c.pending[r.ID]
	delete(c.pending, r.ID)
	t := c.turn
	c.mu.Unlock()
	// Mark the turn as accepted inside the reader goroutine first, so the events after it are handled as part of this
	// turn.
	if t != nil && t.promptID == r.ID {
		t.responseReceived(r.Success)
	}
	if ch != nil {
		ch <- r
	}
}

// endedMessage is an assistant message that has ended.
type endedMessage struct {
	text         string
	stopReason   string
	errorMessage string
	superseded   bool // replaced by a later message from an automatic retry after an error
}

// turn is the state of one prompt turn; all event handling happens on the reader goroutine.
type turn struct {
	promptID  string
	sink      TurnSink
	responded chan struct{}   // closed when the prompt response arrives or the turn ends
	result    chan TurnResult // capacity 1

	mu            sync.Mutex
	detached      bool
	accepted      bool
	finished      bool
	respondedShut bool
	ended         []endedMessage
	partial       strings.Builder // delta text of the assistant message that has not ended yet
	notices       []CommandNotice // extensions' ctx.ui.notify in this turn (returned with the result when Pi handles a command itself)
	noticeBytes   int
	noticesCut    bool
	// abortRequested: the platform sent abort (Abort) during this turn. Manual compaction uses it to tell a platform
	// cancel from an extension cancel.
	abortRequested atomic.Bool
}

// CommandNotice is one message an extension gave with ctx.ui.notify during this turn (the extension's original text,
// plain text).
type CommandNotice = runturn.CommandNotice

// The most notices collected per turn: by count and by total bytes; anything beyond is truncated and flagged.
const (
	MaxCommandNotices     = 50
	MaxCommandNoticeBytes = 16 << 10
)

// addNotice collects one notice; past the limit it truncates (the last one is cut to the remaining bytes at a UTF-8
// boundary) and flags it.
func (t *turn) addNotice(level, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactive() || t.noticesCut {
		return
	}
	if len(t.notices) >= MaxCommandNotices {
		t.noticesCut = true
		return
	}
	if left := MaxCommandNoticeBytes - t.noticeBytes; len(message) > left {
		message = strings.ToValidUTF8(message[:max(left, 0)], "")
		t.noticesCut = true
		if message == "" {
			return
		}
	}
	t.noticeBytes += len(message)
	t.notices = append(t.notices, CommandNotice{Level: level, Message: message})
}

// commandNotices returns the collected notices and whether they were truncated.
func (t *turn) commandNotices() ([]CommandNotice, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]CommandNotice(nil), t.notices...), t.noticesCut
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

// active reports whether the turn is still in progress (used to decide whether usage belongs to this turn).
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

// supersedeErrors marks earlier assistant messages that ended with an error as superseded by a retry.
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
		// An agent_settled before the prompt response belongs to the previous run: discard the accumulated state.
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
			msg = "Pi reported that this turn ended with an error."
		}
		res = TurnResult{Outcome: OutcomeFailed, Text: t.text(false), ErrorCode: ErrorCodePi, ErrorMessage: msg}
	default:
		res = TurnResult{Outcome: OutcomeCompleted, Text: t.text(false)}
	}
	t.finish(res)
}

// exited is called when stdout ends: the turn fails with pi_exited, keeping the text produced so far.
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
		ErrorMessage: "The Pi process exited unexpectedly.",
	})
}

func (t *turn) finish(res TurnResult) {
	t.finished = true
	t.result <- res
	t.closeResponded()
}

// text joins this turn's reply text by the rules: error messages superseded by a retry are skipped; with withPartial
// the delta of the unfinished message is appended.
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

// contentText joins the text blocks of a message's content; content may also be a string.
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
