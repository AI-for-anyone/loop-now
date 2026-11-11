package ccstream_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/ccstream"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/testutil/fakeclaudebin"
)

// start 在临时 HOME 与工作目录里启动 fakeclaude（会话记录写在临时 HOME 下）。
func start(t *testing.T, o ccstream.Options, env ...string) *ccstream.Client {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	cmd := exec.Command(fakeclaudebin.Path(t), "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--model", "haiku", "--session-id", "11111111-2222-3333-4444-555555555555", "--tools", "Bash,Read")
	cmd.Dir = cwd
	cmd.Env = append([]string{"HOME=" + home, "PATH=/usr/bin:/bin"}, env...)
	c, err := ccstream.Start(cmd, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.CloseStdin()
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

// sink 记下一轮里的事件。
type sink struct {
	mu       sync.Mutex
	text     strings.Builder
	events   []string
	firstOut chan struct{}
	once     sync.Once
}

func newSink() *sink { return &sink{firstOut: make(chan struct{})} }

func (s *sink) TextDelta(d string) {
	s.mu.Lock()
	s.text.WriteString(d)
	s.mu.Unlock()
	s.once.Do(func() { close(s.firstOut) })
}
func (s *sink) ToolStarted(id, name string) { s.add("start " + id + " " + name) }
func (s *sink) ToolEnded(id, name string, isError bool) {
	if isError {
		s.add("end-error " + id + " " + name)
		return
	}
	s.add("end " + id + " " + name)
}
func (s *sink) ToolCommand(id, name, command string) {
	s.add("command " + id + " " + name + " " + command)
}
func (s *sink) add(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}
func (s *sink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String() + " | " + strings.Join(s.events, "; ")
}

type observer struct {
	mu     sync.Mutex
	usages []ccstream.Usage
	limits []map[string]any
}

func (o *observer) Usage(u ccstream.Usage, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if inTurn {
		o.usages = append(o.usages, u)
	}
}
func (o *observer) RateLimit(info map[string]any, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.limits = append(o.limits, info)
}

// 一个进程连续处理多轮：文字流式交给 sink（子 Agent 的不转）、Bash 工具的开始结束与命令（记为 bash）、按模型的用量、init 信息；
// 不认识的输出类型忽略。
func TestTurnsTextToolsUsageAndInit(t *testing.T) {
	c := start(t, ccstream.Options{})
	obs := &observer{}
	c.SetObserver(obs)
	s := newSink()
	res, err := c.Prompt(ctx(t), "fake:tool echo hi", s)
	if err != nil || res.Outcome != runturn.OutcomeCompleted || res.Text != "ran" {
		t.Fatalf("工具这一轮：%+v %v", res, err)
	}
	if got := s.String(); got != "ran | start toolu_fake_1 Bash; command toolu_fake_1 bash echo hi; end toolu_fake_1 Bash" {
		t.Fatalf("sink：%s", got)
	}
	init := c.Init()
	if init == nil || init.Version != "9.9.9" || init.SessionID != "11111111-2222-3333-4444-555555555555" || init.Model != "claude-haiku-fake" || strings.Join(init.Tools, ",") != "Bash,Read" {
		t.Fatalf("init：%+v", init)
	}
	s = newSink()
	if res, err = c.Prompt(ctx(t), "fake:subagent", s); err != nil || res.Text != "main" || strings.Contains(s.String(), "SUBAGENT") {
		t.Fatalf("子 Agent 的文字不转给 sink：%+v %v %s", res, err, s)
	}
	if res, err = c.Prompt(ctx(t), "fake:usage2", nil); err != nil || res.Outcome != runturn.OutcomeCompleted {
		t.Fatalf("两个模型：%+v %v", res, err)
	}
	if res, err = c.Prompt(ctx(t), "fake:ratelimit", nil); err != nil || res.Text != "limited" {
		t.Fatalf("额度事件：%+v %v", res, err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.usages) != 5 || obs.usages[0].Model != "claude-haiku-fake" || obs.usages[0].Input != 10 || obs.usages[0].Output != 5 || obs.usages[0].CacheRead != 3 || obs.usages[0].CacheWrite != 2 || obs.usages[0].Reasoning != nil {
		t.Fatalf("用量：%+v", obs.usages)
	}
	if two := obs.usages[2:4]; two[0].Model != "claude-haiku-fake" || two[1].Model != "claude-helper-fake" || two[1].Reasoning == nil || *two[1].Reasoning != 7 {
		t.Fatalf("一轮两个模型各一条：%+v", two)
	}
	if len(obs.limits) != 1 || obs.limits[0]["rateLimitType"] != "five_hour" {
		t.Fatalf("额度事件：%+v", obs.limits)
	}
}

// 图片以 base64 块随文字发出。
func TestImages(t *testing.T) {
	c := start(t, ccstream.Options{})
	img := messageimage.Encode("image/png", []byte{0x89, 'P', 'N', 'G'})
	if res, err := c.Prompt(ctx(t), "fake:images", nil, img, img); err != nil || res.Text != "images=2" {
		t.Fatalf("图片：%+v %v", res, err)
	}
}

// claude 报告的错误为 runtime_error，消息取 result；未登录等需要进一步判断的保留原始字段。
func TestFailures(t *testing.T) {
	c := start(t, ccstream.Options{})
	res, err := c.Prompt(ctx(t), "fake:error boom", nil)
	if err != nil || res.Outcome != runturn.OutcomeFailed || res.ErrorCode != executor.ErrRuntimeError || res.ErrorMessage != "boom" {
		t.Fatalf("错误：%+v %v", res, err)
	}
	res, err = c.Prompt(ctx(t), "fake:notloggedin", nil)
	last := c.LastResult()
	if err != nil || res.ErrorCode != executor.ErrRuntimeError || last.TerminalReason != "api_error" || last.APIErrorStatus != nil || !strings.HasPrefix(last.Result, "Not logged in") {
		t.Fatalf("未登录：%+v %+v %v", res, last, err)
	}
	if res, err = c.Prompt(ctx(t), "still alive", nil); err != nil || res.Text != "echo: still alive" {
		t.Fatalf("出错后进程照常：%+v %v", res, err)
	}
}

// 打断：控制消息得到确认时这一轮以打断结束，进程留着，下一轮正常。
func TestInterruptByControlMessage(t *testing.T) {
	c := start(t, ccstream.Options{})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:slow", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.Outcome != runturn.OutcomeAborted || !strings.HasPrefix(res.Text, ".") {
		t.Fatalf("打断：%+v", res)
	}
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatalf("没有进行中的一轮时打断什么也不做：%v", err)
	}
	if res, err := c.Prompt(ctx(t), "after", nil); err != nil || res.Text != "echo: after" {
		t.Fatalf("打断后的下一轮：%+v %v", res, err)
	}
}

// 控制消息等不到确认时改发 SIGINT：这一轮以打断结束，进程随后退出。
func TestInterruptFallsBackToSignal(t *testing.T) {
	c := start(t, ccstream.Options{InterruptGrace: 200 * time.Millisecond})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:noack", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Outcome != runturn.OutcomeAborted {
		t.Fatalf("SIGINT 打断：%+v", res)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT 之后进程应退出")
	}
	if _, err := c.Prompt(ctx(t), "x", nil); !errors.Is(err, runturn.ErrExited) {
		t.Fatalf("退出后再发消息：%v", err)
	}
}

