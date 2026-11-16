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

// Pi extension dialogs (plugins/SPEC.md §11): confirm, select and input sent by an agent-side extension through ctx.ui
// become questions from the Agent to the console and appear in this Agent's console conversation; the console's answer
// is handed back to Pi, and when the request's timeout expires or the turn ends, the reply is a cancel.

// DefaultDialogTimeout is the question's deadline when the request carries no timeout.
const DefaultDialogTimeout = 30 * time.Minute

// dialogPoll is how often the question state is checked while waiting for an answer.
var dialogPoll = 500 * time.Millisecond

// Dialog is one extension dialog request. AgentID is the Agent of the run (the task instance ID for a task turn).
type Dialog struct {
	AgentID, RunID uuid.UUID
	RequestID      string
	Method         string // confirm, select, input
	Title          string
	Message        string
	Placeholder    string
	Options        []string
	Timeout        time.Duration
}

// DialogAnswer is the response handed back to Pi: Cancelled, or Confirmed for confirm, or Value for select and input.
// Outcome is recorded in the run events: answered (someone answered), cancelled (the answer was a cancel, or the
// question could not be asked), expired (deadline reached or the turn ended).
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

// AskDialog sends the dialog to the console as a question and waits for an answer, the deadline or the end of ctx. Only
// runs of regular Agents ask questions; task turns, more than 10 options, or a failed write get a cancel response right
// away. When ctx ends the question expires (reason run_ended).
func (s *Service) AskDialog(ctx context.Context, d Dialog) (DialogAnswer, error) {
	if _, err := s.St.GetAddressableAgent(ctx, d.AgentID); err != nil {
		if store.IsNotFound(err) {
			return cancelled("cancelled", nil), nil // task instance: dialogs of task turns do not ask questions
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

// endDialog expires a question that is still unanswered and pushes the change to the console.
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

// dialogQuestion returns the question's body and options: for confirm “是／否” ("Yes/No"), for select the requested
// options (at most 50 characters each, at most 10 options), for input “取消” ("Cancel"), where replying with text
// directly is the input.
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

// dialogAnswer turns the console's answer into Pi's response: confirm picks “是／否” ("Yes/No"), select picks one option
// (Pi gets back its original option string), input replies with text; anything that does not fit (e.g. text for
// confirm, or “取消” ("Cancel") picked for input) is answered as a cancel.
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
