package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// recObs 记录执行器回报的运行记录。
type recObs struct {
	mu      sync.Mutex
	events  []obsEvent
	usage   []obsUsage
	changed map[uuid.UUID]int
}

type obsEvent struct {
	agent uuid.UUID
	run   *uuid.UUID
	ev    executor.Event
}

type obsUsage struct {
	agent uuid.UUID
	run   *uuid.UUID
	u     executor.Usage
}

func (o *recObs) Event(agentID uuid.UUID, runID *uuid.UUID, e executor.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, obsEvent{agentID, copyID(runID), e})
}

func (o *recObs) Usage(agentID uuid.UUID, runID *uuid.UUID, u executor.Usage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.usage = append(o.usage, obsUsage{agentID, copyID(runID), u})
}

func (o *recObs) ProcessChanged(agentID uuid.UUID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.changed == nil {
		o.changed = map[uuid.UUID]int{}
	}
	o.changed[agentID]++
}

func copyID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}

// eventsOf 返回某个 Agent 的事件（按回报顺序），可按类型过滤。
func (o *recObs) eventsOf(agentID uuid.UUID, types ...string) []obsEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []obsEvent
	for _, e := range o.events {
		if e.agent != agentID {
			continue
		}
		if len(types) > 0 && !contains(types, e.ev.Type) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (o *recObs) usageOf(agentID uuid.UUID) []obsUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []obsUsage
	for _, u := range o.usage {
		if u.agent == agentID {
			out = append(out, u)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// waitFor 轮询直到 cond 成立。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时：%s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *harness) execRun(id uuid.UUID, msg string) (uuid.UUID, executor.Result) {
	h.t.Helper()
	runID := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: msg}, newSink())
	if err != nil {
		h.t.Fatalf("Execute: %v", err)
	}
	return runID, res
}

func types(evs []obsEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ev.Type
	}
	return out
}

func TestRecordsStartReuseAndConfigChange(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)

	run1, res := h.execRun(id, "hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("第一次执行失败：%+v", res)
	}
	evs := h.obs.eventsOf(id)
	if got := types(evs); strings.Join(got, ",") != store.EventWorkspaceSync+","+store.EventPiStarted {
		t.Fatalf("首次启动的事件应为同步、启动：%v", got)
	}
	for _, e := range evs {
		if e.run == nil || *e.run != run1 {
			t.Fatalf("事件应关联到这次执行：%+v", e)
		}
	}
	if sync := evs[0].ev.Data; sync["result"] != "succeeded" {
		t.Fatalf("同步应成功：%v", sync)
	}
	started := evs[1].ev.Data
	if started["reason"] != "no_process" || started["pid"] != h.pid(id) || started["configVersion"] != int64(1) {
		t.Fatalf("启动事件不对：%v", started)
	}
	if _, ok := started["startupMs"].(int64); !ok {
		t.Fatalf("启动事件应带启动用时：%v", started)
	}

	run2, _ := h.execRun(id, "again")
	reused := h.obs.eventsOf(id, store.EventPiReused)
	if len(reused) != 1 || *reused[0].run != run2 || reused[0].ev.Data["pid"] != h.pid(id) {
		t.Fatalf("第二次执行应复用进程：%+v", reused)
	}

	oldPid := h.pid(id)
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	run3, _ := h.execRun(id, "after change")
	stopped := h.obs.eventsOf(id, store.EventPiStopped)
	if len(stopped) != 1 || stopped[0].ev.Data["reason"] != "config_changed" || *stopped[0].run != run3 || stopped[0].ev.Data["pid"] != oldPid {
		t.Fatalf("配置变化应记录停止旧进程：%+v", stopped)
	}
	starts := h.obs.eventsOf(id, store.EventPiStarted)
	if len(starts) != 2 || starts[1].ev.Data["reason"] != "config_changed" || *starts[1].run != run3 {
		t.Fatalf("配置变化后应记录重启：%+v", starts)
	}
	h.obs.mu.Lock()
	changed := h.obs.changed[id]
	h.obs.mu.Unlock()
	if changed == 0 {
		t.Fatal("进程状态变化应通知观察者")
	}
}