// 一轮中进程退出：runtime_exited，带已收到的文字；之后发消息返回 ErrExited。
func TestExitMidTurn(t *testing.T) {
	c := start(t, ccstream.Options{})
	if _, err := c.Prompt(ctx(t), "warm up", nil); err != nil {
		t.Fatal(err)
	}
	res, err := c.Prompt(ctx(t), "fake:exit", nil)
	if err != nil || res.Outcome != runturn.OutcomeFailed || res.ErrorCode != executor.ErrRuntimeExited || res.Text != "partial" {
		t.Fatalf("进程退出：%+v %v", res, err)
	}
	<-c.Done()
	if c.ExitErr() == nil || !strings.Contains(c.Stderr(), "crashing on purpose") {
		t.Fatalf("退出码与错误输出：%v %q", c.ExitErr(), c.Stderr())
	}
	if _, err := c.Prompt(ctx(t), "x", nil); !errors.Is(err, runturn.ErrExited) {
		t.Fatalf("退出后再发消息：%v", err)
	}
}

// 启动即退出（例如不认识的参数）与迟迟没有 init：都是 runtime_start_failed，错误输出留给调用方拼进消息。
func TestStartFailures(t *testing.T) {
	c := start(t, ccstream.Options{}, "FAKECLAUDE_EXIT_AT_START=1")
	<-c.Done()
	res, err := c.Prompt(ctx(t), "hi", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(c.Stderr(), "unknown option") {
		t.Fatalf("启动即退出：%+v %v %q", res, err, c.Stderr())
	}
	slow := start(t, ccstream.Options{InitTimeout: 300 * time.Millisecond}, "FAKECLAUDE_NO_INIT=1")
	res, err = slow.Prompt(ctx(t), "hi", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(res.ErrorMessage, "启动超时") {
		t.Fatalf("启动超时：%+v %v", res, err)
	}
	select {
	case <-slow.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("启动超时后进程应被结束")
	}
}

// 发过 SIGINT 的进程随后一定退出，写进去的消息会丢：打断那一轮结束后立刻发下一条，不写入，等它退出后返回 ErrExited，
// 让调用方用 --resume 重新起进程。
func TestPromptAfterSignalWaitsForExit(t *testing.T) {
	c := start(t, ccstream.Options{InterruptGrace: 200 * time.Millisecond})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:noack", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Outcome != runturn.OutcomeAborted {
		t.Fatalf("打断：%+v", res)
	}
	if !c.Exiting() {
		t.Fatal("发过 SIGINT 后应标记为正在退出")
	}
	begin := time.Now()
	res, err := c.Prompt(ctx(t), "right after", nil)
	if !errors.Is(err, runturn.ErrExited) || res.Text != "" {
		t.Fatalf("紧跟的下一条不应写进正在退出的进程：%+v %v", res, err)
	}
	if time.Since(begin) > 4*time.Second {
		t.Fatalf("应等到进程退出就返回，用了 %v", time.Since(begin))
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("返回时进程应已退出")
	}
}

// 工具调用只出现在完整的 assistant 消息里时补登记：开始、命令与结束都对得上。
func TestToolUseOnlyInAssistantMessage(t *testing.T) {
	c := start(t, ccstream.Options{})
	s := newSink()
	res, err := c.Prompt(ctx(t), "fake:tool2 ls -la", s)
	if err != nil || res.Text != "ran2" {
		t.Fatalf("结果：%+v %v", res, err)
	}
	if got := s.String(); got != "ran2 | start toolu_fake_2 Bash; command toolu_fake_2 bash ls -la; end-error toolu_fake_2 Bash" {
		t.Fatalf("sink：%s", got)
	}
}

// 一行超过上限时停读并结束进程：这一轮以 runtime_exited 结束，不会挂住。
func TestOverlongLineEndsTurn(t *testing.T) {
	c := start(t, ccstream.Options{MaxLine: 1024})
	if _, err := c.Prompt(ctx(t), "warm up", nil); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	res, err := c.Prompt(ctx(t), "fake:bigline", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeExited {
		t.Fatalf("超长行：%+v %v", res, err)
	}
	if time.Since(begin) > 10*time.Second {
		t.Fatalf("不应等到外层超时：%v", time.Since(begin))
	}
}
