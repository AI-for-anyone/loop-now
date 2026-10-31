package local

import (
	"context"
	"os"
	"path/filepath"
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

func TestConsoleStopIdleAndExportLifecycle(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_EXPORT_CONTENT") }, "")
	h.env["FAKEPI_EXPORT_CONTENT"] = "<html>exported session</html>"
	id := h.agent(nil)
	res, _ := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", res)
	}
	jsonl, err := h.e.ExportSession(context.Background(), id, "jsonl")
	if err != nil || jsonl.ContentType != "application/x-ndjson" || !strings.Contains(string(jsonl.Data), "echo: hello") {
		t.Fatalf("JSONL 导出不符：%+v %v", jsonl, err)
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

	html, err := h.e.ExportSession(context.Background(), id, "html")
	if err != nil || html.ContentType != "text/html; charset=utf-8" || string(html.Data) != "<html>exported session</html>" || filepath.Ext(html.Filename) != ".html" {
		t.Fatalf("HTML 导出不符：%+v %v", html, err)
	}
	waitFor(t, "导出启动的进程回收", func() bool { return h.pid(id) == 0 })
	leftovers, err := filepath.Glob(filepath.Join(h.e.dirs(id).sessions, ".loop-now-export-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("临时 HTML 应清理：%v %v", leftovers, err)
	}
}

func TestConsoleExportRejectsReturnedPathOutsideSessionDir(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_EXPORT_RETURN_PATH") }, "")
	h.env["FAKEPI_EXPORT_RETURN_PATH"] = outside
	id := h.agent(nil)
	res, _ := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("准备会话失败：%+v", res)
	}
	stopped, err := h.e.StopIdle(context.Background(), id, "test")
	if err != nil || !stopped {
		t.Fatalf("停止进程：%v %v", stopped, err)
	}
	waitFor(t, "进程退出", func() bool { return h.pid(id) == 0 })
	if _, err := h.e.ExportSession(context.Background(), id, "html"); err == nil || !strings.Contains(err.Error(), "不受管") {
		t.Fatalf("Pi 返回外部路径必须拒绝：%v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(h.e.dirs(id).sessions, ".loop-now-export-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("拒绝路径后临时文件仍应清理：%v %v", leftovers, err)
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
