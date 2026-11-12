package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
)

func TestConsoleCompactUsesRealRunAndPiResult(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "instructions.txt")
	h := newHarness(t, func(o *Options) {
		o.Passthrough = append(o.Passthrough, "FAKEPI_COMPACT_CAPTURE", "FAKEPI_COMPACT_TOKENS", "FAKEPI_COMPACT_USAGE")
	}, "")
	h.env["FAKEPI_COMPACT_CAPTURE"] = capture
	h.env["FAKEPI_COMPACT_TOKENS"] = "4200,350"
	h.env["FAKEPI_COMPACT_USAGE"] = "300,40"
	id := h.agent(nil)
	if prepared, _ := h.exec(id, "先建立会话"); prepared.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", prepared)
	}
	runID := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Origin: executor.OriginConsoleCompact, Message: "保留关键决定"}, newSink())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || *res.Compaction != (executor.CompactionResult{TokensBefore: 4200, EstimatedTokensAfter: 350}) {
		t.Fatalf("压缩结果不符：%+v", res)
	}
	if data, err := os.ReadFile(capture); err != nil || string(data) != "保留关键决定" {
		t.Fatalf("customInstructions 不符：%q %v", data, err)
	}
	usage := h.obs.usageOf(id)
	last := usage[len(usage)-1]
	if last.run == nil || *last.run != runID || last.u.Kind != pirpc.UsageCompaction || last.u.Input != 300 || last.u.Output != 40 {
		t.Fatalf("压缩用量应归属 compact run：%+v", usage)
	}
}

// sessionFile 是 Agent 当前记下的会话文件（执行器写给数据库的那份）。
func (h *harness) sessionFile(id uuid.UUID) string {
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	return h.src.recorded[id]
}

