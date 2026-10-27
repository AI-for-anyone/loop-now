package pirpc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Usage 是 Pi 报告的一次用量（Pi 1.1.0 pi-ai 的 Usage，见 dist/types.d.ts）。费用字段不解析。
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

// 用量类别。
const (
	UsageAssistant  = "assistant"
	UsageTool       = "tool"
	UsageCompaction = "compaction"
	UsageCacheWarm  = "cache_warm"
	UsageOther      = "other"
)

// Notice 是对运行记录有用的 Pi 事件：自动重试与上下文压缩。
type Notice struct {
	Type string // 见 Notice 常量
	Data map[string]any
}

// Notice 类型。
const (
	NoticeRetryStarted = "retry_started"
	NoticeRetryEnded   = "retry_ended"
	NoticeCompaction   = "compaction"
	// NoticeExtensionUI 是扩展的 ctx.ui 调用：通知、状态，以及平台已按默认值回应的对话。
	NoticeExtensionUI = "extension_ui"
)

// Observer 接收用量与事件，不论当时有没有进行中的一轮；inTurn 表示是否发生在某一轮 prompt 之中。
// 调用发生在读协程上，实现必须迅速返回，且不得在回调中同步调用 Client 的方法。
type Observer interface {
	Usage(u Usage, inTurn bool)
	Notice(n Notice, inTurn bool)
}

// SetObserver 设置观察者；之后收到的用量与事件交给它。
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// observe 返回当前观察者，以及此刻是否处于一轮之中。
func (c *Client) observe() (Observer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.turn
	return c.observer, t != nil && t.active()
}

// wireUsage 是 Pi Usage 的 JSON 形状。
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

// usageMessage 是 message_end 中与用量有关的消息字段。
type usageMessage struct {
	Role          string     `json:"role"`
	Provider      string     `json:"provider"`
	Model         string     `json:"model"`
	ResponseModel string     `json:"responseModel"`
	StopReason    string     `json:"stopReason"`
	DurationMs    *int64     `json:"durationMs"`
	Usage         *wireUsage `json:"usage"`
}

// observeMessageEnd 从 message_end 中取出助手消息或带用量的工具结果消息的用量。
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

// observeLine 处理与运行记录有关、但不影响一轮结果的事件。
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

// dialogMethods 是要求回应的扩展对话。confirm、select、input 在一轮之中且设置了 Dialogs 时交给它（控制台提问），
// 其余（editor、一轮之外、没有 Dialogs）立即按取消回应：扩展得到的值与请求自带超时到期时的默认值相同
// （select、input、editor 为 undefined，confirm 为 false），这一轮不会被卡住。
var dialogMethods = map[string]bool{"select": true, "confirm": true, "input": true, "editor": true}

// DialogRequest 是扩展的一次对话请求。
type DialogRequest struct {
	ID          string
	Method      string // confirm、select、input
	Title       string
	Message     string
	Placeholder string
	Options     []string
	Timeout     time.Duration // 请求自带的超时；没有时为零
}

// DialogResponse 是回给 Pi 的 extension_ui_response：Cancelled，或 confirm 的 Confirmed，或 select、input 的 Value。
// Outcome 记入运行事件的 answered（answered、cancelled、expired），QuestionID 是对应的控制台提问。
type DialogResponse struct {
	Cancelled  bool
	Confirmed  bool
	Value      string
	Outcome    string
	QuestionID *uuid.UUID
}

// Dialogs 处理需要人回答的扩展对话，阻塞到有回答、到期或 ctx 结束（Pi 进程退出时 ctx 结束）。
type Dialogs func(ctx context.Context, req DialogRequest) DialogResponse

// SetDialogs 设置对话的处理者；为空时对话一律立即按取消回应。
func (c *Client) SetDialogs(d Dialogs) {
	c.mu.Lock()
	c.dialogs = d
	c.mu.Unlock()
}

// extensionUI 处理扩展的 ctx.ui 请求（Pi RPC 的 extension_ui_request）：对话见 dialogMethods；
// notify 与 setStatus 交给观察者记为运行事件；setWidget、setTitle、set_editor_text 忽略。
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
		// 不在读协程里写标准输入：Pi 同时阻塞在写标准输出时会互相等待。
		go c.answerDialog(req.ID, req.Method, DialogResponse{Cancelled: true})
		data = map[string]any{"method": req.Method, "message": req.Title, "answered": "cancelled"}
	case req.Method == "notify":
		level := req.NotifyType
		if level != "warning" && level != "error" {
			level = "info"
		}
		data = map[string]any{"method": req.Method, "message": req.Message, "level": level}
	case req.Method == "setStatus":
		data = map[string]any{"method": req.Method, "statusKey": req.StatusKey, "message": req.StatusText}
	default:
		return
	}
	if o, inTurn := c.observe(); o != nil {
		o.Notice(Notice{Type: NoticeExtensionUI, Data: data}, inTurn)
	}
}

// askDialog 等对话的处理者回答后回应 Pi，并把结果记为运行事件（这时这一轮通常仍在进行）。
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

// answerDialog 写 extension_ui_response：取消为 cancelled，confirm 为 confirmed，select 与 input 为 value。
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
