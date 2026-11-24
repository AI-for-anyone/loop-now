package store_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
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
	if h := sess.Handoff; h == nil || h.ID == uuid.Nil || h.From != "pi" || h.SessionFile != "/tmp/pi-session.jsonl" || h.Ran || h.Launching != nil {
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
	starting := e.send(true, "")
	pending, err := e.s.DispatchHandoff(e.ctx, e.agent, starting.ID)
	if err != nil || pending == nil || pending.From != "pi" || pending.Launching == nil || *pending.Launching != starting.ID {
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
	pending, err := e.s.DispatchHandoff(e.ctx, e.agent, failed.ID)
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
	if _, err := e.s.DispatchHandoff(e.ctx, e.agent, carried.ID); err != nil {
		t.Fatal(err)
	}
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

// A first message that fails before it reaches the new runtime (not installed, not logged in) has run nothing there:
// changing back still continues the earlier session.
func TestFailedFirstStartKeepsTheChangeUndoable(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	failed := e.send(true, "")
	if _, err := e.s.DispatchHandoff(e.ctx, e.agent, failed.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, failed.ID, store.RunEnd{Status: "failed", ErrorCode: "runtime_not_found", ErrorMessage: "install it"}); err != nil {
		t.Fatal(err)
	}
	if sess := e.session(); sess.Handoff == nil || sess.Handoff.Launching != nil || sess.Handoff.Ran {
		t.Fatalf("a message that never reached the runtime ran nothing: %+v", sess.Handoff)
	}
	e.switchTo("anthropic", "claude-x")
	if sess := e.session(); sess.State != "active" || sess.SessionFile != "/tmp/pi-session.jsonl" || sess.Handoff != nil {
		t.Fatalf("changing back continues the earlier session: %+v", sess)
	}
}

// A handoff into a new session of the runtime it came from (no earlier session to continue) ends only with a message
// that carried it: a Pi command that starts the new session first does not end it.
func TestHandoffIntoTheSameRuntimeEndsOnlyWhenCarried(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "failed", "")
	e.switchTo("claude-code", "sonnet")
	e.switchTo("anthropic", "claude-x")
	sess := e.session()
	if sess.Handoff == nil || sess.Handoff.From != "pi" {
		t.Fatalf("pending handoff into Pi: %+v", sess.Handoff)
	}
	level := "medium"
	sel := &store.RunSelection{Runtime: "pi", Provider: "anthropic", ModelID: "claude-x", ThinkingLevel: &level}
	command := e.send(true, "")
	if _, err := e.s.DispatchHandoff(e.ctx, e.agent, command.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetRunRunningWith(e.ctx, command.ID, 1, store.RunStart{Selection: sel}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordRuntimeSessionFile(e.ctx, e.agent, "pi", "/tmp/pi-new.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, command.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if after := e.session(); after.State != "active" || after.Handoff == nil || after.Handoff.ID != sess.Handoff.ID {
		t.Fatalf("a session started without the handoff does not end it: %+v %+v", after, after.Handoff)
	}
	carried := e.send(true, "")
	pending, err := e.s.DispatchHandoff(e.ctx, e.agent, carried.ID)
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	if err := e.s.SetRunRunningWith(e.ctx, carried.ID, 1, store.RunStart{Selection: sel, Handoff: &store.HandoffRecord{ID: pending.ID, FromRuntime: "pi", ToRuntime: "pi"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, carried.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if sess := e.session(); sess.Handoff != nil {
		t.Fatalf("the message that carried it ends it: %+v", sess.Handoff)
	}
}

// A turn that carried the handoff and completed ends it even when the runtime's session could not be saved (Claude
// Code's session record not found): the runtime has it in its context, and it must not be sent again with every
// message. A turn that failed after reaching the runtime without a saved session keeps it pending.
func TestHandoffEndsWhenTheCarryingTurnCompletes(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	sel := &store.RunSelection{Runtime: "claude-code", Provider: "claude-code", ModelID: "sonnet"}
	carry := func(status string) {
		t.Helper()
		r := e.send(true, "")
		pending, err := e.s.DispatchHandoff(e.ctx, e.agent, r.ID)
		if err != nil || pending == nil {
			t.Fatal(pending, err)
		}
		if err := e.s.SetRunRunningWith(e.ctx, r.ID, 1, store.RunStart{Selection: sel, Handoff: &store.HandoffRecord{ID: pending.ID, FromRuntime: "pi", ToRuntime: "claude-code"}}); err != nil {
			t.Fatal(err)
		}
		end := store.RunEnd{Status: status}
		if status == "failed" {
			end.ErrorCode, end.ErrorMessage = "runtime_login_required", "log in"
		}
		if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, end); err != nil {
			t.Fatal(err)
		}
	}
	carry("failed")
	if sess := e.session(); sess.State != "none" || sess.Handoff == nil || !sess.Handoff.Ran {
		t.Fatalf("a failed turn without a saved session keeps the handoff pending: %+v %+v", sess, sess.Handoff)
	}
	carry("completed")
	if sess := e.session(); sess.State != "none" || sess.Handoff != nil {
		t.Fatalf("a completed turn ends the handoff: %+v %+v", sess, sess.Handoff)
	}
}

// The execution capture reads the session together with the Agent, so a run sees the runtime and the session (with
// its pending handoff) as of one moment.
func TestExecutionCaptureReadsTheSession(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.switchTo("claude-code", "sonnet")
	captured, err := e.s.CaptureExecutionIdentity(e.ctx, e.agent)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Agent.ModelProvider != "claude-code" || captured.Session.State != "none" || captured.Session.Handoff == nil || captured.Session.Handoff.From != "pi" {
		t.Fatalf("capture: %s %+v %+v", captured.Agent.ModelProvider, captured.Session, captured.Session.Handoff)
	}
}

// A session its runtime can no longer continue never waits in a "missing" state: it becomes a new start that carries
// the conversation. The pointer is cleared, the session number stays, and a session_lost handoff from the current
// runtime is pending; a handoff already pending stays as it is.
func TestMarkSessionMissingStartsOver(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/gone.jsonl"); err != nil {
		t.Fatal(err)
	}
	epoch := e.session().Epoch
	run := uuid.Must(uuid.NewV7())
	if err := e.s.MarkSessionMissing(e.ctx, e.agent, &run); err != nil {
		t.Fatal(err)
	}
	sess := e.session()
	if sess.State != "none" || sess.SessionFile != "" || sess.Epoch != epoch || sess.Handoff == nil || sess.Handoff.From != "pi" || sess.Handoff.Reason != store.HandoffSessionLost {
		t.Fatalf("a lost session leaves a session_lost handoff and keeps its number: %+v %+v", sess, sess.Handoff)
	}
	if evs := e.agentEvents(store.EventSessionLost); len(evs) != 1 || evs[0].Data["runtime"] != "pi" || evs[0].Data["sessionFile"] != "/tmp/gone.jsonl" || evs[0].RunID == nil || *evs[0].RunID != run {
		t.Fatalf("the loss is recorded with the run that found it: %+v", evs)
	}
	if evs := e.agentEvents(store.EventSessionMissing); len(evs) != 0 {
		t.Fatalf("no session.missing: %+v", evs)
	}
	// Nothing to lose without an active session.
	if err := e.s.MarkSessionMissing(e.ctx, e.agent, nil); err != nil || len(e.agentEvents(store.EventSessionLost)) != 1 {
		t.Fatalf("again: %v", err)
	}
	// A session the runtime records before the handoff was carried is a new one: the handoff stays.
	if err := e.s.RecordRuntimeSessionFile(e.ctx, e.agent, "pi", "/tmp/new.jsonl"); err != nil {
		t.Fatal(err)
	}
	if got := e.session(); got.Handoff == nil || got.Handoff.Reason != store.HandoffSessionLost {
		t.Fatalf("a new session does not end a session_lost handoff: %+v", got.Handoff)
	}

	f := newRecEnv(t)
	f.turn("hello", "completed", "hi")
	if err := f.s.RecordSessionFile(f.ctx, f.agent, "/tmp/pi.jsonl"); err != nil {
		t.Fatal(err)
	}
	f.switchTo("claude-code", "sonnet")
	pending := f.session().Handoff
	if err := f.s.RecordRuntimeSessionFile(f.ctx, f.agent, "claude-code", "/tmp/gone.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.MarkSessionMissing(f.ctx, f.agent, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.session(); got.State != "none" || got.Handoff == nil || got.Handoff.ID != pending.ID || got.Handoff.Reason != "" {
		t.Fatalf("a pending change of runtime stays: %+v %+v", got, got.Handoff)
	}
}

// Sessions left "missing" by an earlier version are turned into the same new start when the server starts.
func TestHealMissingSessions(t *testing.T) {
	e := newRecEnv(t)
	e.turn("hello", "completed", "hi")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/gone.jsonl"); err != nil {
		t.Fatal(err)
	}
	epoch := e.session().Epoch
	if _, err := e.s.Pool.Exec(e.ctx, `UPDATE agent_sessions SET state = 'missing' WHERE agent_id = $1`, e.agent); err != nil {
		t.Fatal(err)
	}
	healed, err := e.s.HealMissingSessions(e.ctx)
	if err != nil || len(healed) != 1 || healed[0] != e.agent {
		t.Fatalf("heal: %v %v", healed, err)
	}
	if sess := e.session(); sess.State != "none" || sess.SessionFile != "" || sess.Epoch != epoch || sess.Handoff == nil || sess.Handoff.Reason != store.HandoffSessionLost {
		t.Fatalf("healed: %+v %+v", sess, sess.Handoff)
	}
	if healed, err := e.s.HealMissingSessions(e.ctx); err != nil || len(healed) != 0 {
		t.Fatalf("nothing left: %v %v", healed, err)
	}
}

// Local ACP commands are kept with their arguments and environment; one that an Agent's provider names cannot be
// removed.
func TestACPCommands(t *testing.T) {
	e := newRecEnv(t)
	c, err := e.s.CreateACPCommand(e.ctx, "My Agent", "my-agent", []string{"--profile", "acp"}, map[string]string{"B": "2", "A": "1"})
	if err != nil || c.Name != "My Agent" || c.Command != "my-agent" || !slices.Equal(c.Args, []string{"--profile", "acp"}) || !slices.Equal(c.EnvNames(), []string{"A", "B"}) ||
		c.Runtime() != "acp-"+c.ID.String() {
		t.Fatalf("create: %+v %v", c, err)
	}
	name, three, changed := "Renamed", "3", "changed"
	u, err := e.s.UpdateACPCommand(e.ctx, c.ID, store.ACPCommandPatch{Name: &name, Env: map[string]*string{"C": &three, "A": nil, "B": &changed}})
	if err != nil || u.Name != "Renamed" || u.Command != "my-agent" || !slices.Equal(u.EnvNames(), []string{"B", "C"}) || u.Env["C"] != "3" || u.Env["B"] != "changed" {
		t.Fatalf("update merges the environment: %+v %v", u, err)
	}
	list, err := e.s.ACPCommands(e.ctx)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	provider, model := c.Runtime(), "default"
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: &provider, ModelID: &model}); err != nil {
		t.Fatal(err)
	}
	err = e.s.DeleteACPCommand(e.ctx, c.ID)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeRuntimeInUse {
		t.Fatalf("in use: %v", err)
	}
	if d, ok := ae.Details.(store.RuntimeInUseDetails); !ok || len(d.Agents) != 1 || d.Agents[0].ID != e.agent || d.Agents[0].Name == "" || d.Agents[0].ProjectID == nil {
		t.Fatalf("the Agents that use it: %+v", ae.Details)
	}
	e.switchTo("anthropic", "claude-x")
	if err := e.s.DeleteACPCommand(e.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteACPCommand(e.ctx, c.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("gone: %v", err)
	}
}