func TestRecordsUsageAttribution(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	runID, res := h.execRun(id, "!usage=100,20,1000,0 !respmodel=claude-x-2026 !tool=bash !toolusage=3,1 !compact=500,50 !warmafter=0,1,2000 hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("执行失败：%+v", res)
	}
	waitFor(t, "两轮之间的预热用量", func() bool { return len(h.obs.usageOf(id)) == 5 })
	usage := h.obs.usageOf(id)
	kinds := []string{}
	for _, u := range usage[:4] {
		kinds = append(kinds, u.u.Kind)
		if u.run == nil || *u.run != runID {
			t.Fatalf("一轮之中的用量应关联到这次执行：%+v", u)
		}
	}
	if strings.Join(kinds, ",") != "assistant,tool,compaction,assistant" {
		t.Fatalf("用量顺序不对：%v", kinds)
	}
	if a := usage[0].u; a.Provider != "anthropic" || a.Model != "claude-x" || a.ResponseModel != "claude-x-2026" || a.Input != 100 || a.CacheRead != 1000 || a.StopReason != "toolUse" {
		t.Fatalf("助手用量不对：%+v", a)
	}
	if tool := usage[1].u; tool.Provider != "" || tool.Model != "" || tool.Input != 3 {
		t.Fatalf("工具用量应记为未知模型：%+v", tool)
	}
	if c := usage[2].u; c.Provider != "anthropic" || c.Model != "claude-x" || c.Input != 500 {
		t.Fatalf("压缩用量应计入进程启动时的模型：%+v", c)
	}
	if w := usage[4]; w.run != nil || w.u.Kind != "cache_warm" || w.u.CacheRead != 2000 {
		t.Fatalf("两轮之间的预热不应属于任何执行：%+v", w)
	}
	comp := h.obs.eventsOf(id, store.EventContextCompaction)
	if len(comp) != 1 || comp[0].ev.Data["result"] != "succeeded" || *comp[0].run != runID {
		t.Fatalf("应记录一次压缩：%+v", comp)
	}
}

func TestRecordsRetry(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	runID, _ := h.execRun(id, "!retry hi")
	evs := h.obs.eventsOf(id, store.EventModelRetryStarted, store.EventModelRetryEnded)
	if len(evs) != 2 || evs[0].ev.Type != store.EventModelRetryStarted || evs[1].ev.Data["success"] != true || *evs[1].run != runID {
		t.Fatalf("应记录一次重试的开始与结束：%+v", evs)
	}
}

func TestRecordsIdleTimeout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.IdleTimeout = 200 * time.Millisecond }, "")
	id := h.agent(nil)
	h.execRun(id, "hi")
	waitFor(t, "空闲超时停止", func() bool { return len(h.obs.eventsOf(id, store.EventPiStopped)) == 1 })
	st := h.obs.eventsOf(id, store.EventPiStopped)[0]
	if st.run != nil || st.ev.Data["reason"] != "idle_timeout" {
		t.Fatalf("空闲超时的停止事件不对：%+v", st)
	}
	if len(h.obs.eventsOf(id, store.EventPiExited)) != 0 {
		t.Fatal("主动停止不应记为意外退出")
	}
	if p := h.e.Process(id); p.State != executor.ProcessStopped || p.StartedAt != nil || p.PID != 0 {
		t.Fatalf("停止后进程状态不对：%+v", p)
	}
}

func TestRecordsCrashDuringRun(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.execRun(id, "first")
	runID, res := h.execRun(id, "!crash")
	if res.ErrorCode != executor.ErrPiExited || !strings.Contains(res.ErrorMessage, "模拟崩溃") {
		t.Fatalf("崩溃的错误信息应带 Pi 错误输出：%+v", res)
	}
	waitFor(t, "意外退出事件", func() bool { return len(h.obs.eventsOf(id, store.EventPiExited)) == 1 })
	ex := h.obs.eventsOf(id, store.EventPiExited)[0]
	if ex.run == nil || *ex.run != runID || !strings.Contains(ex.ev.Data["stderrTail"].(string), "模拟崩溃") ||
		!strings.Contains(ex.ev.Data["exit"].(string), "exit status 3") {
		t.Fatalf("回复中崩溃的事件不对：%+v", ex)
	}
}

func TestRecordsIdleCrash(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.execRun(id, "hi")
	pid := h.pid(id)
	if pid == 0 {
		t.Fatal("应有存活进程")
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	waitFor(t, "空闲时意外退出事件", func() bool { return len(h.obs.eventsOf(id, store.EventPiExited)) == 1 })
	ex := h.obs.eventsOf(id, store.EventPiExited)[0]
	if ex.run != nil {
		t.Fatalf("空闲时退出不应关联执行：%+v", ex)
	}
}

func TestRecordsEvicted(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 1 }, "")
	a, b := h.agent(nil), h.agent(nil)
	h.execRun(a, "a")
	h.execRun(b, "b")
	st := h.obs.eventsOf(a, store.EventPiStopped)
	if len(st) != 1 || st[0].ev.Data["reason"] != "evicted" || st[0].ev.Data["forAgentId"] != b.String() || st[0].run != nil {
		t.Fatalf("腾出名额应记录停止原因与目标 Agent：%+v", st)
	}
}

func TestRecordsAbortTimeout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.AbortTimeout = 500 * time.Millisecond }, "")
	id := h.agent(nil)
	runID := uuid.Must(uuid.NewV7())
	sink := newSink()
	done := make(chan struct{})
	go func() {
		_, _ = h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: "!sleep=10s !noabort"}, sink)
		close(done)
	}()
	<-sink.gotText
	_ = h.e.Interrupt(context.Background(), id, runID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("打断超时后应结束")
	}
	waitFor(t, "打断超时的停止事件", func() bool { return len(h.obs.eventsOf(id, store.EventPiStopped)) == 1 })
	st := h.obs.eventsOf(id, store.EventPiStopped)[0]
	if st.ev.Data["reason"] != "abort_timeout" || st.run == nil || *st.run != runID {
		t.Fatalf("打断超时的停止事件不对：%+v", st)
	}
	if len(h.obs.eventsOf(id, store.EventPiExited)) != 0 {
		t.Fatal("强制结束不应记为意外退出")
	}
}

