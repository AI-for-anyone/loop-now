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
	if prepared, _ := h.exec(id, "Set up the session first"); prepared.Outcome != executor.OutcomeCompleted {
		t.Fatalf("preparing the session failed: %+v", prepared)
	}
	runID := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Origin: executor.OriginConsoleCompact, Message: "Keep the key decisions"}, newSink())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || *res.Compaction != (executor.CompactionResult{TokensBefore: 4200, EstimatedTokensAfter: 350}) {
		t.Fatalf("unexpected compaction result: %+v", res)
	}
	if data, err := os.ReadFile(capture); err != nil || string(data) != "Keep the key decisions" {
		t.Fatalf("unexpected customInstructions: %q %v", data, err)
	}
	usage := h.obs.usageOf(id)
	last := usage[len(usage)-1]
	if last.run == nil || *last.run != runID || last.u.Kind != pirpc.UsageCompaction || last.u.Input != 300 || last.u.Output != 40 {
		t.Fatalf("compaction usage should belong to the compact run: %+v", usage)
	}
}

// sessionFile is the session file currently recorded for the Agent (the one the executor wrote to the database).
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
		t.Fatalf("preparing the session failed: %+v", res)
	}
	session := h.sessionFile(id)
	jsonl, err := h.e.ExportSession(context.Background(), id, "pi", session, "jsonl")
	if err != nil || jsonl.ContentType != "application/x-ndjson" || !strings.Contains(string(jsonl.Data), "echo: hello") {
		t.Fatalf("unexpected JSONL export: %+v %v", jsonl, err)
	}
	pid := h.pid(id)
	// Even with an idle process, pi --export is used and that process is left alone.
	html, err := h.e.ExportSession(context.Background(), id, "pi", session, "html")
	if err != nil || html.ContentType != "text/html; charset=utf-8" || string(html.Data) != "<html>exported session</html>" || filepath.Ext(html.Filename) != ".html" {
		t.Fatalf("unexpected HTML export: %+v %v", html, err)
	}
	if h.pid(id) != pid || pid == 0 {
		t.Fatalf("export should not stop or replace the idle process: %d -> %d", pid, h.pid(id))
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	d := h.e.dirs(id)
	for _, want := range []string{"HOME=" + d.home, "PI_CODING_AGENT_DIR=" + d.pi, "PI_OFFLINE=1"} {
		if !strings.Contains(string(env), want) {
			t.Fatalf("the pi --export environment is missing %s: %s", want, env)
		}
	}
	stopped, err := h.e.StopIdle(context.Background(), id, "console_reload")
	if err != nil || !stopped {
		t.Fatalf("the existing idle process should be stopped: %v %v", stopped, err)
	}
	waitFor(t, "idle process exit", func() bool { return h.pid(id) == 0 })
	stopped, err = h.e.StopIdle(context.Background(), id, "console_reload")
	if err != nil || stopped {
		t.Fatalf("should return false when there is no process: %v %v", stopped, err)
	}
	// Without a process, export does not start the runtime either.
	if _, err := h.e.ExportSession(context.Background(), id, "pi", session, "html"); err != nil || h.pid(id) != 0 {
		t.Fatalf("export without a process: %v pid=%d", err, h.pid(id))
	}
	leftovers, err := filepath.Glob(filepath.Join(d.tmp, "loop-now-export-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("the temp HTML should be cleaned up: %v %v", leftovers, err)
	}
}

// Export only accepts session files in the sessions directory; a session file that no longer exists is ErrNoSession; a
// failing pi --export is reported as is and its temp files are cleaned up.
func TestConsoleExportChecksSessionFile(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_EXPORT_FAIL") }, "")
	id := h.agent(nil)
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("preparing the session failed: %+v", res)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.e.ExportSession(ctx, id, "pi", outside, "jsonl"); err == nil || !strings.Contains(err.Error(), "sessions") {
		t.Fatalf("a file outside the sessions directory must be rejected: %v", err)
	}
	gone := filepath.Join(h.e.dirs(id).sessions, "gone.jsonl")
	if _, err := h.e.ExportSession(ctx, id, "pi", gone, "html"); !errors.Is(err, executor.ErrNoSession) {
		t.Fatalf("a session file that is gone should be ErrNoSession: %v", err)
	}
	h.env["FAKEPI_EXPORT_FAIL"] = "1"
	if _, err := h.e.ExportSession(ctx, id, "pi", h.sessionFile(id), "html"); err == nil || !strings.Contains(err.Error(), "pi --export") {
		t.Fatalf("a failing pi --export should report an error: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(h.e.dirs(id).tmp, "loop-now-export-*.html"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files should still be cleaned up after a failure: %v", leftovers)
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
		t.Skipf("cannot create symlinks in this environment: %v", err)
	}
	sessions, err := h.e.ListSessions(context.Background(), id, "pi", older)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "session-new-full-id" || sessions[1].ID != "session-old-full-id" || sessions[0].Current || !sessions[1].Current {
		t.Fatalf("unexpected list, order or current mark: %+v", sessions)
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
		t.Fatalf("unexpected internal managed paths: %+v", sessions)
	}
}

func TestListSessionsExtractsUserTextMetadata(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	longAssistantText := strings.Repeat("abc", 70*1024)
	content := `{"type":"session","id":"metadata"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"/new"}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"Run ID for this turn: runtime-id.\n  First   user message\ncontinued"}]}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":` + strconv.Quote(longAssistantText) + `}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"image"},{"type":"text","text":"Last"},{"type":"text","text":"user message"}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"Text before the corrupt tail"}}` + "\n" +
		`{"type":"message"`
	if err := os.WriteFile(filepath.Join(d.sessions, "metadata.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := h.e.ListSessions(context.Background(), id, "pi", "")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("reading the session metadata failed: %+v %v", sessions, err)
	}
	if sessions[0].Title != "First user message continued" || sessions[0].Preview != "Text before the corrupt tail" {
		t.Fatalf("unexpected title or latest preview: %+v", sessions[0])
	}
}

func TestSessionUserTextTruncatesByRune(t *testing.T) {
	if got := truncateSessionText(strings.Repeat("界", 81), 80); got != strings.Repeat("界", 80)+"…" { // check-english: zh (rune-based truncation of CJK text)
		t.Fatalf("CJK truncation should not cut by bytes: %q", got)
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
	sessions, err := h.e.ListSessions(context.Background(), id, "pi", "")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("a duplicate ID should not make restore guess the target: %+v %v", sessions, err)
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
		t.Fatalf("a direct child file should be accepted: %q %v", got, err)
	}
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := managedSessionFile(d, outside); err == nil {
		t.Fatal("a path outside sessions should be rejected")
	}
	nested := filepath.Join(sessions, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := managedSessionFile(d, nested); err == nil {
		t.Fatal("a directory should not count as a session file")
	}
	link := filepath.Join(sessions, "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlinks in this environment: %v", err)
	}
	if _, err := managedSessionFile(d, link); err == nil {
		t.Fatal("a symlink pointing outside sessions should be rejected")
	}
}

func TestConsoleCommandsRespectClaudeRuntime(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("prepare the session: %+v", res)
	}
	ctx := context.Background()
	res, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: uuid.New(), Origin: executor.OriginConsoleCompact}, newSink())
	if err != nil || res.Outcome != executor.OutcomeFailed || !strings.Contains(res.ErrorMessage, "does not support Pi") {
		t.Fatalf("Pi compact should be rejected: %+v %v", res, err)
	}
	stopped, err := h.e.StopIdle(ctx, id, "console_reload")
	if err != nil || !stopped {
		t.Fatalf("the idle Claude process should be stoppable: %v %v", stopped, err)
	}
}

