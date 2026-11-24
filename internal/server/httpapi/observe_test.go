package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/store"
)

func (e *env) newAgent(name string) (apitypes.Agent, string) {
	e.t.Helper()
	agent := decode[apitypes.Agent](e.t, e.expect(201, "POST", e.agents(), map[string]any{"name": name, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	return agent, "/agents/" + agent.ID.String()
}

func (e *env) lastReply(a string) apitypes.Message {
	e.t.Helper()
	page := decode[apitypes.MessagePage](e.t, e.expect(200, "GET", a+"/messages?limit=200", nil))
	for i := len(page.Messages) - 1; i >= 0; i-- {
		if page.Messages[i].Role == "assistant" {
			return page.Messages[i]
		}
	}
	e.t.Fatal("no reply")
	return apitypes.Message{}
}

// Insert one of every run record event and usage; the timeline endpoint's response must conform to the contract.
func TestRunTimelineContract(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, a := e.newAgent("timeline")
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "Hello"})
	waitIdle(t, e, a)
	reply := e.lastReply(a)
	runID := reply.Run.ID

	at := time.Now()
	add := func(typ string, data map[string]any) {
		at = at.Add(time.Millisecond)
		if err := e.st.AddEvent(ctx, store.LifecycleEvent{AgentID: agent.ID, RunID: &runID, Type: typ, At: at, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	add(store.EventCLIInstall, map[string]any{"cli": "gh", "version": "2.100.0", "result": "failed", "durationMs": 12, "error": "digest mismatch"})
	add(store.EventWorkspaceSync, map[string]any{"result": "succeeded", "durationMs": 30, "sources": []map[string]any{
		{"path": "app", "kind": "git", "outcome": "cloned"}, {"path": "docs", "kind": "files", "outcome": "files_synced", "filesWritten": 2, "filesDeleted": 1}}})
	add(store.EventPiStarted, map[string]any{"pid": 123, "configVersion": 1, "startupMs": 800, "reason": "no_process"})
	add(store.EventPiReused, map[string]any{"pid": 123, "startedAt": time.Now().UTC()})
	add(store.EventModelRetryStarted, map[string]any{"attempt": 1, "maxAttempts": 3, "delayMs": 2000, "errorMessage": "529 overloaded"})
	add(store.EventModelRetryEnded, map[string]any{"success": true, "attempt": 1})
	add(store.EventContextCompaction, map[string]any{"reason": "threshold", "result": "succeeded", "tokensBefore": 150000, "estimatedTokensAfter": 32000, "durationMs": 900})
	add(store.EventSessionMissing, map[string]any{"sessionFile": "/tmp/s.jsonl", "epoch": 1})
	add(store.EventContextHandoff, map[string]any{"fromRuntime": "pi", "toRuntime": "claude-code", "includedMessages": 4, "omittedMessages": 2,
		"images": 1, "bytes": 900, "budgetBytes": 16000})
	add(store.EventPiStopped, map[string]any{"pid": 123, "reason": "abort_timeout", "uptimeMs": 5000})
	add(store.EventPiExited, map[string]any{"pid": 124, "uptimeMs": 10, "exit": "exit status 3", "stderrTail": "boom"})
	add(store.EventRunAbortRequested, map[string]any{"byRunId": uuid.Must(uuid.NewV7()).String()})
	reasoning, dur := int64(5), int64(1200)
	for _, u := range []store.UsageRecord{
		{Kind: store.UsageAssistant, Provider: "anthropic", Model: "claude-x", ResponseModel: "claude-x-2026", StopReason: "toolUse", Input: 100, Output: 20, CacheRead: 1000, Reasoning: &reasoning, DurationMs: &dur},
		{Kind: store.UsageTool, Input: 3, Output: 1},
		{Kind: store.UsageCompaction, Provider: "anthropic", Model: "claude-x", Input: 500, Output: 50},
		{Kind: store.UsageCacheWarm, Provider: "anthropic", Model: "claude-x", CacheRead: 2000},
	} {
		u.AgentID, u.RunID = agent.ID, &runID
		if err := e.st.AddUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	tl := decode[struct {
		Run     apitypes.Run      `json:"run"`
		Entries []json.RawMessage `json:"entries"`
		Usage   apitypes.UsageTotals
	}](t, e.expect(200, "GET", a+"/runs/"+runID.String()+"/timeline", nil))
	seen := map[string]int{}
	var order []string
	for _, raw := range tl.Entries {
		var head struct {
			Type string    `json:"type"`
			At   time.Time `json:"at"`
		}
		_ = json.Unmarshal(raw, &head)
		seen[head.Type]++
		order = append(order, head.Type)
	}
	for _, want := range []string{"dispatched", "phase", "running", "tool", "ended", "cli_install", "workspace_sync", "pi_started", "pi_reused",
		"model_retry_started", "model_retry_ended", "compaction", "session_missing", "context_handoff", "pi_stopped", "pi_exited", "abort_requested", "model_call"} {
		if seen[want] == 0 {
			t.Errorf("timeline is missing entry %s: %v", want, order)
		}
	}
	if seen["model_call"] != 4 || seen["tool"] != 1 {
		t.Errorf("wrong number of usage and tool entries: %v", seen)
	}
	if order[0] != "dispatched" {
		t.Errorf("the first entry should be dispatched: %v", order)
	}
	if tl.Usage.Calls != 4 || tl.Usage.Total != 100+20+1000+3+1+500+50+2000 {
		t.Errorf("wrong usage totals: %+v", tl.Usage)
	}
	if strings.Contains(string(e.expect(200, "GET", a+"/runs/"+runID.String()+"/timeline", nil)), "cost") {
		t.Error("the timeline should not contain costs")
	}

	// The run info of a reply carries timestamps, usage totals and the model used.
	run := e.lastReply(a).Run
	if run.RunningAt == nil || run.StartedAt == nil || run.Usage.Calls != 4 || run.Model == nil || run.Model.ModelID != "claude-x" {
		t.Errorf("wrong run info: %+v", run)
	}

	// Reply not belonging to the Agent, Agent missing: 404.
	_, b := e.newAgent("other")
	e.expect(404, "GET", b+"/runs/"+runID.String()+"/timeline", nil)
	e.expect(404, "GET", "/agents/"+uuid.Must(uuid.NewV7()).String()+"/runs/"+runID.String()+"/timeline", nil)
}

// Runs from before this change have no lifecycle events: entries are synthesized from the columns of runs.
func TestRunTimelineLegacy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, a := e.newAgent("legacy")
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "Hello"})
	waitIdle(t, e, a)
	runID := e.lastReply(a).Run.ID
	if _, err := e.st.Pool.Exec(ctx, `DELETE FROM lifecycle_events WHERE run_id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	tl := decode[struct {
		Entries []struct {
			Type string `json:"type"`
		} `json:"entries"`
	}](t, e.expect(200, "GET", a+"/runs/"+runID.String()+"/timeline", nil))
	var got []string
	for _, en := range tl.Entries {
		got = append(got, en.Type)
	}
	if strings.Join(got, ",") != "dispatched,tool,running,ended" && strings.Join(got, ",") != "dispatched,running,tool,ended" {
		t.Fatalf("wrong synthesized entries for an old run: %v", got)
	}
	_ = agent
}

func TestUsageAndAnomalies(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agentA, a := e.newAgent("A")
	agentB, b := e.newAgent("B")
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "fail please"})
	waitIdle(t, e, a)
	e.expect(202, "POST", b+"/messages", map[string]any{"text": "ok"})
	waitIdle(t, e, b)
	runA := e.lastReply(a).Run.ID

	old := time.Now().Add(-48 * time.Hour)
	for _, u := range []store.UsageRecord{
		{AgentID: agentA.ID, RunID: &runA, Kind: store.UsageAssistant, Provider: "anthropic", Model: "claude-x", Input: 10, Output: 5},
		{AgentID: agentA.ID, RunID: &runA, Kind: store.UsageAssistant, Provider: "anthropic", Model: "claude-x", Input: 10, Output: 5},
		{AgentID: agentB.ID, Kind: store.UsageAssistant, Provider: "openai", Model: "gpt-x", Input: 1, Output: 1},
		{AgentID: agentB.ID, Kind: store.UsageTool, Input: 2},
		{AgentID: agentB.ID, Kind: store.UsageAssistant, Provider: "openai", Model: "gpt-x", Input: 1000, At: old},
	} {
		if err := e.st.AddUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	since := url.QueryEscape(time.Now().Add(-24 * time.Hour).Format(time.RFC3339Nano))
	page := decode[apitypes.UsagePage](t, e.expect(200, "GET", "/usage?from="+since, nil))
	if len(page.Rows) != 3 {
		t.Fatalf("want three rows: %+v", page.Rows)
	}
	if r := page.Rows[0]; r.AgentID != agentA.ID || *r.Provider != "anthropic" || r.Calls != 2 || r.Total != 30 {
		t.Errorf("the first row should be A's anthropic usage: %+v", r)
	}
	var unknown int
	for _, r := range page.Rows {
		if r.Provider == nil && r.Model == nil {
			unknown++
		}
	}
	if unknown != 1 {
		t.Errorf("tool usage should be counted under an unknown model: %+v", page.Rows)
	}
	all := decode[apitypes.UsagePage](t, e.expect(200, "GET", "/usage", nil))
	var totalB int64
	for _, r := range all.Rows {
		if r.AgentID == agentB.ID {
			totalB += r.Total
		}
	}
	if totalB != 1004 {
		t.Errorf("without a time range, older usage should be included: %d", totalB)
	}
	if strings.Contains(string(e.expect(200, "GET", "/usage", nil)), "cost") {
		t.Error("usage stats should not contain costs")
	}
	from, to := url.QueryEscape(time.Now().Format(time.RFC3339)), url.QueryEscape(time.Now().Add(-time.Hour).Format(time.RFC3339))
	e.expect(422, "GET", "/usage?from="+from+"&to="+to, nil, "X-Contract-Invalid-Request", "1")
	e.expect(422, "GET", "/usage?from=yesterday", nil, "X-Contract-Invalid-Request", "1")

	// Deleted Agent: usage is counted as usual and marked as deleted.
	e.expect(204, "DELETE", b, nil)
	page = decode[apitypes.UsagePage](t, e.expect(200, "GET", "/usage?from="+since, nil))
	deleted := false
	for _, r := range page.Rows {
		if r.AgentID == agentB.ID && r.AgentDeleted {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("usage of a deleted Agent should be marked deleted: %+v", page.Rows)
	}

	// Anomaly list: A's failure is listed, B is deleted and not listed.
	for i := range 3 {
		e.expect(202, "POST", a+"/messages", map[string]any{"text": fmt.Sprintf("fail %d", i)})
		waitIdle(t, e, a)
	}
	list := decode[apitypes.AnomalyPage](t, e.expect(200, "GET", "/anomalies?from="+since+"&limit=2", nil))
	if list.Total != 4 || len(list.Items) != 2 || list.NextCursor == nil {
		t.Fatalf("wrong first page: %+v", list)
	}
	it := list.Items[0]
	if it.Kind != "run_failed" || it.AgentID != agentA.ID || it.AgentName != "A" || it.ErrorCode == nil || *it.ErrorCode != "workspace_sync_failed" {
		t.Errorf("wrong anomaly item: %+v", it)
	}
	seen := map[uuid.UUID]bool{}
	for _, x := range list.Items {
		seen[x.RunID] = true
	}
	more := decode[apitypes.AnomalyPage](t, e.expect(200, "GET", "/anomalies?from="+since+"&limit=2&cursor="+url.QueryEscape(*list.NextCursor), nil))
	if len(more.Items) != 2 || more.NextCursor != nil || more.Total != 4 {
		t.Fatalf("wrong second page: %+v", more)
	}
	for _, x := range more.Items {
		if seen[x.RunID] {
			t.Fatalf("duplicate across pages: %v", x.RunID)
		}
	}
	e.expect(422, "GET", "/anomalies?cursor=bad", nil)
	future := url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339))
	if l := decode[apitypes.AnomalyPage](t, e.expect(200, "GET", "/anomalies?from="+future, nil)); l.Total != 0 || len(l.Items) != 0 {
		t.Errorf("no anomalies after the start time: %+v", l)
	}
}

func TestOverviewEvents(t *testing.T) {
	e := newEnv(t)
	_, a := e.newAgent("A")

	ch, stop := readSSE(t, e, "/overview", "")
	defer stop()
	snap := next(t, ch)
	e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, snap.event, []byte(snap.data))
	s := decode[apitypes.SseOverviewSnapshot](t, []byte(snap.data))
	if snap.event != "snapshot" || len(s.Agents) != 1 || s.Agents[0].Name != "A" || s.Agents[0].LastRun != nil {
		t.Fatalf("wrong snapshot: %+v", snap)
	}

	// Creating an Agent pushes its status (pushes are asynchronous, so the status from A's creation may arrive after
	// the snapshot).
	agentB, b := e.newAgent("B")
	var resumeFrom string
	for {
		ev := next(t, ch)
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, ev.event, []byte(ev.data))
		if ev.event != "agent.status" {
			t.Fatalf("no other events should appear: %+v", ev)
		}
		if decode[apitypes.SseAgentStatus](t, []byte(ev.data)).Agent.AgentID == agentB.ID {
			resumeFrom = ev.id
			break
		}
	}

	// Sending a message: the final push is idle and carries the result of one reply (pushes merge intermediate changes,
	// so not every intermediate state is guaranteed to appear); text deltas are not pushed.
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "Hello"})
	for idle := false; !idle; {
		ev := next(t, ch)
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, ev.event, []byte(ev.data))
		if ev.event != "agent.status" {
			t.Fatalf("no other events should appear: %+v", ev)
		}
		st := decode[apitypes.SseAgentStatus](t, []byte(ev.data)).Agent
		idle = !st.Status.Busy && st.LastRun != nil && st.LastRun.Status == "completed"
	}

	// Deleting an Agent: pushes a removal event.
	e.expect(204, "DELETE", b, nil)
	for {
		ev := next(t, ch)
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, ev.event, []byte(ev.data))
		if ev.event == "agent.removed" {
			if decode[apitypes.SseAgentRemoved](t, []byte(ev.data)).AgentID != agentB.ID {
				t.Fatalf("wrong removal event: %+v", ev)
			}
			break
		}
	}
	stop()

	// Resume: reconnecting with the last event ID pushes no snapshot.
	ch2, stop2 := readSSE(t, e, "/overview", resumeFrom)
	defer stop2()
	if first := next(t, ch2); first.event == "snapshot" {
		t.Fatalf("resuming should not push a snapshot: %+v", first)
	}
	// Outside the retained range: a snapshot is pushed.
	ch3, stop3 := readSSE(t, e, "/overview", "1-0")
	defer stop3()
	if ev := next(t, ch3); ev.event != "snapshot" {
		t.Fatalf("outside the retained range a snapshot should be pushed: %+v", ev)
	}
}

func TestMessagesPaging(t *testing.T) {
	e := newEnv(t)
	agent, a := e.newAgent("paging")
	for i := range 15 {
		e.expect(202, "POST", a+"/messages", map[string]any{"text": fmt.Sprintf("m%02d", i)})
		waitIdle(t, e, a)
	}
	all := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=200", nil))
	if len(all.Messages) != 30 || all.HasMore || all.HasMoreAfter || all.TurnsBefore != 0 || all.TurnsTotal != 15 {
		t.Fatalf("wrong full message list: %d %v %v %d %d", len(all.Messages), all.HasMore, all.HasMoreAfter, all.TurnsBefore, all.TurnsTotal)
	}
	// Turn counts: each page's turnsBefore is the number of turns before the turn of its first message (here every turn
	// is one question and one answer, run in order).
	if raw := e.expect(200, "GET", a+"/messages?limit=4", nil); !strings.Contains(string(raw), `"turnsBefore":13`) || !strings.Contains(string(raw), `"turnsTotal":15`) {
		t.Fatalf("wrong turn counts on the latest page: %s", raw)
	}
	target := all.Messages[6] // the 4th user message
	around := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=6&aroundRun="+target.Run.ID.String(), nil))
	if len(around.Messages) != 6 || !around.HasMore || !around.HasMoreAfter || around.Messages[2].ID != target.ID || around.TurnsBefore != 2 || around.TurnsTotal != 15 {
		t.Fatalf("wrong located page: %d %v %v %d %d", len(around.Messages), around.HasMore, around.HasMoreAfter, around.TurnsBefore, around.TurnsTotal)
	}
	// Page forward from the located page to the newest without gaps or duplicates.
	got := append([]apitypes.Message(nil), around.Messages...)
	page := around
	for page.HasMoreAfter {
		last := page.Messages[len(page.Messages)-1].ID
		page = decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=7&after="+last.String(), nil))
		if !page.HasMore {
			t.Fatal("when paging forward there must be earlier messages")
		}
		if first := slices.IndexFunc(all.Messages, func(m apitypes.Message) bool { return m.ID == page.Messages[0].ID }); page.TurnsBefore != int64(first/2) {
			t.Fatalf("paging forward: turnsBefore=%d, the first item is message %d", page.TurnsBefore, first)
		}
		got = append(got, page.Messages...)
	}
	want := all.Messages[4:]
	if len(got) != len(want) {
		t.Fatalf("wrong count when paging forward: %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("item %d is wrong", i)
		}
	}
	// Locate the last reply: nothing more after it.
	lastRun := all.Messages[29].Run.ID
	tail := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=6&aroundRun="+lastRun.String(), nil))
	if tail.HasMoreAfter || len(tail.Messages) != 6 || tail.Messages[5].ID != all.Messages[29].ID {
		t.Fatalf("wrong page when locating the latest: %+v", tail.HasMoreAfter)
	}
	// Mutually exclusive parameters; a run that does not belong to the Agent.
	e.expect(422, "GET", a+"/messages?before="+target.ID.String()+"&after="+target.ID.String(), nil)
	_, b := e.newAgent("other")
	e.expect(404, "GET", b+"/messages?aroundRun="+target.Run.ID.String(), nil)
	_ = agent
}

// Every response (including errors, health checks and rejected Hosts) carries a request ID that a cross-origin console
// can read; the request ID of sending a message is recorded in that run's dispatch or enqueue event.
func TestRequestID(t *testing.T) {
	e := newEnv(t)
	ids := map[string]bool{}
	check := func(h http.Header) string {
		t.Helper()
		id := h.Get("Loop-Now-Request-Id")
		if _, err := uuid.Parse(id); err != nil || ids[id] {
			t.Fatalf("request ID missing or duplicated: %q", id)
		}
		if v := h.Get("Loop-Now-Version"); v != buildinfo.Version() {
			t.Fatalf("version header should be %q: %q", buildinfo.Version(), v)
		}
		ids[id] = true
		return id
	}
	_, _, h := e.do("GET", "/context", nil)
	check(h)
	_, _, h = e.do("GET", "/agents/"+uuid.Must(uuid.NewV7()).String(), nil)
	check(h)
	resp, err := http.Get(e.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	check(resp.Header)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/context", nil)
	req.Host = "evil.example"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("an invalid Host should be rejected: %d", resp.StatusCode)
	}
	check(resp.Header)
	pre, _ := http.NewRequest("OPTIONS", e.srv.URL+"/api/v1/platform/agents", nil)
	pre.Header.Set("Origin", "http://localhost:5173")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	resp, err = http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if exp := resp.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(exp, "Loop-Now-Request-Id") || !strings.Contains(exp, "Loop-Now-Version") {
		t.Fatalf("the preflight response should expose the request ID and version headers: %v", resp.Header)
	}

	agent, a := e.newAgent("req")
	_, _, h = e.do("POST", a+"/messages", map[string]any{"text": "Hello"})
	sent := check(h)
	waitIdle(t, e, a)
	runID := e.lastReply(a).Run.ID
	evs, err := e.st.RunEvents(context.Background(), runID)
	if err != nil || len(evs) == 0 || evs[0].Type != store.EventRunDispatched || evs[0].Data["requestId"] != sent {
		t.Fatalf("the dispatch event should record request ID %s: %+v %v", sent, evs, err)
	}
	_ = agent
}
