package pirpc

import (
	"context"
	"encoding/json"
	"time"

	"loop-now/internal/server/executor/local/runturn"
)

// Usage is one usage report from Pi (the Usage of Pi 1.1.0 pi-ai, see dist/types.d.ts). Cost fields are not parsed.
type Usage struct {
	Kind          string // assistant / tool / compaction / cache_warm / other
	Provider      string
	Model         string
	ResponseModel string
	StopReason    string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheWrite    int64
	Reasoning     *int64
	DurationMs    *int64
}

// Usage kinds.
const (
	UsageAssistant  = "assistant"
	UsageTool       = "tool"
	UsageCompaction = "compaction"
	UsageCacheWarm  = "cache_warm"
	UsageOther      = "other"
)

// Notice is a Pi event that is useful for run records (see runturn.Notice).
type Notice = runturn.Notice

// Notice types, see runturn.
const (
	NoticeRetryStarted = runturn.NoticeRetryStarted
	NoticeRetryEnded   = runturn.NoticeRetryEnded
	NoticeCompaction   = runturn.NoticeCompaction
	NoticeExtensionUI  = runturn.NoticeExtensionUI
)

// Observer receives usage and events whether or not a turn is in progress; inTurn tells whether it happened during a
// prompt turn. Calls happen on the reader goroutine; implementations must return quickly and must not call Client
// methods synchronously from a callback.
type Observer interface {
	Usage(u Usage, inTurn bool)
	Notice(n Notice, inTurn bool)
}

// SetObserver sets the observer; usage and events received afterwards go to it.
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// observe returns the current observer and whether a turn is in progress right now.
func (c *Client) observe() (Observer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.turn
	return c.observer, t != nil && t.active()
}

// wireUsage is the JSON shape of Pi's Usage.
type wireUsage struct {
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Reasoning  *int64 `json:"reasoning"`
}

func (w *wireUsage) toUsage(kind string) Usage {
	return Usage{Kind: kind, Input: nonNeg(w.Input), Output: nonNeg(w.Output), CacheRead: nonNeg(w.CacheRead), CacheWrite: nonNeg(w.CacheWrite), Reasoning: w.Reasoning}
}

func nonNeg(n int64) int64 { return max(n, 0) }

// usageMessage holds the usage-related message fields of message_end.
type usageMessage struct {
	Role          string     `json:"role"`
	Provider      string     `json:"provider"`
	Model         string     `json:"model"`
	ResponseModel string     `json:"responseModel"`
	StopReason    string     `json:"stopReason"`
	DurationMs    *int64     `json:"durationMs"`
	Usage         *wireUsage `json:"usage"`
}