func TestSessionUserTextStripsCurrentAndLegacyRunID(t *testing.T) {
	for _, prefix := range []string{executor.RunIDLine(uuid.New()), "本轮运行ID：old。\n"} { // check-english: zh (the run ID line older sessions start with)
		if got := normalizeSessionUserText(prefix + "The real message"); got != "The real message" {
			t.Fatalf("the run ID should not reach the session title: %q", got)
		}
	}
}

// Expected failures in maintenance operations carry executor's sentinel errors, which the conversation service turns
// into a 409.
func TestMaintenanceErrorsCarrySentinels(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	ctx := context.Background()
	if _, err := h.e.ExportSession(ctx, id, "pi", "", "jsonl"); !errors.Is(err, executor.ErrNoSession) {
		t.Fatalf("should be ErrNoSession without a session: %v", err)
	}
	free, err := h.e.reserveMaintenance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer free()
	if _, err := h.e.StopIdle(ctx, id, "console_reload"); !errors.Is(err, executor.ErrMaintenanceBusy) || !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("StopIdle during maintenance should be ErrMaintenanceBusy: %v", err)
	}
	if _, err := h.e.ExportSession(ctx, id, "pi", "/x.jsonl", "jsonl"); !errors.Is(err, executor.ErrMaintenanceBusy) {
		t.Fatalf("export during maintenance should be ErrMaintenanceBusy: %v", err)
	}
	// Listing sessions only reads files, so it also works while maintenance is in progress.
	if _, err := h.e.ListSessions(ctx, id, "pi", ""); err != nil {
		t.Fatalf("listing sessions should not be affected by maintenance: %v", err)
	}
}

