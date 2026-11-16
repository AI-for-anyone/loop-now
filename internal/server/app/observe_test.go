package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/config"
	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

type timelineEntry struct {
	Type   string    `json:"type"`
	At     time.Time `json:"at"`
	Phase  string    `json:"phase"`
	Reason string    `json:"reason"`
	Kind   string    `json:"kind"`
	Result string    `json:"result"`
	CLI    string    `json:"cli"`
	Error  *string   `json:"error"`
	Total  int64     `json:"total"`
}

func (s *server) timeline(a string, runID string) []timelineEntry {
	s.t.Helper()
	tl := decode[struct {
		Entries []timelineEntry `json:"entries"`
	}](s.t, s.must(200, "GET", a+"/runs/"+runID+"/timeline", nil))
	return tl.Entries
}

func entryTypes(es []timelineEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Type
		if e.Type == "phase" {
			out[i] += ":" + e.Phase
		}
		if e.Type == "model_call" {
			out[i] += ":" + e.Kind
		}
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// Run records, usage and the anomaly list go through the full chain: executor -> recorder -> database -> API.
func TestObservabilityWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	a, ag := s.createAgent("observer")

	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=100,20,1000,0 !tool=bash hello"})
	s.waitIdle(a)
	first := s.replies(a)[0]
	if u := first.Run.Usage; u.Calls != 2 || u.Input != 200 || u.Output != 40 || u.CacheRead != 2000 || u.Total != 2240 {
		t.Fatalf("wrong usage total for the reply: %+v", u)
	}
	if m := first.Run.Model; m == nil || m.Provider != ag.Model.Provider || m.ModelID != ag.Model.ModelID {
		t.Fatalf("wrong model for the reply: %+v, want %+v", m, ag.Model)
	}
	if first.Run.RunningAt == nil || first.Run.StartedAt == nil {
		t.Fatalf("the reply is missing timestamps: %+v", first.Run)
	}
	types := entryTypes(s.timeline(a, first.Run.ID.String()))
	if types[0] != "dispatched" || types[len(types)-1] != "ended" {
		t.Fatalf("the timeline should start with dispatched and end with ended: %v", types)
	}
	order := []string{"dispatched", "phase:preparing", "workspace_sync", "phase:launching", "pi_started", "running", "model_call:assistant", "tool", "ended"}
	last := -1
	for _, want := range order {
		i := indexOf(types, want)
		if i < 0 || i < last {
			t.Fatalf("timeline entry missing or out of order: %s in %v", want, types)
		}
		last = i
	}

	s.must(202, "POST", a+"/messages", map[string]any{"text": "again"})
	s.waitIdle(a)
	second := s.replies(a)[1]
	if types := entryTypes(s.timeline(a, second.Run.ID.String())); indexOf(types, "pi_reused") < 0 || indexOf(types, "pi_started") >= 0 {
		t.Fatalf("the second reply should reuse the process: %v", types)
	}

	since := url.QueryEscape(time.Now().Add(-time.Hour).Format(time.RFC3339))
	usage := decode[apitypes.UsagePage](t, s.must(200, "GET", "/usage?from="+since, nil))
	if len(usage.Rows) != 1 || usage.Rows[0].Calls != 3 || usage.Rows[0].Total != 2240 || *usage.Rows[0].Model != ag.Model.ModelID {
		t.Fatalf("wrong usage stats: %+v", usage.Rows)
	}

	s.must(202, "POST", a+"/messages", map[string]any{"text": "!error=simulated_error"})
	s.waitIdle(a)
	list := decode[apitypes.AnomalyPage](t, s.must(200, "GET", "/anomalies?from="+since, nil))
	if list.Total != 1 || list.Items[0].Kind != "run_failed" || *list.Items[0].ErrorCode != "pi_error" || list.Items[0].AgentName != "observer" {
		t.Fatalf("wrong anomaly list: %+v", list)
	}
}