// observeMessageEnd takes the usage of an assistant message, or of a tool result message that carries usage, from
// message_end.
func (c *Client) observeMessageEnd(line []byte) {
	o, inTurn := c.observe()
	if o == nil {
		return
	}
	var ev struct {
		Message *usageMessage `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Message == nil || ev.Message.Usage == nil {
		return
	}
	m := ev.Message
	switch m.Role {
	case "assistant":
		u := m.Usage.toUsage(UsageAssistant)
		u.Provider, u.Model, u.ResponseModel, u.StopReason, u.DurationMs = m.Provider, m.Model, m.ResponseModel, m.StopReason, m.DurationMs
		o.Usage(u, inTurn)
	case "toolResult":
		u := m.Usage.toUsage(UsageTool)
		u.DurationMs = m.DurationMs
		o.Usage(u, inTurn)
	}
}

// observeLine handles events that matter for run records but do not affect a turn's result.
func (c *Client) observeLine(typ string, line []byte) {
	switch typ {
	case "compaction_start":
		c.mu.Lock()
		c.compactionStart = time.Now()
		c.mu.Unlock()
	case "compaction_end":
		c.observeCompaction(line)
	case "auto_retry_start":
		var ev struct {
			Attempt      int    `json:"attempt"`
			MaxAttempts  int    `json:"maxAttempts"`
			DelayMs      int64  `json:"delayMs"`
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return
		}
		if o, inTurn := c.observe(); o != nil {
			o.Notice(Notice{Type: NoticeRetryStarted, Data: map[string]any{
				"attempt": ev.Attempt, "maxAttempts": ev.MaxAttempts, "delayMs": ev.DelayMs, "errorMessage": ev.ErrorMessage}}, inTurn)
		}
	case "auto_retry_end":
		var ev struct {
			Success    bool   `json:"success"`
			Attempt    int    `json:"attempt"`
			FinalError string `json:"finalError"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return
		}
		if o, inTurn := c.observe(); o != nil {
			data := map[string]any{"success": ev.Success, "attempt": ev.Attempt}
			if ev.FinalError != "" {
				data["finalError"] = ev.FinalError
			}
			o.Notice(Notice{Type: NoticeRetryEnded, Data: data}, inTurn)
		}
	case "entry_appended":
		var ev struct {
			Entry *struct {
				Type     string     `json:"type"`
				Kind     string     `json:"kind"`
				Provider string     `json:"provider"`
				Model    string     `json:"model"`
				Usage    *wireUsage `json:"usage"`
			} `json:"entry"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Entry == nil || ev.Entry.Type != "usage" || ev.Entry.Usage == nil {
			return
		}
		kind := UsageOther
		if ev.Entry.Kind == "cache_warm" {
			kind = UsageCacheWarm
		}
		if o, inTurn := c.observe(); o != nil {
			u := ev.Entry.Usage.toUsage(kind)
			u.Provider, u.Model = ev.Entry.Provider, ev.Entry.Model
			o.Usage(u, inTurn)
		}
	}
}

func (c *Client) observeCompaction(line []byte) {
	var ev struct {
		Reason string `json:"reason"`
		Result *struct {
			TokensBefore         *int64     `json:"tokensBefore"`
			EstimatedTokensAfter *int64     `json:"estimatedTokensAfter"`
			Usage                *wireUsage `json:"usage"`
		} `json:"result"`
		Aborted      bool   `json:"aborted"`
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	c.mu.Lock()
	started := c.compactionStart
	c.compactionStart = time.Time{}
	c.mu.Unlock()
	o, inTurn := c.observe()
	if o == nil {
		return
	}
	data := map[string]any{"reason": ev.Reason}
	switch {
	case ev.Result != nil:
		data["result"] = "succeeded"
		if ev.Result.TokensBefore != nil {
			data["tokensBefore"] = *ev.Result.TokensBefore
		}
		if ev.Result.EstimatedTokensAfter != nil {
			data["estimatedTokensAfter"] = *ev.Result.EstimatedTokensAfter
		}
	case ev.Aborted:
		data["result"] = "aborted"
	default:
		data["result"] = "failed"
		data["errorMessage"] = ev.ErrorMessage
	}
	if !started.IsZero() {
		data["durationMs"] = time.Since(started).Milliseconds()
	}
	o.Notice(Notice{Type: NoticeCompaction, Data: data}, inTurn)
	if ev.Result != nil && ev.Result.Usage != nil {
		o.Usage(ev.Result.Usage.toUsage(UsageCompaction), inTurn)
	}
}

// dialogMethods are the extension dialogs that require an answer. confirm, select and input are handed to Dialogs
// (console questions) when they occur during a turn and Dialogs is set; the rest (editor, outside a turn, no Dialogs)
// are answered as cancelled right away: the extension gets the same value as when the request's own timeout expires
// (undefined for select, input and editor; false for confirm), so the turn does not get stuck.
var dialogMethods = map[string]bool{"select": true, "confirm": true, "input": true, "editor": true}

// DialogRequest is one dialog request from an extension (see runturn.DialogRequest).
type DialogRequest = runturn.DialogRequest

// DialogResponse is the extension_ui_response sent back to Pi (see runturn.DialogResponse).
type DialogResponse = runturn.DialogResponse

// Dialogs handles extension dialogs that need a person to answer; it blocks until there is an answer, the dialog
// expires or ctx ends (ctx ends when the Pi process exits).
type Dialogs func(ctx context.Context, req DialogRequest) DialogResponse

// SetDialogs sets the dialog handler; when nil, every dialog is answered as cancelled right away.
func (c *Client) SetDialogs(d Dialogs) {
	c.mu.Lock()
	c.dialogs = d
	c.mu.Unlock()
}

// extensionUI handles an extension's ctx.ui requests (Pi RPC's extension_ui_request): for dialogs see dialogMethods;
// notify and setStatus are passed to the observer and recorded as run events; setWidget, setTitle and set_editor_text
// are ignored.
func (c *Client) extensionUI(line []byte) {
	var req struct {
		ID          string   `json:"id"`
		Method      string   `json:"method"`
		Title       string   `json:"title"`
		Message     string   `json:"message"`
		Placeholder string   `json:"placeholder"`
		Options     []string `json:"options"`
		Timeout     int64    `json:"timeout"`
		NotifyType  string   `json:"notifyType"`
		StatusKey   string   `json:"statusKey"`
		StatusText  *string  `json:"statusText"`
	}
	if json.Unmarshal(line, &req) != nil || req.ID == "" {
		c.malformed.Add(1)
		return
	}
	var data map[string]any
	switch {
	case dialogMethods[req.Method]:
		c.mu.Lock()
		dialogs, inTurn := c.dialogs, c.turn != nil && c.turn.active()
		c.mu.Unlock()
		if dialogs != nil && inTurn && req.Method != "editor" {
			d := DialogRequest{ID: req.ID, Method: req.Method, Title: req.Title, Message: req.Message, Placeholder: req.Placeholder,
				Options: req.Options, Timeout: time.Duration(req.Timeout) * time.Millisecond}
			go c.askDialog(dialogs, d)
			return
		}
		// Do not write stdin from the reader goroutine: if Pi is blocked writing stdout at the same time, each would
		// wait on the other.
		go c.answerDialog(req.ID, req.Method, DialogResponse{Cancelled: true})
		data = map[string]any{"method": req.Method, "message": req.Title, "answered": "cancelled"}
	case req.Method == "notify":
		level := req.NotifyType
		if level != "warning" && level != "error" {
			level = "info"
		}
		data = map[string]any{"method": req.Method, "message": req.Message, "level": level}
		// Extension command results are mostly given via notify: collect them with this turn and hand them to the
		// caller when Pi handles the command itself (run events are cleaned up over time).
		c.mu.Lock()
		t := c.turn
		c.mu.Unlock()
		if t != nil {
			t.addNotice(level, req.Message)
		}
	case req.Method == "setStatus":
		data = map[string]any{"method": req.Method, "statusKey": req.StatusKey, "message": req.StatusText}
	default:
		return
	}
	if o, inTurn := c.observe(); o != nil {
		o.Notice(Notice{Type: NoticeExtensionUI, Data: data}, inTurn)
	}
}

// askDialog waits for the dialog handler to answer, then replies to Pi and records the result as a run event (the turn
// is usually still in progress at that point).
func (c *Client) askDialog(dialogs Dialogs, d DialogRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	res := dialogs(ctx, d)
	c.answerDialog(d.ID, d.Method, res)
	if res.Outcome == "" {
		res.Outcome = "cancelled"
	}
	data := map[string]any{"method": d.Method, "message": d.Title, "answered": res.Outcome}
	if res.QuestionID != nil {
		data["questionId"] = *res.QuestionID
	}
	if o, inTurn := c.observe(); o != nil {
		o.Notice(Notice{Type: NoticeExtensionUI, Data: data}, inTurn)
	}
}

// answerDialog writes extension_ui_response: cancelled for a cancel, confirmed for confirm, value for select and input.
func (c *Client) answerDialog(id, method string, res DialogResponse) {
	m := map[string]any{"type": "extension_ui_response", "id": id}
	switch {
	case res.Cancelled:
		m["cancelled"] = true
	case method == "confirm":
		m["confirmed"] = res.Confirmed
	default:
		m["value"] = res.Value
	}
	b, _ := json.Marshal(m)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, _ = c.stdin.Write(append(b, '\n'))
}