// StopIdle only accepts the stop reasons of platform commands, and the pi.stopped reasons it records are all in the
// contract's enum.
func TestStopIdleRejectsUnknownReason(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	if _, err := h.e.StopIdle(context.Background(), id, "console_command"); err == nil {
		t.Fatal("an unknown stop reason should be rejected")
	}
}

// When Pi does not compact (session too short, already compacted), the run completes normally and the result states
// the reason; other failures are still failures.
func TestConsoleCompactSkippedIsCompleted(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_COMPACT_ERROR") }, "")
	id := h.agent(nil)
	if prepared, _ := h.exec(id, "Set up the session first"); prepared.Outcome != executor.OutcomeCompleted {
		t.Fatalf("preparing the session failed: %+v", prepared)
	}
	compact := func(piError string) executor.Result {
		t.Helper()
		h.env["FAKEPI_COMPACT_ERROR"] = piError
		// Environment variables are read only when Pi starts: stop the idle process first for each case.
		if _, err := h.e.StopIdle(context.Background(), id, "console_reload"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "idle process exit", func() bool { return h.pid(id) == 0 })
		res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Origin: executor.OriginConsoleCompact}, newSink())
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := compact("Nothing to compact (session too small)"); res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || res.Compaction.Skipped != pirpc.CompactTooSmall {
		t.Fatalf("session too short: %+v", res)
	}
	if res := compact("Already compacted"); res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || res.Compaction.Skipped != pirpc.CompactAlreadyDone {
		t.Fatalf("already compacted: %+v", res)
	}
	if res := compact("Compaction failed: model unavailable"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrPiError {
		t.Fatalf("other failure: %+v", res)
	}
}

func userLine(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}})
	return string(b) + "\n"
}

// Long session: the title is read only forwards and the preview only backwards from the end, and the content in between
// (including corrupt lines) is not read; the cache is used while the file is unchanged and it is re-read when it
// changes.
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
	b.WriteString(userLine("First question"))
	for range 1500 {
		b.WriteString(filler)
	}
	b.WriteString("{corrupt line\n")
	for range 1500 {
		b.WriteString(filler)
	}
	b.WriteString(userLine("Latest question"))
	b.WriteString(filler)
	path := filepath.Join(d.sessions, "long.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	list := func() executor.ManagedSession {
		t.Helper()
		sessions, err := h.e.ListSessions(context.Background(), id, "pi", "")
		if err != nil || len(sessions) != 1 {
			t.Fatalf("list sessions: %+v %v", sessions, err)
		}
		return sessions[0]
	}
	if got := list(); got.Title != "First question" || got.Preview != "Latest question" {
		t.Fatalf("title and preview: %q %q", got.Title, got.Preview)
	}
	// Content changed but size and modification time did not: the cache is still used.
	info, _ := os.Stat(path)
	same := strings.Replace(b.String(), "Latest question", "Edited question", 1)
	if err := os.WriteFile(path, []byte(same), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := list(); got.Preview != "Latest question" {
		t.Fatalf("the cache should be used while the file is unchanged: %q", got.Preview)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(userLine("Newly appended question"))
	_ = f.Close()
	if got := list(); got.Preview != "Newly appended question" || got.Title != "First question" {
		t.Fatalf("should read again once the file changes: %q %q", got.Title, got.Preview)
	}
}

// Resolving by ID reads only the first line of each file; there is no result when the ID is duplicated or not found.
func TestResolveSessionByHeader(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string]string{"a.jsonl": "target", "b.jsonl": "dup", "c.jsonl": "dup"} {
		if err := os.WriteFile(filepath.Join(d.sessions, name), []byte(`{"type":"session","id":"`+header+`"}`+"\n{corrupt"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	got, err := h.e.ResolveSession(ctx, id, "pi", "target", "")
	if err != nil || got == nil || filepath.Base(got.Path) != "a.jsonl" {
		t.Fatalf("find the session by ID: %+v %v", got, err)
	}
	for _, missing := range []string{"dup", "unknown"} {
		if got, err := h.e.ResolveSession(ctx, id, "pi", missing, ""); err != nil || got != nil {
			t.Fatalf("%s should not be found: %+v %v", missing, got, err)
		}
	}
}
