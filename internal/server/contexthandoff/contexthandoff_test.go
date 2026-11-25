package contexthandoff_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/contexthandoff"
)

var at = time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)

func msg(role, text string) contexthandoff.Message {
	return contexthandoff.Message{ID: uuid.New(), Role: role, Origin: "console", Status: "completed", Text: text, CreatedAt: at}
}

// conversation is n turns of a request and a reply, each text carrying its index so tests can see what was kept.
func conversation(n, size int) []contexthandoff.Message {
	var out []contexthandoff.Message
	for i := range n {
		out = append(out, msg("user", fmt.Sprintf("request %d %s", i, strings.Repeat("u", size))))
		out = append(out, msg("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("a", size))))
	}
	return out
}

// kept returns the texts of the messages the block carries, in the order they appear.
func kept(t *testing.T, block contexthandoff.Block, all []contexthandoff.Message) []string {
	t.Helper()
	var out []string
	last := -1
	for _, m := range all {
		i := strings.Index(block.Text, m.ID.String())
		if i < 0 {
			continue
		}
		if i < last {
			t.Fatalf("messages out of order: %q", m.Text)
		}
		last = i
		if !strings.Contains(block.Text, m.Text) {
			t.Fatalf("a kept message must keep its full text: %q", m.Text)
		}
		out = append(out, strings.Fields(m.Text)[0]+" "+strings.Fields(m.Text)[1])
	}
	return out
}

// A conversation that fits the budget goes whole, in order, marked as a block that is context and not a request, and
// says how to read the saved conversation.
func TestBuildCarriesAShortConversationWhole(t *testing.T) {
	all := conversation(3, 10)
	block := contexthandoff.Build(all, "Pi", "Claude Code", contexthandoff.DefaultBudget)
	if got := kept(t, block, all); len(got) != 6 || block.Included != 6 || block.Omitted != 0 {
		t.Fatalf("everything fits: %v %+v", got, block)
	}
	for _, want := range []string{"<conversation-handoff>", "</conversation-handoff>", "from Pi to Claude Code", "not a new request", "loopctl conversation"} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("the block lacks %q:\n%s", want, block.Text)
		}
	}
	if strings.Count(block.Text, "</conversation-handoff>") != 1 || !strings.HasSuffix(block.Text, "</conversation-handoff>") {
		t.Fatalf("the closing tag appears once, at the end, so a reader can split at it:\n%s", block.Text)
	}
	if block.Bytes != len(block.Text) {
		t.Fatalf("bytes %d, text %d", block.Bytes, len(block.Text))
	}
	if empty := contexthandoff.Build(nil, "Pi", "Claude Code", contexthandoff.DefaultBudget); empty.Text != "" || empty.Bytes != 0 {
		t.Fatalf("nothing to carry gives no block: %+v", empty)
	}
}

// Over the budget, the first request and the most recent requests and answers are kept, in full and in order; the
// block stays within the budget, counts what was left out and marks where.
func TestBuildOverBudgetKeepsTheFirstRequestAndTheLatestTurns(t *testing.T) {
	all := conversation(40, 400)
	budget := 6000
	block := contexthandoff.Build(all, "Pi", "Claude Code", budget)
	if block.Bytes > budget || len(block.Text) > budget {
		t.Fatalf("the block exceeds the budget: %d > %d", block.Bytes, budget)
	}
	got := kept(t, block, all)
	if len(got) < 4 || got[0] != "request 0" || got[len(got)-1] != "answer 39" || got[len(got)-2] != "request 39" {
		t.Fatalf("the first request and the latest turns must be kept: %v", got)
	}
	if block.Included != len(got) || block.Omitted != len(all)-len(got) {
		t.Fatalf("counts: %+v for %d kept", block, len(got))
	}
	if !strings.Contains(block.Text, fmt.Sprintf("%d were left out", block.Omitted)) || !strings.Contains(block.Text, "left out here") {
		t.Fatalf("the block must say how many were left out and where:\n%s", block.Text)
	}
}

