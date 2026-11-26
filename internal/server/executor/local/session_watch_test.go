//go:build unix

package local

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
)

// A session is recorded while the turn runs, once the runtime has saved it, and again before its process is stopped:
// a first turn that never ends (the app quits, the process is killed) is then resumed by the next turn, not replaced.

// recordedSession is the session reference recorded for the Agent, and how many times one was recorded.
func (h *harness) recordedSession(id uuid.UUID) (string, int) {
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	return h.src.recorded[id], len(h.src.recordLog[id])
}

// startTurn runs a turn in the background; the returned channel gets its result when it ends.
func (h *harness) startTurn(id uuid.UUID, msg string) <-chan executor.Result {
	ch := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Message: msg}, newSink())
		ch <- res
	}()
	return ch
}

// waitRecorded waits until a session is recorded for the Agent while the turn is still running.
func (h *harness) waitRecorded(id uuid.UUID, turn <-chan executor.Result) string {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case res := <-turn:
			h.t.Fatalf("the turn ended before its session was recorded: %+v", res)
		default:
		}
		if ref, _ := h.recordedSession(id); ref != "" {
			return ref
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("the session was not recorded while the turn ran")
	return ""
}

// restart quits the executor as the app does and starts a new one on the same data, as reopening the app does.
func (h *harness) restart(o Options) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := h.e.Shutdown(ctx); err != nil {
		h.t.Fatal(err)
	}
	tmpls, err := clitemplate.Load("")
	if err != nil {
		h.t.Fatal(err)
	}
	h.e = New(o, h.src, tmpls, clitemplate.NewInstaller(h.root, time.Minute, nil), h.e.log)
	h.t.Cleanup(func() { _ = h.e.Shutdown(context.Background()) })
}

