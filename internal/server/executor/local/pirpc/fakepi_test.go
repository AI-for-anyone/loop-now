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
		t.Fatalf("结果不符：%+v", res)
	}
	deltas, _ := rec.snapshot()
	if len(deltas) != 3 || strings.Join(deltas, "") != "echo: hello" {
		t.Fatalf("应分 3 段流式输出：%q", deltas)
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
		t.Fatalf("结果不符：%+v", res)
	}
	_, tools := rec.snapshot()
	want := []string{"start call_1 bash", "end call_1 bash false", "start call_2 edit", "end call_2 edit true"}
	if !slices.Equal(tools, want) {
		t.Fatalf("工具事件不符：%q", tools)
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
		t.Fatalf("结果不符：%+v", res)
	}
	// 出错后进程仍可继续处理下一条消息。
	res, err = p.Prompt(ctx, "again", nil)
	if err != nil || res.Text != "echo: again" {
		t.Fatalf("出错后应能继续对话：%+v %v", res, err)
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
		t.Fatalf("被重试取代的出错消息不应计入文本：%+v", res)
	}
	if !strings.Contains(rec.text(), "retry-attempt-1") {
		t.Fatalf("失败尝试的增量仍应实时推送：%q", rec.text())
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
	if res != (TurnResult{Outcome: OutcomeCompleted}) {
		t.Fatalf("handled 应直接完成且无文本：%+v", res)
	}
	res, err = p.Prompt(ctx, "next", nil)
	if err != nil || res.Text != "echo: next" {
		t.Fatalf("handled 后应能继续：%+v %v", res, err)
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
		t.Fatalf("结果不符：%+v", res)
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
		t.Fatal("abort 应很快返回")
	}
	out := recvPrompt(t, ch)
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res != (TurnResult{Outcome: OutcomeAborted, Text: "aaa"}) {
		t.Fatalf("打断应保留已产生的文字：%+v", out.res)
	}
	// 同一进程继续处理下一条消息。
	res, err := p.Prompt(ctx, "after", nil)
	if err != nil || res.Text != "echo: after" {
		t.Fatalf("打断后应能继续：%+v %v", res, err)
	}
}

func TestFakeAbortIdle(t *testing.T) {
	p := startFake(t, nil)
	if err := p.Abort(testCtx(t)); err != nil {
		t.Fatalf("空闲时 abort 应成功：%v", err)
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
		t.Fatalf("Pi 不响应 abort 时应超时：%v", err)
	}
	_ = p.cmd.Process.Kill()
	out := recvPrompt(t, ch)
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res.Outcome != OutcomeFailed || out.res.ErrorCode != "pi_exited" || out.res.Text != "aaa" {
		t.Fatalf("强杀后应以 pi_exited 结束并保留文字：%+v", out.res)
	}
	waitDone(t, p.Client)
	if p.ExitErr() == nil {
		t.Fatal("被强杀的进程应有退出错误")
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
		t.Fatalf("进程崩溃应以 pi_exited 结束并保留文字：%+v", res)
	}
	waitDone(t, p.Client)
	var ee *exec.ExitError
	if !errors.As(p.ExitErr(), &ee) || ee.ExitCode() != 3 {
		t.Fatalf("退出码应为 3：%v", p.ExitErr())
	}
	if _, err := p.Prompt(ctx, "x", nil); !errors.Is(err, ErrExited) {
		t.Fatalf("进程退出后 Prompt 应返回 ErrExited：%v", err)
	}
	if _, err := p.GetState(ctx); !errors.Is(err, ErrExited) {
		t.Fatalf("进程退出后 Call 应返回 ErrExited：%v", err)
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
		t.Fatalf("超长行应完整读取：outcome=%s len=%d", res.Outcome, len(res.Text))
	}
	deltas, _ := rec.snapshot()
	for _, d := range deltas {
		if len(d) <= 64<<10 {
			t.Fatalf("每段增量应超过 64KB：%d", len(d))
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
		t.Fatalf("会话路径不符：%+v", st)
	}
	if _, err := os.Stat(st.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("首条消息前不应写会话文件：%v", err)
	}
	if res, err := p.Prompt(ctx, "!nosession hi", nil); err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(st.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("!nosession 不应写会话文件：%v", err)
	}
	if res, err := p.Prompt(ctx, "hello", nil); err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(st.SessionFile); err != nil {
		t.Fatalf("首条消息后应写会话文件：%v", err)
	}
	if err := p.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p.Client)
	if p.ExitErr() != nil {
		t.Fatalf("关闭 stdin 后应正常退出：%v", p.ExitErr())
	}

	p2 := startFake(t, nil, "--session", st.SessionFile)
	st2, err := p2.GetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st2 != st {
		t.Fatalf("接续会话应保持路径与 ID：%+v vs %+v", st2, st)
	}
}

func TestFakeNoSessionFlag(t *testing.T) {
	p := startFake(t, nil, "--no-session")
	st, err := p.GetState(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if st.SessionFile != "" || st.SessionID == "" {
		t.Fatalf("--no-session 时应无会话文件：%+v", st)
	}
}

func TestFakeMissingSessionExits(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	p := startFake(t, nil, "--session", missing)
	waitDone(t, p.Client)
	var ee *exec.ExitError
	if !errors.As(p.ExitErr(), &ee) || ee.ExitCode() != 1 {
		t.Fatalf("会话文件不存在应以 1 退出：%v", p.ExitErr())
	}
	if !strings.Contains(p.Stderr(), "会话文件不存在") {
		t.Fatalf("stderr 应包含原因：%q", p.Stderr())
	}
	if _, err := p.Prompt(testCtx(t), "x", nil); !errors.Is(err, ErrExited) {
		t.Fatalf("应返回 ErrExited：%v", err)
	}
}

func TestFakeUnknownCommand(t *testing.T) {
	p := startFake(t, nil)
	_, err := p.Call(testCtx(t), "bogus", map[string]any{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "Unknown command: bogus") {
		t.Fatalf("未知命令应失败并带 Pi 的错误文本：%v", err)
	}
}

func TestFakeConcurrentPromptRejected(t *testing.T) {
	p := startFake(t, nil)
	ctx := testCtx(t)
	rec := newRecorder()
	ch := goPrompt(ctx, p.Client, "!sleep=30s long", rec)
	rec.waitFirst(t)
	if _, err := p.Prompt(ctx, "second", nil); err == nil || errors.Is(err, ErrExited) {
		t.Fatalf("并发 Prompt 应立即报错：%v", err)
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
		t.Fatalf("dump 内容不符：%+v", d)
	}
	gotCwd, _ := filepath.EvalSymlinks(d.Cwd)
	wantCwd, _ := filepath.EvalSymlinks(p.dir)
	if gotCwd != wantCwd {
		t.Fatalf("cwd 不符：%s vs %s", gotCwd, wantCwd)
	}

	out, err := exec.Command(fakepiBin, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "1.1.0" {
		t.Fatalf("默认版本应为 1.1.0：%q %v", out, err)
	}
	cmd := exec.Command(fakepiBin, "--version")
	cmd.Env = []string{"FAKEPI_VERSION=9.9.9"}
	out, err = cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "9.9.9" {
		t.Fatalf("应输出 FAKEPI_VERSION：%q %v", out, err)
	}
}
