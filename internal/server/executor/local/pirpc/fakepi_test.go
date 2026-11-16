package pirpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFakeEchoStreamsDeltas(t *testing.T) {
	p := startFake(t, nil)
	rec := newRecorder()
	res, err := p.Prompt(testCtx(t), "hello", rec)
	if err != nil {
		t.Fatal(err)
	}
	if res != (TurnResult{Outcome: OutcomeCompleted, Text: "echo: hello"}) {
		t.Fatalf("unexpected result: %+v", res)
	}
	deltas, _ := rec.snapshot()
	if len(deltas) != 3 || strings.Join(deltas, "") != "echo: hello" {
		t.Fatalf("should stream in 3 parts: %q", deltas)
	}
}

func TestFakeToolEvents(t *testing.T) {
	p := startFake(t, nil)
	rec := newRecorder()
	res, err := p.Prompt(testCtx(t), "!tool=bash !toolfail=edit hi", rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCompleted || res.Text != "echo: hi" {
		t.Fatalf("unexpected result: %+v", res)
	}
	_, tools := rec.snapshot()
	want := []string{"start call_1 bash", "end call_1 bash false", "start call_2 edit", "end call_2 edit true"}
	if !slices.Equal(tools, want) {
		t.Fatalf("unexpected tool events: %q", tools)
	}
}

func TestFakeErrorStopReason(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	res, err := p.Prompt(ctx, "!error=boom_now", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := TurnResult{Outcome: OutcomeFailed, ErrorCode: "pi_error", ErrorMessage: "boom now"}
	if res != want {
		t.Fatalf("unexpected result: %+v", res)
	}
	// After an error the process can still handle the next message.
	res, err = p.Prompt(ctx, "again", nil)
	if err != nil || res.Text != "echo: again" {
		t.Fatalf("the conversation should go on after an error: %+v %v", res, err)
	}
}

func TestFakeRetrySuperseded(t *testing.T) {
	p := startFake(t, nil)
	rec := newRecorder()
	res, err := p.Prompt(testCtx(t), "!retry !text=final_answer", rec)
	if err != nil {
		t.Fatal(err)
	}
	if res != (TurnResult{Outcome: OutcomeCompleted, Text: "final answer"}) {
		t.Fatalf("an error message superseded by a retry should not count toward the text: %+v", res)
	}
	if !strings.Contains(rec.text(), "retry-attempt-1") {
		t.Fatalf("deltas of the failed attempt should still be pushed live: %q", rec.text())
	}
}

func TestFakeHandledDisposition(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	rec := newRecorder()
	res, err := p.Prompt(ctx, "!handled", rec)
	if err != nil {
		t.Fatal(err)
	}
	if res != (TurnResult{Outcome: OutcomeCompleted, Handled: true}) {
		t.Fatalf("handled should complete at once, flagged as handled and with no text: %+v", res)
	}
	res, err = p.Prompt(ctx, "next", nil)
	if err != nil || res.Text != "echo: next" {
		t.Fatalf("should go on after handled: %+v %v", res, err)
	}
}

func TestFakePromptRejected(t *testing.T) {
	p := startFake(t, nil)
	res, err := p.Prompt(testCtx(t), "!rejectprompt", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := TurnResult{Outcome: OutcomeFailed, ErrorCode: "pi_error", ErrorMessage: "rejected"}
	if res != want {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestFakeAbortMidTurnKeepsPartialText(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	rec := newRecorder()
	ch := goPrompt(ctx, p.Client, "!sleep=30s !text=aaa_bbb_ccc", rec)
	rec.waitFirst(t)
	start := time.Now()
	if err := p.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("abort should return quickly")
	}
	out := recvPrompt(t, ch)
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res != (TurnResult{Outcome: OutcomeAborted, Text: "aaa"}) {
		t.Fatalf("an interrupt should keep the text produced so far: %+v", out.res)
	}
	// The same process goes on to handle the next message.
	res, err := p.Prompt(ctx, "after", nil)
	if err != nil || res.Text != "echo: after" {
		t.Fatalf("should go on after an interrupt: %+v %v", res, err)
	}
}

func TestFakeAbortIdle(t *testing.T) {
	p := startFake(t, nil)
	if err := p.Abort(testCtx(t)); err != nil {
		t.Fatalf("abort should succeed when idle: %v", err)
	}
}

func TestFakeAbortTimeoutThenKill(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	rec := newRecorder()
	ch := goPrompt(ctx, p.Client, "!noabort !sleep=30s !text=aaa_bbb_ccc", rec)
	rec.waitFirst(t)
	actx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if err := p.Abort(actx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("should time out when Pi does not respond to abort: %v", err)
	}
	_ = p.cmd.Process.Kill()
	out := recvPrompt(t, ch)
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res.Outcome != OutcomeFailed || out.res.ErrorCode != "pi_exited" || out.res.Text != "aaa" {
		t.Fatalf("after a kill the turn should end with pi_exited and keep the text: %+v", out.res)
	}
	waitDone(t, p.Client)
	if p.ExitErr() == nil {
		t.Fatal("a killed process should have an exit error")
	}
}

func TestFakeCrashMidTurn(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	res, err := p.Prompt(ctx, "!crash !text=aaa_bbb_ccc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeFailed || res.ErrorCode != "pi_exited" || res.Text != "aaa" {
		t.Fatalf("a process crash should end with pi_exited and keep the text: %+v", res)
	}
	waitDone(t, p.Client)
	var ee *exec.ExitError
	if !errors.As(p.ExitErr(), &ee) || ee.ExitCode() != 3 {
		t.Fatalf("exit code should be 3: %v", p.ExitErr())
	}
	if _, err := p.Prompt(ctx, "x", nil); !errors.Is(err, ErrExited) {
		t.Fatalf("Prompt should return ErrExited after the process exits: %v", err)
	}
	if _, err := p.GetState(ctx); !errors.Is(err, ErrExited) {
		t.Fatalf("Call should return ErrExited after the process exits: %v", err)
	}
}

func TestFakeLargeLines(t *testing.T) {
	p := startFake(t, nil)
	big := strings.Repeat("x", 200<<10)
	rec := newRecorder()
	res, err := p.Prompt(testCtx(t), "!text="+big, rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCompleted || res.Text != big {
		t.Fatalf("an overlong line should be read in full: outcome=%s len=%d", res.Outcome, len(res.Text))
	}
	deltas, _ := rec.snapshot()
	for _, d := range deltas {
		if len(d) <= 64<<10 {
			t.Fatalf("every delta should be over 64KB: %d", len(d))
		}
	}
}

func TestFakeSessionLazyAndResume(t *testing.T) {
	sd := filepath.Join(t.TempDir(), "sessions")
	p := startFake(t, nil, "--session-dir", sd)
	ctx := testCtx(t)
	st, err := p.GetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(st.SessionFile) != sd || st.SessionID == "" || !strings.Contains(st.SessionFile, st.SessionID) {
		t.Fatalf("unexpected session path: %+v", st)
	}
	if _, err := os.Stat(st.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("the session file should not be written before the first message: %v", err)
	}
	if res, err := p.Prompt(ctx, "!nosession hi", nil); err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(st.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("!nosession should not write the session file: %v", err)
	}
	if res, err := p.Prompt(ctx, "hello", nil); err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(st.SessionFile); err != nil {
		t.Fatalf("the session file should be written after the first message: %v", err)
	}
	if err := p.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p.Client)
	if p.ExitErr() != nil {
		t.Fatalf("should exit normally after stdin is closed: %v", p.ExitErr())
	}

	p2 := startFake(t, nil, "--session", st.SessionFile)
	st2, err := p2.GetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st2 != st {
		t.Fatalf("a resumed session should keep its path and ID: %+v vs %+v", st2, st)
	}
}

func TestFakeNoSessionFlag(t *testing.T) {
	p := startFake(t, nil, "--no-session")
	st, err := p.GetState(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if st.SessionFile != "" || st.SessionID == "" {
		t.Fatalf("--no-session should mean no session file: %+v", st)
	}
}

func TestFakeMissingSessionExits(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	p := startFake(t, nil, "--session", missing)
	waitDone(t, p.Client)
	var ee *exec.ExitError
	if !errors.As(p.ExitErr(), &ee) || ee.ExitCode() != 1 {
		t.Fatalf("a missing session file should exit with 1: %v", p.ExitErr())
	}
	if !strings.Contains(p.Stderr(), "session file does not exist") {
		t.Fatalf("stderr should contain the reason: %q", p.Stderr())
	}
	if _, err := p.Prompt(testCtx(t), "x", nil); !errors.Is(err, ErrExited) {
		t.Fatalf("should return ErrExited: %v", err)
	}
}

func TestFakeUnknownCommand(t *testing.T) {
	p := startFake(t, nil)
	_, err := p.Call(testCtx(t), "bogus", map[string]any{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "Unknown command: bogus") {
		t.Fatalf("an unknown command should fail with Pi's error text: %v", err)
	}
}

func TestFakeConcurrentPromptRejected(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	rec := newRecorder()
	ch := goPrompt(ctx, p.Client, "!sleep=30s long", rec)
	rec.waitFirst(t)
	if _, err := p.Prompt(ctx, "second", nil); err == nil || errors.Is(err, ErrExited) {
		t.Fatalf("a concurrent Prompt should fail right away: %v", err)
	}
	if err := p.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if out := recvPrompt(t, ch); out.res.Outcome != OutcomeAborted {
		t.Fatalf("%+v", out)
	}
}

func TestFakeDumpAndVersion(t *testing.T) {
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	p := startFake(t, []string{"FAKEPI_DUMP=" + dumpPath, "SECRET_X=1"}, "--skill", "a", "--skill", "b", "--model", "m1")
	if _, err := p.GetState(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
		Cwd  string            `json:"cwd"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(d.Args, "--skill") || d.Args[len(d.Args)-1] != "m1" || d.Env["SECRET_X"] != "1" {
		t.Fatalf("unexpected dump contents: %+v", d)
	}
	gotCwd, _ := filepath.EvalSymlinks(d.Cwd)
	wantCwd, _ := filepath.EvalSymlinks(p.dir)
	if gotCwd != wantCwd {
		t.Fatalf("cwd mismatch: %s vs %s", gotCwd, wantCwd)
	}

	out, err := exec.Command(fakepiBin, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "1.4.0" {
		t.Fatalf("the default version should be 1.4.0: %q %v", out, err)
	}
	cmd := exec.Command(fakepiBin, "--version")
	cmd.Env = []string{"FAKEPI_VERSION=9.9.9"}
	out, err = cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "9.9.9" {
		t.Fatalf("should print FAKEPI_VERSION: %q %v", out, err)
	}
}