func importedCapture(t *testing.T) string {
	capture := filepath.Join(t.TempDir(), "capture.jsonl")
	if err := os.WriteFile(capture, []byte("{\"type\":\"user\",\"text\":\"before import\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return capture
}

// The app is killed in the middle of an imported Agent's first turn (its run never ends): the session claude saved was
// recorded while the turn ran, so after the restart the next turn resumes it instead of forking the imported copy again.
func TestImportedFirstTurnInterruptedIsResumed(t *testing.T) {
	var opts *Options
	h := newClaudeHarness(t, func(o *Options) {
		o.SessionCheckInterval = 20 * time.Millisecond
		opts = o
	})
	capture := importedCapture(t)
	id := h.claudeAgent(nil)
	h.src.imported = map[uuid.UUID]string{id: capture}
	turn := h.startTurn(id, "fake:slow")
	ref := h.waitRecorded(id, turn)
	first := h.launches()[0]
	sid := argValue(first, "--session-id")
	if argValue(first, "--resume") != capture || !hasFlag(first, "--fork-session") || filepath.Base(ref) != sid+".jsonl" {
		t.Fatalf("the first turn forks the capture and its own transcript is recorded: %v %q", first, ref)
	}
	_ = procgroup.Kill(h.claudePid(id))
	<-turn
	h.restart(*opts)
	res, _ := h.exec(id, "fake:history")
	if res.Text != "history=2" {
		t.Fatalf("the next turn should know the imported message and the interrupted one: %+v", res)
	}
	if args := h.launches()[1]; argValue(args, "--resume") != sid || hasFlag(args, "--fork-session") {
		t.Fatalf("the next start should resume the interrupted session, not fork the capture: %v", args)
	}
	if b, _ := os.ReadFile(capture); string(b) != "{\"type\":\"user\",\"text\":\"before import\"}\n" {
		t.Fatalf("the imported capture changed: %q", b)
	}
}

// A process stopped in the middle of a turn (shutdown, stop, idle) has its session recorded before it is killed, not
// only when its turn ends.
func TestStopRecordsSessionBeforeKill(t *testing.T) {
	h := newClaudeHarness(t, func(o *Options) { o.SessionCheckInterval = time.Hour })
	id := h.claudeAgent(nil)
	turn := h.startTurn(id, "fake:slow")
	waitFor(t, "claude started", func() bool { return h.claudePid(id) != 0 })
	time.Sleep(200 * time.Millisecond) // the message is in claude's transcript
	if ref, _ := h.recordedSession(id); ref != "" {
		t.Fatalf("nothing is recorded before the stop here: %q", ref)
	}
	pid := h.claudePid(id)
	aliveAtRecord := make(chan bool, 4)
	h.src.onRecord = func(uuid.UUID) { aliveAtRecord <- syscall.Kill(pid, 0) == nil }
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	h.e.stop(p, stopCause{reason: stopShutdown})
	select {
	case alive := <-aliveAtRecord:
		if !alive {
			t.Fatal("the session should be recorded before the process is stopped")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session was not recorded")
	}
	sid := argValue(h.launches()[0], "--session-id")
	if ref, n := h.recordedSession(id); filepath.Base(ref) != sid+".jsonl" || n != 1 {
		t.Fatalf("the stop should record the session once: %q (%d)", ref, n)
	}
	<-turn
	if _, n := h.recordedSession(id); n != 1 {
		t.Fatalf("the end of the turn should not record it again: %d", n)
	}
}

// The process of a plain Agent's first turn is killed outright (no stop runs, as when the server itself is killed): the
// session was already recorded while the turn ran, so the next turn resumes it.
func TestFirstTurnKilledIsResumed(t *testing.T) {
	h := newClaudeHarness(t, func(o *Options) { o.SessionCheckInterval = 20 * time.Millisecond })
	id := h.claudeAgent(nil)
	turn := h.startTurn(id, "fake:slow")
	ref := h.waitRecorded(id, turn)
	sid := argValue(h.launches()[0], "--session-id")
	if filepath.Base(ref) != sid+".jsonl" {
		t.Fatalf("recorded %q, want the transcript of %s", ref, sid)
	}
	_ = procgroup.Kill(h.claudePid(id))
	<-turn
	res, _ := h.exec(id, "fake:history")
	if res.Text != "history=1" {
		t.Fatalf("the next turn should know the interrupted one: %+v", res)
	}
	if args := h.launches()[1]; argValue(args, "--resume") != sid {
		t.Fatalf("the next start should resume the session: %v", args)
	}
	if _, n := h.recordedSession(id); n != 1 {
		t.Fatalf("the same session should be recorded once, got %d", n)
	}
}

// Killed before the runtime saved anything: nothing is recorded, and the next turn starts a new session as today.
func TestFirstTurnKilledBeforeSaveRecordsNothing(t *testing.T) {
	h := newClaudeHarness(t, func(o *Options) { o.SessionCheckInterval = 20 * time.Millisecond })
	h.env["FAKECLAUDE_NO_TRANSCRIPT"] = "1"
	id := h.claudeAgent(nil)
	turn := h.startTurn(id, "fake:slow")
	waitFor(t, "claude started", func() bool { return h.claudePid(id) != 0 })
	time.Sleep(200 * time.Millisecond)
	_ = procgroup.Kill(h.claudePid(id))
	<-turn
	if ref, n := h.recordedSession(id); ref != "" || n != 0 {
		t.Fatalf("nothing should be recorded: %q (%d)", ref, n)
	}
	delete(h.env, "FAKECLAUDE_NO_TRANSCRIPT")
	h.exec(id, "hello")
	if args := h.launches()[1]; hasFlag(args, "--resume") || argValue(args, "--session-id") == argValue(h.launches()[0], "--session-id") {
		t.Fatalf("the next start should open a new session: %v", args)
	}
}

// Codex, ACP and Pi record their session while the turn runs too, once the runtime has it saved.
func TestSessionRecordedDuringTurnOnEveryRuntime(t *testing.T) {
	every := func(o *Options) { o.SessionCheckInterval = 20 * time.Millisecond }
	t.Run("codex", func(t *testing.T) {
		h := newCodexHarness(t, every)
		id := h.codexAgent(nil)
		turn := h.startTurn(id, "fake:slow")
		if ref := h.waitRecorded(id, turn); ref == "" || filepath.IsAbs(ref) {
			t.Fatalf("a thread id should be recorded: %q", ref)
		}
	})
	for _, flavor := range []string{"grok", "agy"} {
		t.Run(flavor, func(t *testing.T) {
			h := newACPHarness(t, flavor, every)
			id := h.acpAgent(nil)
			turn := h.startTurn(id, "fake:slow")
			h.waitRecorded(id, turn)
		})
	}
	t.Run("pi", func(t *testing.T) {
		h := newHarness(t, every, "")
		id := h.agent(nil)
		turn := h.startTurn(id, "!sleep=1s")
		if ref := h.waitRecorded(id, turn); !filepath.IsAbs(ref) {
			t.Fatalf("Pi's session file should be recorded: %q", ref)
		}
		<-turn
		if _, n := h.recordedSession(id); n != 1 {
			t.Fatalf("recorded %d times, want once", n)
		}
	})
}
