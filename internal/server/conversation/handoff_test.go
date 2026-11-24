package conversation_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

func (e *env) switchModel(provider, model string) {
	e.t.Helper()
	if _, err := e.st.UpdateAgent(context.Background(), e.agent, store.AgentPatch{ModelProvider: &provider, ModelID: &model}); err != nil {
		e.t.Fatalf("switch to %s: %v", provider, err)
	}
}

// turnTo sends a message, waits for its turn to end and returns the prompt the runtime was given (without the run ID
// line).
func (e *env) turnTo(text string) string {
	e.t.Helper()
	e.mustSend(text, "")
	select {
	case got := <-e.ex.started:
		e.waitIdle()
		return got
	case <-time.After(5 * time.Second):
		e.t.Fatalf("timed out waiting for %q to start", text)
	}
	return ""
}

func lastRun(t *testing.T, h []apitypes.Message) apitypes.Run {
	t.Helper()
	if len(h) == 0 {
		t.Fatal("empty history")
	}
	return h[len(h)-1].Run
}

// After a change of runtime, the first prompt of the new runtime's session carries the saved conversation in a marked
// block before the user's message; the stored message is the user's own text, the turn records the handoff and who ran
// it, and the next turn has no block.
func TestRuntimeChangeCarriesTheConversation(t *testing.T) {
	e := newEnv(t)
	e.ex.sessions = e.st
	if got := e.turnTo("remember the word apple"); strings.Contains(got, "<conversation-handoff>") {
		t.Fatalf("the first turn has nothing to carry: %q", got)
	}
	e.switchModel("claude-code", "sonnet")
	got := e.turnTo("what was the word?")
	block, request, ok := strings.Cut(got, "</conversation-handoff>")
	if !ok || !strings.HasPrefix(block, "<conversation-handoff>") || !strings.Contains(block, "remember the word apple") ||
		!strings.Contains(block, "Reply: remember the word apple") || !strings.Contains(block, "from Pi to Claude Code") {
		t.Fatalf("the first prompt after the change must start with the conversation:\n%q", got)
	}
	if strings.TrimLeft(request, "\n") != "what was the word?" {
		t.Fatalf("the request follows the block unchanged: %q", request)
	}
	h := e.history()
	if h[2].Role != "user" || h[2].Text != "what was the word?" {
		t.Fatalf("the stored message is the user's own text: %+v", h[2])
	}
	run := lastRun(t, h)
	if c := run.ContextHandoff; c == nil || c.FromRuntime != "pi" || c.ToRuntime != "claude-code" || c.IncludedMessages != 2 || c.OmittedMessages != 0 || c.Bytes == 0 || c.BudgetBytes != 16000 {
		t.Fatalf("the turn records the handoff: %+v", run.ContextHandoff)
	}
	if s := run.Selection; s == nil || s.Runtime != "claude-code" || s.Provider != "claude-code" || s.ModelID != "sonnet" {
		t.Fatalf("the turn records who ran it: %+v", run.Selection)
	}
	if first := h[1].Run; first.ContextHandoff != nil || first.Selection == nil || first.Selection.Runtime != "pi" {
		t.Fatalf("the earlier turn ran on Pi and carried nothing: %+v %+v", first.Selection, first.ContextHandoff)
	}
	if again := e.turnTo("and now?"); strings.Contains(again, "<conversation-handoff>") {
		t.Fatalf("once delivered, the handoff is not sent again: %q", again)
	}
}

// /new starts clean: no handoff even right after a change of runtime. A change of model inside Pi keeps Pi's session,
// so it carries nothing either.
func TestNoHandoffAfterNewOrWithinPi(t *testing.T) {
	e := newEnv(t)
	e.ex.sessions = e.st
	e.turnTo("hello")
	e.switchModel("openai", "gpt-x")
	if got := e.turnTo("same runtime"); strings.Contains(got, "<conversation-handoff>") {
		t.Fatalf("a change of model within Pi carries nothing: %q", got)
	}
	e.switchModel("claude-code", "sonnet")
	if _, err := e.st.ResetSession(context.Background(), e.agent); err != nil {
		t.Fatal(err)
	}
	if got := e.turnTo("fresh start"); strings.Contains(got, "<conversation-handoff>") {
		t.Fatalf("/new starts clean: %q", got)
	}
}