// One message larger than the budget is left out, not cut: the others still go, and nothing is shortened.
func TestBuildLeavesOutAMessageTooLargeToFit(t *testing.T) {
	all := conversation(3, 10)
	huge := msg("user", "request huge "+strings.Repeat("h", 5000))
	all = append(all, huge, msg("assistant", "answer huge done"))
	block := contexthandoff.Build(all, "Pi", "Claude Code", 2048)
	if strings.Contains(block.Text, huge.ID.String()) || strings.Contains(block.Text, "hhhhhhhhhh") {
		t.Fatal("a message too large for the budget must be left out whole")
	}
	got := kept(t, block, all)
	if len(got) == 0 || got[len(got)-1] != "answer huge" || block.Omitted < 1 || block.Bytes > 2048 {
		t.Fatalf("the rest still goes: %v %+v", got, block)
	}
}

// Failed and interrupted turns keep the text that was written, with the turn's status; tool use is summarized; images
// are counted, never carried; platform reminders and wake-ups are named as such.
func TestBuildDescribesTurns(t *testing.T) {
	failed := msg("assistant", "partial answer before the error")
	failed.Status = "failed"
	stopped := msg("assistant", "partial answer before the stop")
	stopped.Status = "interrupted"
	tools := msg("assistant", "answer with tools")
	tools.Tools = []contexthandoff.Tool{{Name: "bash", Calls: 3, Failed: 1}, {Name: "read", Calls: 1}}
	pictured := msg("user", "look at these")
	pictured.Images = 2
	reminder := msg("user", "Platform reminder: you have unread messages")
	reminder.Origin = "inbox_reminder"
	wake := msg("user", "Your sleep is over")
	wake.Origin = "sleep_wake"
	ended := msg("user", "Platform notice: background task \"make test\" (completed) ended")
	ended.Origin = "background"
	all := []contexthandoff.Message{pictured, failed, reminder, stopped, wake, ended, tools}
	block := contexthandoff.Build(all, "Claude Code", "Pi", contexthandoff.DefaultBudget)
	for _, want := range []string{"partial answer before the error", "failed", "interrupted", "bash ×3 (1 failed)", "read ×1",
		"2 images not carried", "platform reminder", "sleep wake-up", "user, background work ended"} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("the block lacks %q:\n%s", want, block.Text)
		}
	}
	if block.Images != 2 || !strings.Contains(block.Text, "Images (2)") {
		t.Fatalf("images are counted: %+v", block)
	}
}

// The budget is 16000 by default (LOOP_NOW_CONTEXT_HANDOFF_TOKENS in the server config changes it) and stays within
// 1024..64000.
func TestClampBudget(t *testing.T) {
	for value, want := range map[int]int{0: 16000, -5: 16000, 8000: 8000, 10: 1024, 1000000: 64000} {
		if got := contexthandoff.ClampBudget(value); got != want {
			t.Errorf("%d: got %d, want %d", value, got, want)
		}
	}
}

// When the conversation goes to a new session of the same runtime (the change was undone after the other runtime had
// already run a message), the block says so instead of naming a change from a runtime to itself.
func TestBuildIntoANewSessionOfTheSameRuntime(t *testing.T) {
	block := contexthandoff.Build(conversation(1, 10), "Pi", "Pi", contexthandoff.DefaultBudget)
	if strings.Contains(block.Text, "from Pi to Pi") || !strings.Contains(block.Text, "a new Pi session") {
		t.Fatalf("wrong introduction:\n%s", block.Text)
	}
}

// The block says why the conversation is carried: a lost session, or a task instance that cannot copy the session.
func TestBuildForReason(t *testing.T) {
	all := conversation(1, 10)
	if b := contexthandoff.BuildFor(contexthandoff.SessionLost, all, "Grok", "Grok", contexthandoff.DefaultBudget); !strings.Contains(b.Text, "earlier Grok session could not be continued") {
		t.Fatalf("lost session:\n%s", b.Text)
	}
	if b := contexthandoff.BuildFor(contexthandoff.TaskStart, all, "Grok", "Grok", contexthandoff.DefaultBudget); !strings.Contains(b.Text, "task instance") {
		t.Fatalf("task start:\n%s", b.Text)
	}
}
