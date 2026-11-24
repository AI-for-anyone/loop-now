package store_test

import (
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

func (e *recEnv) switchTo(provider, model string) {
	e.t.Helper()
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: &provider, ModelID: &model}); err != nil {
		e.t.Fatalf("switch to %s: %v", provider, err)
	}
}

func (e *recEnv) session() store.AgentSession {
	e.t.Helper()
	sess, err := e.s.GetAgentSession(e.ctx, e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	return sess
}

func (e *recEnv) agentEvents(typ string) []store.LifecycleEvent {
	e.t.Helper()
	evs, err := e.s.AgentEvents(e.ctx, e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.LifecycleEvent
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// A change of runtime no longer starts over: it is accepted while a reply is in progress, keeps the session number,
// and leaves a pending handoff from the runtime that ran the conversation, holding that runtime's session so the
// change can be undone. A change of model inside one runtime leaves the session alone.
func TestRuntimeChangeLeavesAPendingHandoff(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("openai", "gpt-x")
	if sess := e.session(); sess.State != "active" || sess.Epoch != 1 || sess.SessionFile != "/tmp/pi-session.jsonl" || sess.Handoff != nil {
		t.Fatalf("a change of model within Pi keeps its session: %+v", sess)
	}
	busy := e.send(true, "")
	e.switchTo("claude-code", "sonnet")
	sess := e.session()
	if sess.State != "none" || sess.SessionFile != "" || sess.Epoch != 1 {
		t.Fatalf("the new runtime starts its own session, in the same conversation: %+v", sess)
	}
	if h := sess.Handoff; h == nil || h.ID == uuid.Nil || h.From != "pi" || h.SessionFile != "/tmp/pi-session.jsonl" || h.Dispatched {
		t.Fatalf("wrong pending handoff: %+v", sess.Handoff)
	}
	if active, err := e.s.ActiveRun(e.ctx, e.agent); err != nil || active == nil || active.ID != busy.ID {
		t.Fatalf("the reply in progress goes on: %+v %v", active, err)
	}
	if resets := e.agentEvents(store.EventSessionReset); len(resets) != 0 {
		t.Fatalf("a change of runtime is not a session reset: %+v", resets)
	}
	changes := e.agentEvents(store.EventRuntimeChanged)
	if len(changes) != 1 || changes[0].Data["fromRuntime"] != "pi" || changes[0].Data["toRuntime"] != "claude-code" || changes[0].Data["handoff"] != "pending" {
		t.Fatalf("wrong runtime change event: %+v", changes)
	}
	e.switchTo("claude-code", "opus")
	if again := e.session(); again.Handoff == nil || *again.Handoff != *sess.Handoff {
		t.Fatalf("a change of model on the new runtime keeps the pending handoff: %+v", again.Handoff)
	}
}

// Changing back before the new runtime ran anything continues the earlier session; once a message was dispatched to
// the new runtime, changing back starts a new session with a handoff from the runtime that ran it.
func TestRuntimeChangeUndone(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	e.switchTo("anthropic", "claude-x")
	if sess := e.session(); sess.State != "active" || sess.SessionFile != "/tmp/pi-session.jsonl" || sess.Handoff != nil {
		t.Fatalf("an undone change continues the earlier session: %+v", sess)
	}
	if changes := e.agentEvents(store.EventRuntimeChanged); len(changes) != 2 || changes[1].Data["handoff"] != "restored" {
		t.Fatalf("wrong runtime change events: %+v", changes)
	}

	e.switchTo("claude-code", "sonnet")
	pending, err := e.s.DispatchHandoff(e.ctx, e.agent)
	if err != nil || pending == nil || pending.From != "pi" || !pending.Dispatched {
		t.Fatalf("dispatching a message to the new runtime takes the pending handoff: %+v %v", pending, err)
	}
	e.switchTo("anthropic", "claude-x")
	sess := e.session()
	if sess.State != "none" || sess.Handoff == nil || sess.Handoff.From != "claude-code" || sess.Handoff.SessionFile != "" || sess.Handoff.ID == pending.ID {
		t.Fatalf("after the new runtime ran a message, changing back hands the conversation over again: %+v %+v", sess, sess.Handoff)
	}

	// Without an earlier session to continue (the first turn never saved one), the conversation is still handed over.
	f := newRecEnv(t)
	f.turn("hello", "failed", "")
	f.switchTo("claude-code", "sonnet")
	f.switchTo("anthropic", "claude-x")
	if sess := f.session(); sess.State != "none" || sess.Handoff == nil || sess.Handoff.From != "pi" {
		t.Fatalf("no session to continue keeps the handoff: %+v", sess)
	}
	// An Agent that never talked has nothing to hand over.
	g := newRecEnv(t)
	g.switchTo("claude-code", "sonnet")
	if sess := g.session(); sess.Handoff != nil || sess.State != "none" {
		t.Fatalf("an empty conversation needs no handoff: %+v", sess)
	}
}

// A reply that was in progress during the change saves its session into the pending handoff, never as the new
// runtime's session; so the change can still be undone, and its sleep stays valid.
func TestSessionSavedByTheOldRuntimeAfterAChange(t *testing.T) {
	e := newRecEnv(t)
	busy := e.send(true, "")
	if err := e.s.SetRunRunning(e.ctx, busy.ID, 1); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: e.agent}
	receipt, err := e.s.RequestSleep(e.ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: busy.ID, Seconds: 120}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	if err := e.s.RecordRuntimeSessionFile(e.ctx, e.agent, "pi", "/tmp/pi-late.jsonl"); err != nil {
		t.Fatal(err)
	}
	sess := e.session()
	if sess.State != "none" || sess.SessionFile != "" || sess.Handoff == nil || sess.Handoff.SessionFile != "/tmp/pi-late.jsonl" {
		t.Fatalf("the old runtime's late save belongs to the pending handoff: %+v %+v", sess, sess.Handoff)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, busy.ID, store.RunEnd{Status: "completed", Text: "done"}); err != nil {
		t.Fatalf("the reply in progress ends normally: %v", err)
	}
	state, err := e.s.SleepState(e.ctx, p)
	if err != nil || state.Sleep == nil || state.Sleep.ID != receipt.Sleep.ID || state.Sleep.State != "sleeping" {
		t.Fatalf("a sleep survives a change of runtime: %+v %v", state, err)
	}
	e.switchTo("anthropic", "claude-x")
	if sess := e.session(); sess.State != "active" || sess.SessionFile != "/tmp/pi-late.jsonl" || sess.Handoff != nil {
		t.Fatalf("undoing the change continues the session saved late: %+v", sess)
	}
}

// The pending handoff ends once a turn that carried it reached the new runtime and the new runtime's session is
// saved; a turn that failed before that leaves it pending. /new drops it.
func TestHandoffEndsWhenDelivered(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	level := "medium"
	sel := &store.RunSelection{Runtime: "claude-code", Provider: "claude-code", ModelID: "sonnet", ThinkingLevel: &level}

	failed := e.send(true, "")
	pending, err := e.s.DispatchHandoff(e.ctx, e.agent)
	if err != nil || pending == nil {
		t.Fatalf("pending handoff: %+v %v", pending, err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, failed.ID, store.RunEnd{Status: "failed", ErrorCode: "runtime_login_required", ErrorMessage: "log in"}); err != nil {
		t.Fatal(err)
	}
	if sess := e.session(); sess.Handoff == nil || sess.Handoff.ID != pending.ID {
		t.Fatalf("a turn that never reached the runtime leaves the handoff pending: %+v", sess.Handoff)
	}

	carried := e.send(true, "")
	record := &store.HandoffRecord{ID: pending.ID, FromRuntime: "pi", ToRuntime: "claude-code", IncludedMessages: 2, Bytes: 300, BudgetBytes: 16000}
	if err := e.s.SetRunRunningWith(e.ctx, carried.ID, 3, store.RunStart{Selection: sel, Handoff: record}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordRuntimeSessionFile(e.ctx, e.agent, "claude-code", "/tmp/claude-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if sess := e.session(); sess.State != "active" || sess.SessionFile != "/tmp/claude-session.jsonl" || sess.Handoff == nil {
		t.Fatalf("the new runtime's session is saved; the handoff ends with the turn: %+v", sess)
	}
	run, _, _, err := e.s.FinishRun(e.ctx, carried.ID, store.RunEnd{Status: "completed", Text: "I remember"})
	if err != nil {
		t.Fatal(err)
	}
	if sess := e.session(); sess.Handoff != nil {
		t.Fatalf("a delivered handoff ends: %+v", sess.Handoff)
	}
	if got := run.SelectionInfo(); got == nil || *got.ThinkingLevel != "medium" || got.Runtime != "claude-code" || got.ModelID != "sonnet" {
		t.Fatalf("the run records who ran it: %+v", got)
	}
	if got := run.HandoffInfo(); got == nil || *got != *record {
		t.Fatalf("the run records the handoff it carried: %+v", got)
	}
	evs, err := e.s.RunEvents(e.ctx, carried.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		if ev.Type == store.EventContextHandoff {
			found = ev.Data["fromRuntime"] == "pi" && ev.Data["toRuntime"] == "claude-code" && ev.Data["includedMessages"] == float64(2) && ev.Data["bytes"] == float64(300)
		}
	}
	if !found {
		t.Fatalf("the handoff is recorded in the run's timeline: %+v", evs)
	}

	e.switchTo("anthropic", "claude-x")
	if sess := e.session(); sess.Handoff == nil {
		t.Fatal("changing again leaves a new pending handoff")
	}
	if sess, err := e.s.ResetSession(e.ctx, e.agent); err != nil || sess.Epoch != 2 {
		t.Fatalf("/new: %+v %v", sess, err)
	}
	if sess := e.session(); sess.Handoff != nil || sess.State != "none" {
		t.Fatalf("/new starts clean, with no handoff: %+v", sess)
	}
}