// A change of runtime while a reply is in progress applies from the next message, which carries the conversation
// including that reply.
func TestRuntimeChangeWhileReplying(t *testing.T) {
	e := newEnv(t)
	e.ex.sessions = e.st
	e.mustSend("block until released", "")
	e.waitStarted("block until released")
	e.switchModel("claude-code", "sonnet")
	if !e.ex.signal(e.agent, "release") {
		t.Fatal("the reply in progress did not take the release")
	}
	e.waitIdle()
	got := e.turnTo("next")
	if !strings.Contains(got, "Reply: block until released") || !strings.HasSuffix(got, "next") {
		t.Fatalf("the next message carries the reply that was in progress:\n%s", got)
	}
	h := e.history()
	if first := h[1].Run; first.Selection == nil || first.Selection.Runtime != "pi" {
		t.Fatalf("the reply in progress finished on Pi: %+v", first.Selection)
	}
	if run := lastRun(t, h); run.ContextHandoff == nil || run.Selection == nil || run.Selection.Runtime != "claude-code" {
		t.Fatalf("the next turn ran on Claude Code with the handoff: %+v %+v", run.Selection, run.ContextHandoff)
	}
}

// A session the runtime can no longer continue does not stop the conversation: the message starts a new session of
// the same runtime with the saved conversation, and the turn records that handoff (reason session_lost) so the console
// can show it.
func TestLostSessionContinuesWithTheConversation(t *testing.T) {
	e := newEnv(t)
	e.ex.sessions = e.st
	e.turnTo("remember the word apple")
	if err := e.st.RecordRuntimeSessionFile(context.Background(), e.agent, "pi", "/gone/session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.mustSend("what was the word?", "")
	var got string
	for got == "" || !strings.Contains(got, "<conversation-handoff>") {
		select {
		case got = <-e.ex.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("the message was not sent again with the conversation; last prompt %q", got)
		}
	}
	e.waitIdle()
	if !strings.Contains(got, "remember the word apple") || !strings.Contains(got, "could not be continued") || !strings.HasSuffix(got, "what was the word?") {
		t.Fatalf("the new session gets the conversation:\n%s", got)
	}
	run := lastRun(t, e.history())
	if run.Status != "completed" || run.ContextHandoff == nil || run.ContextHandoff.Reason != "session_lost" || run.ContextHandoff.FromRuntime != "pi" || run.ContextHandoff.ToRuntime != "pi" {
		t.Fatalf("the turn completes and records the handoff: %s %+v", run.Status, run.ContextHandoff)
	}
	sess, err := e.st.GetAgentSession(context.Background(), e.agent)
	if err != nil || sess.State != "active" || sess.Handoff != nil || sess.Epoch != 1 {
		t.Fatalf("the conversation goes on in the new session: %+v %v", sess, err)
	}
}

// Known window: a change of runtime that lands after a message was dispatched (no handoff was pending yet) but before
// the executor read the Agent's configuration sends that message to the new runtime without the conversation. The
// turn counts as having run there, so changing back no longer continues the earlier session, and the next message
// carries the conversation, including that turn.
func TestRuntimeChangeBetweenDispatchAndStart(t *testing.T) {
	e := newEnv(t)
	e.ex.sessions = e.st
	e.turnTo("remember the word apple")
	var once sync.Once
	e.ex.beforeSnapshot = func(executor.Request) { once.Do(func() { e.switchModel("claude-code", "sonnet") }) }
	if got := e.turnTo("racing message"); strings.Contains(got, "<conversation-handoff>") || got != "racing message" {
		t.Fatalf("the message dispatched before the change goes without the conversation: %q", got)
	}
	h := e.history()
	if run := lastRun(t, h); run.Selection == nil || run.Selection.Runtime != "claude-code" || run.ContextHandoff != nil {
		t.Fatalf("it ran on Claude Code and carried nothing: %+v %+v", run.Selection, run.ContextHandoff)
	}
	if sess, err := e.st.GetAgentSession(context.Background(), e.agent); err != nil || sess.Handoff == nil || !sess.Handoff.Ran || sess.Handoff.From != "pi" {
		t.Fatalf("the handoff stays pending and counts the turn as run: %+v %v", sess.Handoff, err)
	}
	got := e.turnTo("what was the word?")
	if !strings.Contains(got, "remember the word apple") || !strings.Contains(got, "Reply: racing message") || !strings.HasSuffix(got, "what was the word?") {
		t.Fatalf("the next message carries the conversation, including the racing turn:\n%s", got)
	}
}