func TestRecordsWorkspaceSyncFailure(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Sources = []store.FolderSource{
			{ID: uuid.Must(uuid.NewV7()), Path: "docs", Kind: "files"},
			{ID: uuid.Must(uuid.NewV7()), Path: "app", Kind: "git", GitURL: "file:///nonexistent/repo.git", GitBranch: "main"},
		}
	})
	runID, res := h.execRun(id, "hi")
	if res.ErrorCode != executor.ErrWorkspaceSyncFailed {
		t.Fatalf("应以工作区同步失败结束：%+v", res)
	}
	evs := h.obs.eventsOf(id, store.EventWorkspaceSync)
	if len(evs) != 1 || *evs[0].run != runID || evs[0].ev.Data["result"] != "failed" || !strings.Contains(evs[0].ev.Data["error"].(string), "来源 app") {
		t.Fatalf("同步失败事件不对：%+v", evs)
	}
	sources := evs[0].ev.Data["sources"].([]map[string]any)
	if len(sources) != 2 || sources[0]["outcome"] != "files_synced" || sources[1]["outcome"] != "failed" || sources[1]["path"] != "app" {
		t.Fatalf("各来源的处理结果不对：%+v", sources)
	}
	if len(h.obs.eventsOf(id, store.EventPiStarted)) != 0 {
		t.Fatal("同步失败时不应启动 Pi")
	}
}

func TestRecordsCLIInstall(t *testing.T) {
	// 只有其他平台安装包的模板：安装尝试以“没有当前平台的安装包”失败，并被记录。
	dir := t.TempDir()
	tmpl := `{"name":"zz","version":"1.0.0","injection":"env","fields":[],"credential":{"env":{}},"binary":"zz",
		"install":{"plan9/mips":{"url":"https://example.invalid/zz.zip","sha256":"` + strings.Repeat("0", 64) + `","format":"zip","path":"zz"}}}`
	if err := os.WriteFile(filepath.Join(dir, "zz.json"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil, dir)
	if _, ok := h.e.tmpl.Get("zz"); !ok {
		t.Skip("模板目录覆盖方式与预期不同，跳过")
	}
	id := h.agent(func(s *agentconfig.Snapshot) { s.CLIs = []string{"zz"} })
	runID, res := h.execRun(id, "hi")
	if res.ErrorCode != executor.ErrCLIInstallFailed {
		t.Fatalf("应以 CLI 安装失败结束：%+v", res)
	}
	evs := h.obs.eventsOf(id, store.EventCLIInstall)
	if len(evs) != 1 || *evs[0].run != runID || evs[0].ev.Data["result"] != "failed" || evs[0].ev.Data["cli"] != "zz" ||
		evs[0].ev.Data["version"] != "1.0.0" || !strings.Contains(evs[0].ev.Data["error"].(string), "当前平台") {
		t.Fatalf("安装失败事件不对：%+v", evs)
	}

	// 已安装的 CLI 不产生安装事件。
	preinstall(t, h.root, "gh", "2.100.0", "gh")
	h2 := h.agent(func(s *agentconfig.Snapshot) { s.CLIs = []string{"gh"} })
	h.execRun(h2, "hi")
	if evs := h.obs.eventsOf(h2, store.EventCLIInstall); len(evs) != 0 {
		t.Fatalf("已安装时不应记录安装：%+v", evs)
	}
}

// 打断只作用于指定的那一轮：A 已经结束、B 正在执行时，针对 A 的迟到打断不能影响 B；针对 B 的打断照常生效。
func TestInterruptTargetsOnlyGivenRun(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	ctx := context.Background()
	runA := uuid.Must(uuid.NewV7())
	if res, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: runA, Message: "第一轮"}, newSink()); err != nil || res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("A 应正常完成：%+v %v", res, err)
	}
	start := func(runID uuid.UUID, msg string) (*recSink, chan executor.Result) {
		sink, done := newSink(), make(chan executor.Result, 1)
		go func() {
			res, _ := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: runID, Message: msg}, sink)
			done <- res
		}()
		<-sink.gotText // 已经在输出文字，正处于两段文字之间的等待中
		return sink, done
	}
	wait := func(done chan executor.Result) executor.Result {
		select {
		case res := <-done:
			return res
		case <-time.After(10 * time.Second):
			t.Fatal("等待执行结束超时")
			return executor.Result{}
		}
	}
	runB := uuid.Must(uuid.NewV7())
	_, doneB := start(runB, "!sleep=300ms 第二轮")
	_ = h.e.Interrupt(ctx, id, runA) // 针对 A 的迟到打断
	if res := wait(doneB); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("针对 A 的打断不应作用到 B：%+v", res)
	}
	runC := uuid.Must(uuid.NewV7())
	_, doneC := start(runC, "!sleep=10s 第三轮")
	_ = h.e.Interrupt(ctx, id, runC)
	if res := wait(doneC); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("针对 C 的打断应生效：%+v", res)
	}
}
