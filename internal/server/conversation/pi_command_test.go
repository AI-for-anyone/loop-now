package conversation_test

import (
	"testing"

	"loop-now/internal/server/conversation"
)

func TestPiCommandParse(t *testing.T) {
	for _, c := range []struct{ in, name, msg string }{
		{"/pi:grok-cli-accounts gui", "grok-cli-accounts", "/grok-cli-accounts gui"},
		{"/pi:skill:review take a look\nsecond line", "skill:review", "/skill:review take a look\nsecond line"},
		{"/pi:usage", "usage", "/usage"},
		{"/pi:", "", ""},
		{"/pi: x", "", ""},
		{"/usage", "", ""},
		{" /pi:usage", "", ""},
		{"Hello /pi:usage", "", ""},
	} {
		name, msg := conversation.PiCommand(c.in)
		if name != c.name || msg != c.msg {
			t.Errorf("%q → %q %q, want %q %q", c.in, name, msg, c.name, c.msg)
		}
	}
}

// /pi:<name> on the conversation page: the original text is saved and displayed, and /<name> is sent to Pi (without the
// run ID); when Pi handles it directly the turn is completed, has no reply, and has piCommand.handled; a /xxx without
// pi: still gets the run ID and goes to the Agent as before.
func TestPiCommandRuns(t *testing.T) {
	e := newEnv(t)
	e.mustSend("/pi:handled-cmd gui", "")
	e.waitStarted("/handled-cmd gui")
	e.waitIdle()
	e.mustSend("/pi:skill:review take a look", "")
	e.waitStarted("/skill:review take a look")
	e.waitIdle()
	e.mustSend("/usage", "")
	e.waitStarted("/usage")
	e.waitIdle()
	h := e.history()
	if len(h) != 6 {
		t.Fatalf("history: %+v", h)
	}
	if h[0].Text != "/pi:handled-cmd gui" || h[1].Text != "" || h[1].Run.Status != "completed" || h[1].Run.PiCommand == nil || h[1].Run.PiCommand.Name != "handled-cmd" || !h[1].Run.PiCommand.Handled {
		t.Fatalf("extension command: %+v %+v", h[0], h[1].Run)
	}
	if n := h[1].Run.PiCommand.Notices; len(n) != 2 || n[1].Level != "warning" || n[1].Message != "Weekly Limit\n   Used       1%" || !h[1].Run.PiCommand.Truncated {
		t.Fatalf("command output should be saved with the run: %+v", h[1].Run.PiCommand)
	}
	if h[3].Run.PiCommand != nil && (len(h[3].Run.PiCommand.Notices) != 0 || h[3].Run.PiCommand.Truncated) {
		t.Fatalf("a command that was not handled directly should get no notices: %+v", h[3].Run.PiCommand)
	}
	if h[2].Text != "/pi:skill:review take a look" || h[3].Run.PiCommand == nil || h[3].Run.PiCommand.Name != "skill:review" || h[3].Run.PiCommand.Handled || h[3].Text != "Reply: /skill:review take a look" {
		t.Fatalf("skill command: %+v %+v", h[2], h[3].Run)
	}
	if h[5].Run.PiCommand != nil {
		t.Fatalf("a command without pi: is not a Pi command: %+v", h[5].Run)
	}
}