// Across a restart: CLI install failures stay visible; a reply in progress shows up in the anomaly list as interrupted
// by a server restart.
func TestObservabilityAcrossRestart(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not a zip")) }))
	defer bad.Close()
	dir := t.TempDir()
	tmpl := fmt.Sprintf(`{"name":"zz","version":"1.0.0","injection":"env","fields":[],"credential":{"env":{}},"binary":"zz",
		"install":{"%s/%s":{"url":"%s/zz.zip","sha256":"%s","format":"zip","path":"zz"}}}`, runtime.GOOS, runtime.GOARCH, bad.URL, strings.Repeat("0", 64))
	if err := os.WriteFile(filepath.Join(dir, "zz.json"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	w.cfg.CLITemplateDir = dir
	s := w.start(nil)

	a, _ := s.createAgent("CLI installer")
	s.must(200, "PUT", a+"/clis/zz", nil)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "hi"})
	s.waitIdle(a)
	failed := s.replies(a)[0]
	if failed.Run.ErrorCode == nil || *failed.Run.ErrorCode != "cli_install_failed" {
		t.Fatalf("should end with a CLI install failure: %+v", failed.Run)
	}
	var install *timelineEntry
	for _, e := range s.timeline(a, failed.Run.ID.String()) {
		if e.Type == "cli_install" {
			install = &e
		}
	}
	if install == nil || install.Result != "failed" || install.CLI != "zz" || install.Error == nil || !strings.Contains(*install.Error, "checksum mismatch") {
		t.Fatalf("the timeline should have the CLI install failure: %+v", install)
	}

	b, _ := s.createAgent("long reply")
	s.must(202, "POST", b+"/messages", map[string]any{"text": "!sleep=10s a very long reply"})
	s.waitRunning(b)
	s.stop()

	s2 := w.start(nil)
	defer s2.stop()
	tmpls := decode[struct {
		Templates []apitypes.CliTemplate `json:"templates"`
	}](t, s2.must(200, "GET", "/cli-templates", nil))
	found := false
	for _, tp := range tmpls.Templates {
		if tp.Name == "zz" {
			found = true
			if tp.Install.Status != "failed" || tp.Install.Error == nil || !strings.Contains(*tp.Install.Error, "checksum mismatch") {
				t.Fatalf("after the restart the install failure and its reason should still show: %+v", tp.Install)
			}
		}
	}
	if !found {
		t.Fatal("zz is not in the template directory")
	}
	list := decode[apitypes.AnomalyPage](t, s2.must(200, "GET", "/anomalies", nil))
	kinds := map[string]int{}
	for _, it := range list.Items {
		kinds[it.Kind]++
	}
	if kinds["server_restart"] != 1 || kinds["run_failed"] != 1 {
		t.Fatalf("the anomaly list should have one restart interruption and one install failure: %+v", list.Items)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Writing to the database fails while recording a Pi process start event: the reply completes as usual and the log has
// a warning.
func TestRecordFailureDoesNotAffectRun(t *testing.T) {
	w := newWorld(t)
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	st, err := store.Open(context.Background(), db.Options{Dir: config.DBDir(w.cfg.DataDir)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Pool.Exec(context.Background(), `CREATE TRIGGER ev.fail_pi_started BEFORE INSERT ON lifecycle_events
		WHEN NEW.type = 'pi.started' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	a, _ := s.createAgent("record failure")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "hello"})
	s.waitIdle(a)
	if r := s.replies(a); len(r) != 1 || r[0].Run.Status != "completed" {
		t.Fatalf("a record failure should not affect the reply: %+v", r)
	}
	if !strings.Contains(logs.String(), "failed to write the run record") || !strings.Contains(logs.String(), "pi.started") {
		t.Fatalf("the log should have a record failure warning:\n%s", logs.String())
	}
}

type logLine map[string]any

func parseLogs(t *testing.T, raw string) []logLine {
	t.Helper()
	var out []logLine
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		if l == "" {
			continue
		}
		var m logLine
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("every JSON log line should parse on its own: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

// Server logs: one info line at the start and one at the end of each reply, all with runId and agentId; API errors
// carry requestId; no message bodies; no log files are written.
func TestServerLogs(t *testing.T) {
	w := newWorld(t)
	w.cfg.LogFormat = "json"
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	a, ag := s.createAgent("logger")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "warm-up"})
	s.waitIdle(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=10,5 !tool=bash !tool=bash !tool=bash secret-body-xyz"})
	s.waitIdle(a)
	run := s.replies(a)[1].Run

	lines := parseLogs(t, logs.String())
	var info []logLine
	for _, l := range lines {
		if l["level"] == "INFO" && l["runId"] == run.ID.String() {
			info = append(info, l)
		}
	}
	if len(info) != 2 || info[0]["msg"] != "reply started" || info[1]["msg"] != "reply ended" {
		t.Fatalf("this reply should have only a start line and an end line at info level: %v", info)
	}
	end := info[1]
	if end["agentId"] != ag.ID.String() || end["status"] != "completed" || end["tokens"] != float64(30) {
		t.Fatalf("wrong end log: %v", end)
	}
	if _, ok := end["durationMs"]; !ok {
		t.Fatalf("the end log should carry the total duration: %v", end)
	}
	if strings.Contains(logs.String(), "secret-body-xyz") {
		t.Fatal("message bodies should not appear in the log")
	}

	// A failed reply: the end log carries the error code.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!error=simulated_failure again"})
	s.waitIdle(a)
	failed := s.replies(a)[2].Run
	found := false
	for _, l := range parseLogs(t, logs.String()) {
		if l["msg"] == "reply ended" && l["runId"] == failed.ID.String() {
			found = l["status"] == "failed" && l["errorCode"] == "pi_error"
		}
	}
	if !found {
		t.Fatalf("the end log of a failed reply should carry the error code: %+v", failed)
	}

	// Internal API error: the error log carries the request ID, matching the response header.
	st, err := store.Open(context.Background(), db.Options{Dir: config.DBDir(w.cfg.DataDir)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Pool.Exec(context.Background(), `ALTER TABLE usage_records RENAME TO usage_records_hidden`); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", s.url+"/usage", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("should return 500: %d", resp.StatusCode)
	}
	rid := resp.Header.Get("Loop-Now-Request-Id")
	ok := false
	for _, l := range parseLogs(t, logs.String()) {
		if l["level"] == "ERROR" && l["requestId"] == rid {
			ok = true
		}
	}
	if !ok || rid == "" {
		t.Fatalf("the API error log should carry request ID %q", rid)
	}
	if _, err := st.Pool.Exec(context.Background(), `ALTER TABLE usage_records_hidden RENAME TO usage_records`); err != nil {
		t.Fatal(err)
	}

	// No log files are written.
	_ = filepath.WalkDir(w.cfg.WorkRoot, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".log") {
			t.Errorf("the server should not write log files: %s", p)
		}
		return nil
	})
}

// firstOverviewEvent connects to the overview event stream (optionally with Last-Event-ID) and returns the first
// event.
func (s *server) firstOverviewEvent(lastID string) (event string, data []byte) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.url+"/overview/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			return event, []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	s.t.Fatal("no overview event received")
	return "", nil
}

// 8.1: restart after a forced kill: leftover replies are recorded as interrupted by a server restart and show up in the
// anomaly list; the overview snapshot shows the correct status.
func TestRecoveryCrashRecords(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, ag := s.createAgent("crash records")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s very long"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "queued", "whenBusy": "queue"})
	_ = s.a.Conv.Stop(context.Background()) // simulate a forced kill: no graceful exit
	s.a.ReleaseInstanceLocks()
	s2 := w.start(nil)
	defer s2.stop()
	defer func() { _ = s.a.Executor.Shutdown(context.Background()) }()
	s2.waitIdle(a)
	r := s2.replies(a)
	if len(r) != 2 || *r[0].Run.EndReason != "server_restart" || r[1].Run.Status != "completed" {
		t.Fatalf("wrong replies after the restart: %+v", r)
	}
	es := s2.timeline(a, r[0].Run.ID.String())
	if last := es[len(es)-1]; last.Type != "ended" {
		t.Fatalf("a reply interrupted by a restart should end with an ended entry: %+v", es)
	}
	list := decode[apitypes.AnomalyPage](t, s2.must(200, "GET", "/anomalies", nil))
	if list.Total != 1 || list.Items[0].Kind != "server_restart" || list.Items[0].RunID != r[0].Run.ID {
		t.Fatalf("the anomaly list should have one server restart interruption: %+v", list)
	}
	ev, data := s2.firstOverviewEvent("")
	snap := decode[apitypes.SseOverviewSnapshot](t, data)
	if ev != "snapshot" || len(snap.Agents) != 1 || snap.Agents[0].AgentID != ag.ID || snap.Agents[0].Status.Busy ||
		snap.Agents[0].Status.Queued.Normal != 0 || snap.Agents[0].LastRun == nil || snap.Agents[0].LastRun.Status != "completed" {
		t.Fatalf("wrong overview snapshot after the restart: %s", data)
	}
}

// 8.2: the client reconnects with a position not in the event stream (for example, the server restarted and the events
// were trimmed): it gets a snapshot, and run records and usage in the database are not affected.
func TestRecoveryUnknownStreamPosition(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	a, _ := s.createAgent("stale position")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=10,5 hello"})
	s.waitIdle(a)
	_, data := s.firstOverviewEvent("")
	_ = data
	before := s.replies(a)[0]
	if ev, _ := s.firstOverviewEvent("9999999999999-0"); ev != "snapshot" {
		t.Fatalf("reconnecting after the event stream is lost should push a snapshot: %s", ev)
	}
	after := s.replies(a)[0]
	if after.Run.Usage != before.Run.Usage || len(s.timeline(a, after.Run.ID.String())) == 0 {
		t.Fatalf("reconnecting should not affect run records and usage: %+v %+v", before.Run.Usage, after.Run.Usage)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "continue"})
	s.waitIdle(a)
}

// 8.3: forced kill after an interrupt timeout: the stop reason is recorded, the reply ends as interrupted, and the
// timeline ends with a forced-kill entry and an end entry.
func TestRecoveryAbortTimeout(t *testing.T) {
	w := newWorld(t)
	s := w.start(func(c *config.Config) { c.AbortTimeout = time.Second })
	defer s.stop()
	a, _ := s.createAgent("abort timeout")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=10s !noabort very long"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "interrupt", "whenBusy": "interrupt"})
	s.waitIdle(a)
	first := s.replies(a)[0]
	if first.Run.Status != "interrupted" || first.Run.AbortRequestedAt == nil {
		t.Fatalf("should end as interrupted and record when the interrupt was requested: %+v", first.Run)
	}
	types := entryTypes(s.timeline(a, first.Run.ID.String()))
	ia, is, ie := indexOf(types, "abort_requested"), indexOf(types, "pi_stopped"), indexOf(types, "ended")
	if ia < 0 || is < ia || ie != len(types)-1 || indexOf(types, "pi_exited") >= 0 {
		t.Fatalf("the timeline should have abort requested, forced stop and ended in order: %v", types)
	}
	if indexOf(types, "model_call:assistant") >= 0 {
		t.Fatalf("the call that was force-stopped has no usage report: %v", types)
	}
}