func TestConsoleStopIdleAndExportLifecycle(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "export-env")
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_EXPORT_CONTENT", "FAKEPI_EXPORT_ENV") }, "")
	h.env["FAKEPI_EXPORT_CONTENT"] = "<html>exported session</html>"
	h.env["FAKEPI_EXPORT_ENV"] = envFile
	id := h.agent(nil)
	res, _ := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", res)
	}
	session := h.sessionFile(id)
	jsonl, err := h.e.ExportSession(context.Background(), id, session, "jsonl")
	if err != nil || jsonl.ContentType != "application/x-ndjson" || !strings.Contains(string(jsonl.Data), "echo: hello") {
		t.Fatalf("JSONL 导出不符：%+v %v", jsonl, err)
	}
	pid := h.pid(id)
	// 有空闲进程时也用 pi --export，不动那个进程。
	html, err := h.e.ExportSession(context.Background(), id, session, "html")
	if err != nil || html.ContentType != "text/html; charset=utf-8" || string(html.Data) != "<html>exported session</html>" || filepath.Ext(html.Filename) != ".html" {
		t.Fatalf("HTML 导出不符：%+v %v", html, err)
	}
	if h.pid(id) != pid || pid == 0 {
		t.Fatalf("导出不应停止或替换空闲进程：%d → %d", pid, h.pid(id))
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	d := h.e.dirs(id)
	for _, want := range []string{"HOME=" + d.home, "PI_CODING_AGENT_DIR=" + d.pi, "PI_OFFLINE=1"} {
		if !strings.Contains(string(env), want) {
			t.Fatalf("pi --export 的环境缺少 %s：%s", want, env)
		}
	}
	stopped, err := h.e.StopIdle(context.Background(), id, "console_reload")
	if err != nil || !stopped {
		t.Fatalf("应停止已有空闲进程：%v %v", stopped, err)
	}
	waitFor(t, "空闲进程退出", func() bool { return h.pid(id) == 0 })
	stopped, err = h.e.StopIdle(context.Background(), id, "console_reload")
	if err != nil || stopped {
		t.Fatalf("没有进程时应返回 false：%v %v", stopped, err)
	}
	// 没有进程时导出也不启动运行时。
	if _, err := h.e.ExportSession(context.Background(), id, session, "html"); err != nil || h.pid(id) != 0 {
		t.Fatalf("没有进程时导出：%v pid=%d", err, h.pid(id))
	}
	leftovers, err := filepath.Glob(filepath.Join(d.tmp, "loop-now-export-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("临时 HTML 应清理：%v %v", leftovers, err)
	}
}

// 导出只认 sessions 目录里的会话文件；会话文件不在了是 ErrNoSession；pi --export 失败如实报错并清理临时文件。
func TestConsoleExportChecksSessionFile(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_EXPORT_FAIL") }, "")
	id := h.agent(nil)
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", res)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.e.ExportSession(ctx, id, outside, "jsonl"); err == nil || !strings.Contains(err.Error(), "sessions") {
		t.Fatalf("sessions 目录外的文件必须拒绝：%v", err)
	}
	gone := filepath.Join(h.e.dirs(id).sessions, "gone.jsonl")
	if _, err := h.e.ExportSession(ctx, id, gone, "html"); !errors.Is(err, executor.ErrNoSession) {
		t.Fatalf("会话文件不在了应是 ErrNoSession：%v", err)
	}
	h.env["FAKEPI_EXPORT_FAIL"] = "1"
	if _, err := h.e.ExportSession(ctx, id, h.sessionFile(id), "html"); err == nil || !strings.Contains(err.Error(), "pi --export") {
		t.Fatalf("pi --export 失败应报错：%v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(h.e.dirs(id).tmp, "loop-now-export-*.html"))
	if len(leftovers) != 0 {
		t.Fatalf("失败后临时文件仍应清理：%v", leftovers)
	}
}

func TestListSessionsValidatesHeadersSortsAndMarksCurrent(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, header string, modified time.Time) string {
		t.Helper()
		path := filepath.Join(d.sessions, name)
		if err := os.WriteFile(path, []byte(header+"\n{\"type\":\"message\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		return path
	}
	now := time.Now()
	older := write("older.jsonl", `{"type":"session","id":"session-old-full-id"}`, now.Add(-time.Hour))
	newer := write("newer.jsonl", `{"type":"session","id":"session-new-full-id"}`, now)
	write("bad-header.jsonl", `{"type":"message","id":"not-a-session"}`, now.Add(time.Hour))
	write("empty-id.jsonl", `{"type":"session","id":""}`, now.Add(time.Hour))
	write("wrong-extension.json", `{"type":"session","id":"wrong-extension"}`, now.Add(time.Hour))
	nested := filepath.Join(d.sessions, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested.jsonl"), []byte(`{"type":"session","id":"nested"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session","id":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d.sessions, "escape.jsonl")); err != nil {
		t.Skipf("当前环境不能创建软链接：%v", err)
	}
	sessions, err := h.e.ListSessions(context.Background(), id, older)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "session-new-full-id" || sessions[1].ID != "session-old-full-id" || sessions[0].Current || !sessions[1].Current {
		t.Fatalf("列表、排序或当前标记不符：%+v", sessions)
	}
	resolvedNewer, err := filepath.EvalSymlinks(newer)
	if err != nil {
		t.Fatal(err)
	}
	resolvedOlder, err := filepath.EvalSymlinks(older)
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Path != resolvedNewer || sessions[1].Path != resolvedOlder {
		t.Fatalf("内部受管路径不符：%+v", sessions)
	}
}

func TestListSessionsExtractsUserTextMetadata(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	longAssistantText := strings.Repeat("长", 70*1024)
	content := `{"type":"session","id":"metadata"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"/new"}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"本轮运行ID：runtime-id\n  第一条   用户消息\n继续"}]}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":` + strconv.Quote(longAssistantText) + `}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"image"},{"type":"text","text":"最后一条"},{"type":"text","text":"用户消息"}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"被后续损坏截断前的文字"}}` + "\n" +
		`{"type":"message"`
	if err := os.WriteFile(filepath.Join(d.sessions, "metadata.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := h.e.ListSessions(context.Background(), id, "")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("读取会话元数据失败：%+v %v", sessions, err)
	}
	if sessions[0].Title != "第一条 用户消息 继续" || sessions[0].Preview != "被后续损坏截断前的文字" {
		t.Fatalf("标题或最近摘要不符：%+v", sessions[0])
	}
}

func TestSessionUserTextTruncatesByRune(t *testing.T) {
	if got := truncateSessionText(strings.Repeat("界", 81), 80); got != strings.Repeat("界", 80)+"…" {
		t.Fatalf("中文截断不应按字节切断：%q", got)
	}
}

func TestListSessionsRejectsDuplicateIDs(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(d.sessions, name), []byte(`{"type":"session","id":"duplicate"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := h.e.ListSessions(context.Background(), id, "")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("重复 ID 不应让恢复猜测目标：%+v %v", sessions, err)
	}
}

func TestManagedSessionFileRejectsEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	d := agentDirs{sessions: sessions}
	file := filepath.Join(sessions, "session.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := managedSessionFile(d, file); err != nil || got != resolved {
		t.Fatalf("直接子文件应接受：%q %v", got, err)
	}
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := managedSessionFile(d, outside); err == nil {
		t.Fatal("sessions 外路径应拒绝")
	}
	nested := filepath.Join(sessions, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := managedSessionFile(d, nested); err == nil {
		t.Fatal("目录不应作为会话文件")
	}
	link := filepath.Join(sessions, "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前环境不能创建软链接：%v", err)
	}
	if _, err := managedSessionFile(d, link); err == nil {
		t.Fatal("指向 sessions 外的软链接应拒绝")
	}
}

func TestConsoleCommandsRespectClaudeRuntime(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话：%+v", res)
	}
	ctx := context.Background()
	res, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: uuid.New(), Origin: executor.OriginConsoleCompact}, newSink())
	if err != nil || res.Outcome != executor.OutcomeFailed || !strings.Contains(res.ErrorMessage, "不支持 Pi") {
		t.Fatalf("应拒绝 Pi compact：%+v %v", res, err)
	}
	stopped, err := h.e.StopIdle(ctx, id, "console_reload")
	if err != nil || !stopped {
		t.Fatalf("应能停止 Claude 空闲进程：%v %v", stopped, err)
	}
}

func TestSessionUserTextStripsCurrentAndLegacyRunID(t *testing.T) {
	for _, prefix := range []string{executor.RunIDLine(uuid.New()), "本轮运行ID：old。\n"} {
		if got := normalizeSessionUserText(prefix + "真正的消息"); got != "真正的消息" {
			t.Fatalf("运行 ID 不应进入会话标题：%q", got)
		}
	}
}

// 维护操作里可以预期的失败带 executor 的哨兵错误，对话服务据此返回 409。
func TestMaintenanceErrorsCarrySentinels(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	ctx := context.Background()
	if _, err := h.e.ExportSession(ctx, id, "", "jsonl"); !errors.Is(err, executor.ErrNoSession) {
		t.Fatalf("没有会话时应是 ErrNoSession：%v", err)
	}
	free, err := h.e.reserveMaintenance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer free()
	if _, err := h.e.StopIdle(ctx, id, "console_reload"); !errors.Is(err, executor.ErrMaintenanceBusy) || !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("维护进行中 StopIdle 应是 ErrMaintenanceBusy：%v", err)
	}
	if _, err := h.e.ExportSession(ctx, id, "/x.jsonl", "jsonl"); !errors.Is(err, executor.ErrMaintenanceBusy) {
		t.Fatalf("维护进行中导出应是 ErrMaintenanceBusy：%v", err)
	}
	// 列出会话只读文件，维护进行中也可以。
	if _, err := h.e.ListSessions(ctx, id, ""); err != nil {
		t.Fatalf("列出会话不受维护影响：%v", err)
	}
}

// StopIdle 只接受平台命令的停止原因，记下的 pi.stopped 原因都在契约的枚举里。
func TestStopIdleRejectsUnknownReason(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	if _, err := h.e.StopIdle(context.Background(), id, "console_command"); err == nil {
		t.Fatal("未知的停止原因应被拒绝")
	}
}

// Pi 没有压缩（会话太短、已经压缩过）时这次执行正常完成，结果里写明原因；其余失败仍是失败。
func TestConsoleCompactSkippedIsCompleted(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_COMPACT_ERROR") }, "")
	id := h.agent(nil)
	if prepared, _ := h.exec(id, "先建立会话"); prepared.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", prepared)
	}
	compact := func(piError string) executor.Result {
		t.Helper()
		h.env["FAKEPI_COMPACT_ERROR"] = piError
		// 环境变量只在 Pi 启动时读：每种情况先停掉空闲进程。
		if _, err := h.e.StopIdle(context.Background(), id, "console_reload"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "空闲进程退出", func() bool { return h.pid(id) == 0 })
		res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Origin: executor.OriginConsoleCompact}, newSink())
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := compact("Nothing to compact (session too small)"); res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || res.Compaction.Skipped != pirpc.CompactTooSmall {
		t.Fatalf("会话太短：%+v", res)
	}
	if res := compact("Already compacted"); res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || res.Compaction.Skipped != pirpc.CompactAlreadyDone {
		t.Fatalf("已经压缩过：%+v", res)
	}
	if res := compact("Compaction failed: model unavailable"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrPiError {
		t.Fatalf("其他失败：%+v", res)
	}
}

func userLine(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}})
	return string(b) + "\n"
}

// 长会话：标题只向前读、预览只从末尾往回读，中间的内容（包括损坏的行）不读；文件没变时用缓存，变了重新读。
func TestListSessionsReadsOnlyHeadAndTail(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	filler := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("x", 1000) + `"}]}}` + "\n"
	var b strings.Builder
	b.WriteString(`{"type":"session","id":"long-session"}` + "\n")
	b.WriteString(userLine("第一条问题"))
	for range 1500 {
		b.WriteString(filler)
	}
	b.WriteString("{损坏的行\n")
	for range 1500 {
		b.WriteString(filler)
	}
	b.WriteString(userLine("最后一条问题"))
	b.WriteString(filler)
	path := filepath.Join(d.sessions, "long.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	list := func() executor.ManagedSession {
		t.Helper()
		sessions, err := h.e.ListSessions(context.Background(), id, "")
		if err != nil || len(sessions) != 1 {
			t.Fatalf("列出会话：%+v %v", sessions, err)
		}
		return sessions[0]
	}
	if got := list(); got.Title != "第一条问题" || got.Preview != "最后一条问题" {
		t.Fatalf("标题与预览：%q %q", got.Title, got.Preview)
	}
	// 内容换了但大小与修改时间不变：仍用缓存。
	info, _ := os.Stat(path)
	same := strings.Replace(b.String(), "最后一条问题", "改过的问题题", 1)
	if err := os.WriteFile(path, []byte(same), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := list(); got.Preview != "最后一条问题" {
		t.Fatalf("文件没变时应用缓存：%q", got.Preview)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(userLine("新追加的问题"))
	_ = f.Close()
	if got := list(); got.Preview != "新追加的问题" || got.Title != "第一条问题" {
		t.Fatalf("文件变了应重新读：%q %q", got.Title, got.Preview)
	}
}

// 按 ID 恢复只读各文件的头一行；ID 重复或找不到时没有结果。
func TestResolveSessionByHeader(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string]string{"a.jsonl": "target", "b.jsonl": "dup", "c.jsonl": "dup"} {
		if err := os.WriteFile(filepath.Join(d.sessions, name), []byte(`{"type":"session","id":"`+header+`"}`+"\n{损坏"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	got, err := h.e.ResolveSession(ctx, id, "target", "")
	if err != nil || got == nil || filepath.Base(got.Path) != "a.jsonl" {
		t.Fatalf("按 ID 找会话：%+v %v", got, err)
	}
	for _, missing := range []string{"dup", "unknown"} {
		if got, err := h.e.ResolveSession(ctx, id, missing, ""); err != nil || got != nil {
			t.Fatalf("%s 不应找到：%+v %v", missing, got, err)
		}
	}
}
