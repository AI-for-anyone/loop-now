package inbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

// Pi 扩展的对话（plugins/SPEC.md §11）：Agent 半边的扩展经 ctx.ui 发来的 confirm、select、input 变成 Agent 发给控制台的提问，
// 出现在这个 Agent 的控制台对话里；控制台的回答交还给 Pi，请求的超时到期、这一轮结束时按取消回应。

// DefaultDialogTimeout 是请求没有带超时时提问的期限。
const DefaultDialogTimeout = 30 * time.Minute

// dialogPoll 是等待回答时检查提问状态的间隔。
var dialogPoll = 500 * time.Millisecond

// Dialog 是一次扩展对话请求。AgentID 是执行的 Agent（任务轮次时为任务实例 ID）。
type Dialog struct {
	AgentID, RunID uuid.UUID
	RequestID      string
	Method         string // confirm、select、input
	Title          string
	Message        string
	Placeholder    string
	Options        []string
	Timeout        time.Duration
}

// DialogAnswer 是交还给 Pi 的回应：Cancelled，或 confirm 的 Confirmed，或 select、input 的 Value。
// Outcome 记入运行事件：answered（有人回答）、cancelled（回答为取消或无法提问）、expired（到期或这一轮结束）。
type DialogAnswer struct {
	Cancelled  bool
	Confirmed  bool
	Value      string
	Outcome    string
	QuestionID *uuid.UUID
}

func cancelled(outcome string, question *uuid.UUID) DialogAnswer {
	return DialogAnswer{Cancelled: true, Outcome: outcome, QuestionID: question}
}

// AskDialog 把对话作为提问发到控制台，等到有回答、到期或 ctx 结束。只有常态 Agent 的执行会提问；
// 任务轮次、选项超过 10 个或写入失败时直接按取消回应。ctx 结束时提问到期（原因为 run_ended）。
func (s *Service) AskDialog(ctx context.Context, d Dialog) (DialogAnswer, error) {
	if _, err := s.St.GetAddressableAgent(ctx, d.AgentID); err != nil {
		if store.IsNotFound(err) {
			return cancelled("cancelled", nil), nil // 任务实例：任务轮次的对话不提问
		}
		return cancelled("cancelled", nil), err
	}
	text, options, ok := dialogQuestion(d)
	if !ok {
		return cancelled("cancelled", nil), nil
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultDialogTimeout
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: d.AgentID}
	msg := store.NewInboxMessage{ID: uuid.Must(uuid.NewV7()), From: store.AgentParty(d.AgentID), To: store.ConsoleParty, Text: text}
	q, err := s.questionDeliver(ctx, p, msg, QuestionInput{Options: options, ExpiresIn: timeout,
		Dialog: &store.ExtensionDialog{RunID: d.RunID, RequestID: d.RequestID, Method: d.Method}})
	if err != nil {
		return cancelled("cancelled", nil), err
	}
	id := q.ID
	tick := time.NewTicker(dialogPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.endDialog(id)
			return cancelled("expired", &id), nil
		case <-tick.C:
		}
		st, err := s.St.GetDialogState(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return cancelled("cancelled", &id), err
		}
		switch {
		case st.State == "answered":
			return dialogAnswer(d, st, id), nil
		case st.State != "open":
			return cancelled("expired", &id), nil
		case !st.ExpiresAt.After(time.Now()):
			if err := s.ReconcileQuestions(context.WithoutCancel(ctx)); err != nil {
				s.Log.Warn("扩展对话提问到期处理失败", "question", id, "err", err)
			}
			return cancelled("expired", &id), nil
		}
	}
}

// endDialog 让仍未回答的提问到期，并把变化推给控制台。
func (s *Service) endDialog(id uuid.UUID) {
	ctx := context.Background()
	changed, err := s.St.EndExtensionDialog(ctx, id)
	if err == nil && changed {
		err = s.ReconcileQuestions(ctx)
	}
	if err != nil {
		s.Log.Warn("结束扩展对话提问失败", "question", id, "err", err)
	}
}

// dialogQuestion 返回提问的正文与选项：confirm 为“是／否”，select 为请求的选项（每项至多 50 字，至多 10 项），
// input 为“取消”，直接回复文字即为输入的内容。
func dialogQuestion(d Dialog) (string, []string, bool) {
	var lines []string
	for _, l := range []string{d.Title, d.Message} {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "扩展请求"+map[string]string{"confirm": "确认", "select": "选择", "input": "输入"}[d.Method])
	}
	switch d.Method {
	case "confirm":
		return strings.Join(lines, "\n"), []string{"是", "否"}, true
	case "select":
		if len(d.Options) == 0 || len(d.Options) > 10 {
			return "", nil, false
		}
		options := make([]string, len(d.Options))
		seen := map[string]bool{}
		for i, o := range d.Options {
			label := strings.TrimSpace(o)
			if label == "" {
				label = "（空）"
			}
			if utf8.RuneCountInString(label) > 50 {
				label = string([]rune(label)[:49]) + "…"
			}
			for n := 2; seen[label]; n++ {
				suffix := fmt.Sprintf("（%d）", n)
				base := []rune(label)
				if len(base)+utf8.RuneCountInString(suffix) > 50 {
					base = base[:50-utf8.RuneCountInString(suffix)]
				}
				label = string(base) + suffix
			}
			seen[label] = true
			options[i] = label
		}
		return strings.Join(lines, "\n"), options, true
	case "input":
		hint := "直接回复文字作答"
		if strings.TrimSpace(d.Placeholder) != "" {
			hint += "（" + d.Placeholder + "）"
		}
		return strings.Join(append(lines, hint), "\n"), []string{"取消"}, true
	}
	return "", nil, false
}

// dialogAnswer 把控制台的回答换成 Pi 的回应：confirm 选“是／否”，select 选一项（回 Pi 原来的选项字符串），
// input 回复文字；不合这些的（例如 confirm 回了文字、input 选了“取消”）按取消回应。
func dialogAnswer(d Dialog, st store.DialogState, id uuid.UUID) DialogAnswer {
	out := DialogAnswer{Outcome: "answered", QuestionID: &id}
	option := 0
	if st.OptionID != nil {
		option, _ = strconv.Atoi(*st.OptionID)
	}
	switch {
	case d.Method == "confirm" && option > 0:
		out.Confirmed = option == 1
		return out
	case d.Method == "select" && option > 0 && option <= len(d.Options):
		out.Value = d.Options[option-1]
		return out
	case d.Method == "input" && option == 0:
		out.Value = st.Text
		return out
	}
	return cancelled("cancelled", &id)
}
