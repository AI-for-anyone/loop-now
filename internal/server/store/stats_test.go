package store_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type statsSeed struct {
	t    *testing.T
	s    *store.Store
	base time.Time
}

func (e statsSeed) at(d time.Duration) time.Time { return e.base.Add(d) }

// run inserts a normal run that was dispatched at queued; running and ended are
// offsets from queued (negative: unset).
func (e statsSeed) run(agent uuid.UUID, origin, status string, queued, running, ended time.Duration, endReason, errorCode *string, trigger, sleep *uuid.UUID) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	opt := func(d time.Duration) *time.Time {
		if d < 0 {
			return nil
		}
		v := e.at(queued + d)
		return &v
	}
	mustExec(e.t, e.s, `INSERT INTO runs(id,agent_id,user_message_id,config_version,priority,status,origin,end_reason,error_code,queued_at,started_at,running_at,ended_at,trigger_inbox_message_id,wake_sleep_id)
		VALUES($1,$2,$3,1,1,$4,$5,$6,$7,$8,$8,$9,$10,$11,$12)`, id, agent, uuid.New(), status, origin, endReason, errorCode, e.at(queued), opt(running), opt(ended), trigger, sleep)
	return id
}

func (e statsSeed) turn(task, runtime uuid.UUID, origin, status string, queued, ended time.Duration, errorCode *string, sleep *uuid.UUID) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	mustExec(e.t, e.s, `INSERT INTO task_turns(id,task_id,runtime_id,user_message_id,assistant_message_id,status,origin,text,error_code,queued_at,started_at,ended_at,wake_sleep_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,'',$8,$9,$9,$10,$11)`, id, task, runtime, uuid.New(), uuid.New(), status, origin, errorCode, e.at(queued), e.at(queued+ended), sleep)
	return id
}

func (e statsSeed) usage(agent uuid.UUID, at time.Duration, kind string, run, task, turn *uuid.UUID, input, output, cacheRead, cacheWrite int64) {
	e.t.Helper()
	mustExec(e.t, e.s, `INSERT INTO usage_records(id,at,agent_id,run_id,task_id,task_turn_id,kind,provider,model,input,output,cache_read,cache_write)
		VALUES($1,$2,$3,$4,$5,$6,$7,'anthropic','claude-x',$8,$9,$10,$11)`, uuid.New(), e.at(at), agent, run, task, turn, kind, input, output, cacheRead, cacheWrite)
}

func (e statsSeed) event(agent uuid.UUID, at time.Duration, typ string, data map[string]any) {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	mustExec(e.t, e.s, `INSERT INTO lifecycle_events(id,at,agent_id,type,data) VALUES($1,$2,$3,$4,$5)`, uuid.New(), e.at(at), agent, typ, raw)
}

func (e statsSeed) message(from uuid.UUID, to *uuid.UUID, at time.Duration, text string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	toKind, whenBusy := "console", (*string)(nil)
	if to != nil {
		q := "queue"
		toKind, whenBusy = "agent", &q
	}
	mustExec(e.t, e.s, `INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_agent_id,text,when_busy,created_at) VALUES($1,$1,'agent',$2,$3,$4,$5,$6,$7)`,
		id, from, toKind, to, text, whenBusy, e.at(at))
	return id
}

// question inserts a question to the console; answered < 0 leaves it open.
func (e statsSeed) question(from uuid.UUID, at time.Duration, text string, answered time.Duration, expires time.Time) uuid.UUID {
	e.t.Helper()
	id := e.message(from, nil, at, text)
	state, answeredAt := "open", (*time.Time)(nil)
	if answered >= 0 {
		v := e.at(at + answered)
		state, answeredAt = "answered", &v
	}
	mustExec(e.t, e.s, `INSERT INTO inbox_questions(message_id,options,expires_at,state,answered_at) VALUES($1,'[]',$2,$3,$4)`, id, expires, state, answeredAt)
	return id
}

func strp(s string) *string { return &s }

func near(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 1e-6 }

// The stats report attributes tokens to what triggered each run, takes wait
// percentiles over console runs (queue time included), and lists pending
// questions regardless of the window; deleted agents never count.
func TestStatsAttributionWaitAndPendingQuestions(t *testing.T) {
	s := pgtest.NewStore(t)
	ctx := context.Background()
	dc, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	agent := func(name string) uuid.UUID {
		a, err := s.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), name, "anthropic", "claude-x", "")
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	A, B, D := agent("alpha"), agent("beta"), agent("gone")
	e := statsSeed{t: t, s: s, base: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)}
	h := time.Hour
	sec := time.Second

	// One task with an instance of B.
	task, runtime := uuid.New(), uuid.New()
	doc, _ := json.Marshal(map[string]any{"view": map[string]any{"task": map[string]any{"status": "running", "version": 1}}})
	mustExec(t, s, `INSERT INTO task_records(id,project_id,status,document,version) VALUES($1,$2,'running',$3,1)`, task, dc.Project.ID, doc)
	mustExec(t, s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha) VALUES($1,$2,$3,'beta',$4,encode(sha256($4),'hex'))`, runtime, task, B, []byte(`{}`))
	sleep, timer := uuid.New(), uuid.New()
	mustExec(t, s, `INSERT INTO agent_sleeps(id,scope_key,scope_kind,mode,agent_id,session_epoch,source_run_id,action_id,body_hash,receipt,state,seconds,entered_at,wake_at,ended_at,end_reason,timer_run_id,wake_run_id,revision)
		VALUES($1,'normal/'||$2::uuid::text,'normal','normal',$2::uuid,1,$3,$4,$5,'{}','woken',60,$6,$6,$6,'deadline',$7,$7,1)`, sleep, A, uuid.New(), uuid.New(), make([]byte, 32), e.at(0), timer)

	// Bucket 0 (day 1) and bucket 1 (day 2). Console waits: 10, 30, 20 | 50.
	r1 := e.run(A, "console", "completed", 1*h, 10*sec, 70*sec, nil, nil, nil, nil)
	r2 := e.run(A, "console", "interrupted", 2*h, 30*sec, 90*sec, strp("user_interrupt"), nil, nil, nil)
	e.run(A, "console", "interrupted", 3*h, 20*sec, 40*sec, strp("user_stopped"), nil, nil, nil)
	trigger := e.message(B, &A, 4*h, "提醒")
	r5 := e.run(A, "inbox_reminder", "completed", 4*h, 5*sec, 25*sec, nil, nil, &trigger, nil)
	r6 := e.run(A, "sleep_wake", "completed", 5*h, 5*sec, 65*sec, nil, nil, nil, &sleep)
	r7 := e.run(A, "sleep_wake", "completed", 9*h, 5*sec, 15*sec, nil, nil, nil, &sleep)
	e.run(A, "console", "failed", 25*h, 50*sec, 110*sec, nil, strp("pi_exited"), nil, nil)
	// Never dispatched: cancelled while queued.
	mustExec(t, s, `INSERT INTO runs(id,agent_id,user_message_id,config_version,priority,status,origin,end_reason,queued_at,ended_at) VALUES($1,$2,$3,1,1,'interrupted','console','user_cancelled',$4,$4)`,
		uuid.New(), A, uuid.New(), e.at(6*h))
	t1 := e.turn(task, runtime, "task_node", "completed", 6*h, 100*sec, nil, nil)
	t2 := e.turn(task, runtime, "console_node", "interrupted", 26*h, 10*sec, strp("user_stopped"), nil)
	t3 := e.turn(task, runtime, "sleep_wake", "completed", 27*h, 10*sec, nil, &sleep)
	// The deleted agent and out-of-window rows never count.
	gone := e.run(D, "console", "completed", 1*h, 1*sec, 2*sec, nil, nil, nil, nil)
	e.usage(D, 1*h, "assistant", &gone, nil, nil, 99999, 0, 0, 0)
	e.usage(A, -1*h, "assistant", nil, nil, nil, 77777, 0, 0, 0)
	e.run(A, "console", "completed", 49*h, 1*sec, 2*sec, nil, nil, nil, nil)

	e.usage(A, 1*h+30*sec, "assistant", &r1, nil, nil, 100, 50, 900, 0)
	e.usage(A, 1*h+40*sec, "compaction", &r1, nil, nil, 1000, 0, 0, 0)
	e.usage(A, 4*h+10*sec, "assistant", &r5, nil, nil, 10, 10, 0, 0)
	e.usage(A, 5*h+10*sec, "assistant", &r6, nil, nil, 0, 100, 500, 0)
	e.usage(A, 9*h+10*sec, "assistant", &r7, nil, nil, 100, 0, 0, 0)
	e.usage(A, 10*h, "cache_warm", nil, nil, nil, 0, 0, 0, 300)
	e.usage(B, 6*h+10*sec, "assistant", nil, &task, &t1, 200, 20, 0, 0)
	e.usage(B, 26*h+5*sec, "assistant", nil, &task, &t2, 30, 0, 0, 0)
	e.usage(B, 27*h+5*sec, "assistant", nil, &task, &t3, 40, 0, 0, 0)

	for _, ev := range []struct {
		run   uuid.UUID
		call  string
		name  string
		state string
	}{{r1, "c1", "bash", "started"}, {r1, "c1", "bash", "succeeded"}, {r1, "c2", "bash", "started"}, {r1, "c2", "bash", "failed"},
		{r1, "c3", "read", "started"}, {r1, "c3", "read", "succeeded"}, {r2, "c1", "bash", "started"}, {r2, "c1", "bash", "succeeded"}} {
		mustExec(t, s, `INSERT INTO run_tool_events(run_id,seq,tool_call_id,tool_name,state,at) VALUES($1,(SELECT count(*)+1 FROM run_tool_events WHERE run_id=$1),$2,$3,$4,$5)`,
			ev.run, ev.call, ev.name, ev.state, e.at(2*h))
	}
	e.event(A, 1*h, "model.retry_started", map[string]any{"attempt": 1})
	e.event(A, 2*h, "model.retry_started", map[string]any{"attempt": 1})
	e.event(A, 3*h, "pi.exited", map[string]any{"exit": "signal: killed"})
	e.event(A, 3*h, "pi.stopped", map[string]any{"reason": "abort_timeout"})
	e.event(A, 3*h, "pi.stopped", map[string]any{"reason": "idle_timeout"})
	e.event(A, 4*h, "pi.started", map[string]any{"startupMs": 300})
	e.event(A, 5*h, "pi.started", map[string]any{"startupMs": 500})
	e.event(D, 5*h, "pi.started", map[string]any{"startupMs": 9000})

	far := time.Now().Add(240 * h)
	e.question(A, 2*h, "第一个问题", 600*sec, far)
	e.question(A, 26*h, "第二个问题", 1200*sec, far)
	q3 := e.question(A, 3*h, strings.Repeat("问", 100), -1, far)
	q4 := e.question(B, -5*24*h, "窗口之前就在等", -1, far)
	e.question(A, 4*h, "已经过期", -1, time.Now().Add(-h))
	e.question(D, 4*h, "已删除的 Agent", -1, far)
	if err := s.SoftDeleteAgent(ctx, D); err != nil {
		t.Fatal(err)
	}

	got, err := s.Stats(ctx, store.ScopeFilter{}, e.base, e.at(48*h), 24*h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 2 || !got.Buckets[1].Equal(e.at(24*h)) {
		t.Fatalf("buckets: %v", got.Buckets)
	}

	// Tokens and attribution.
	o := got.ByOrigin.Totals
	if o != (store.StatsOriginTokens{Console: 2080, InboxReminder: 20, SleepWake: 740, Task: 220, Other: 300}) || got.Tokens.Totals.Total != 3360 {
		t.Fatalf("byOrigin: %+v total %d", o, got.Tokens.Totals.Total)
	}
	if s0, s1 := got.ByOrigin.Series[0], got.ByOrigin.Series[1]; s0.Console != 2050 || s0.SleepWake != 700 || s0.Task != 220 || s0.Other != 300 || s1.Console != 30 || s1.SleepWake != 40 {
		t.Fatalf("byOrigin series: %+v", got.ByOrigin.Series)
	}
	if got.Tokens.Series[0]+got.Tokens.Series[1] != 3360 || !near(got.ByOrigin.UnrequestedShare, 740.0/3360) {
		t.Fatalf("token series/share: %v %v", got.Tokens.Series, got.ByOrigin.UnrequestedShare)
	}
	if !near(got.Tokens.CacheHitRate, 1400.0/(1400+1480)) || len(got.Tokens.ByModel) != 1 || got.Tokens.ByModel[0].Total != 3360 {
		t.Fatalf("cache/byModel: %v %+v", got.Tokens.CacheHitRate, got.Tokens.ByModel)
	}

	// Wakes: normal and task sleep wakes, tokens of those runs.
	if len(got.Wakes) != 2 || got.Wakes[0].AgentID != A || got.Wakes[0].Count != 2 || got.Wakes[0].Tokens != 700 || got.Wakes[0].AvgTokens != 350 ||
		!near(got.Wakes[0].AvgIntervalSeconds, 4*3600) || got.Wakes[1].AgentID != B || got.Wakes[1].Count != 1 || got.Wakes[1].AvgIntervalSeconds != nil {
		t.Fatalf("wakes: %+v", got.Wakes)
	}

	// Context: assistant calls only; compactions counted separately.
	c := got.Context
	if c.Compactions != 1 || !near(c.AvgInputPerCall, 1880.0/7) || len(c.Agents) != 2 || c.Agents[0].AgentID != A || c.Agents[0].Calls != 4 || c.Agents[0].AvgInputPerCall != 402.5 || c.Agents[1].AvgInputPerCall != 90 {
		t.Fatalf("context: %+v", c)
	}

	// Wait: queued → running for console runs only; p90 interpolated.
	w := got.Wait
	if !near(w.P50, 25) || !near(w.P90, 44) || !near(w.Series[0].P50, 20) || !near(w.Series[0].P90, 28) || !near(w.Series[1].P50, 50) || !near(w.Series[1].P90, 50) {
		t.Fatalf("wait: %+v %+v", w, w.Series)
	}
	// Durations: runs from running, task turns from dispatch: 60,60,20,20,60,10,60 and 100,10,10.
	if !near(w.DurationP50, 40) {
		t.Fatalf("durationP50: %v", *w.DurationP50)
	}

	// Outcomes and reasons, task turns included; the cancelled queued run is not.
	out := got.Outcomes
	if out.Totals != (store.StatsOutcomeCounts{Completed: 6, Interrupted: 3, Failed: 1}) ||
		out.Series[0] != (store.StatsOutcomeCounts{Completed: 5, Interrupted: 2}) || out.Series[1] != (store.StatsOutcomeCounts{Completed: 1, Interrupted: 1, Failed: 1}) {
		t.Fatalf("outcomes: %+v", out)
	}
	reasons, _ := json.Marshal(out.Reasons)
	if string(reasons) != `[{"kind":"stopped","code":"user_stopped","count":2},{"kind":"failed","code":"pi_exited","count":1},{"kind":"interrupted","code":"user_interrupt","count":1}]` {
		t.Fatalf("reasons: %s", reasons)
	}

	// Questions: asked in the window; pending is everything open now.
	q := got.Questions
	if q.Asked != 4 || q.Answered != 2 || !near(q.AvgAnswerSeconds, 900) || !near(q.MaxAnswerSeconds, 1200) || q.Series[0] != 3 || q.Series[1] != 1 {
		t.Fatalf("questions: %+v", q)
	}
	if len(q.Pending) != 2 || q.Pending[0].MessageID != q4 || q.Pending[0].AgentID != B || q.Pending[1].MessageID != q3 ||
		q.Pending[1].Text != strings.Repeat("问", 80) || q.Pending[1].Name != "alpha" || *q.Pending[1].ProjectID != dc.Project.ID {
		t.Fatalf("pending: %+v", q.Pending)
	}

	// Health rows, deleted agent excluded.
	if len(got.Agents) != 2 || got.Agents[0].AgentID != A || got.Agents[1].AgentID != B {
		t.Fatalf("agents: %+v", got.Agents)
	}
	a, b := got.Agents[0], got.Agents[1]
	if a.Runs != 7 || !near(a.CompletionRate, 4.0/7) || !near(a.WaitP90, 44) || a.Wakes != 2 || a.WakeTokens != 700 || !near(a.AvgContext, 402.5) ||
		a.Compactions != 1 || a.Tokens != 3070 || !near(a.CacheHitRate, 1400.0/2610) || !near(a.DurationP50, 60) {
		t.Fatalf("health A: %+v", a)
	}
	if b.Runs != 3 || !near(b.CompletionRate, 2.0/3) || b.WaitP90 != nil || b.Tokens != 290 || !near(b.DurationP50, 10) {
		t.Fatalf("health B: %+v", b)
	}

	// Tools of normal runs, one call per (run, call id).
	tools, _ := json.Marshal(got.Tools)
	if string(tools) != `{"byName":[{"name":"bash","calls":3,"failures":1},{"name":"read","calls":1,"failures":0}],"callsPerRunAvg":0.5714285714285714,"callsPerRunMax":3}` {
		t.Fatalf("tools: %s", tools)
	}
	if got.Process != (store.StatsProcess{ModelRetries: 2, PiExits: 2, PiColdStarts: 2, PiColdStartAvgMs: got.Process.PiColdStartAvgMs}) || !near(got.Process.PiColdStartAvgMs, 400) {
		t.Fatalf("process: %+v", got.Process)
	}

	// Concurrency against a per-second count of the same replying intervals.
	type span struct{ lo, hi time.Time }
	spans := []span{}
	for _, r := range []struct{ q, run, end time.Duration }{{1 * h, 10 * sec, 70 * sec}, {2 * h, 30 * sec, 90 * sec}, {3 * h, 20 * sec, 40 * sec}, {4 * h, 5 * sec, 25 * sec},
		{5 * h, 5 * sec, 65 * sec}, {9 * h, 5 * sec, 15 * sec}, {25 * h, 50 * sec, 110 * sec}, {6 * h, 0, 100 * sec}, {26 * h, 0, 10 * sec}, {27 * h, 0, 10 * sec}} {
		spans = append(spans, span{e.at(r.q + r.run), e.at(r.q + r.end)})
	}
	var busy, seen [24]float64
	for x := e.base; x.Before(e.at(48 * h)); x = x.Add(time.Second) {
		hour := x.In(time.Local).Hour()
		seen[hour]++
		for _, sp := range spans {
			if !x.Before(sp.lo) && x.Before(sp.hi) {
				busy[hour]++
			}
		}
	}
	for hour := range 24 {
		if !near(got.Concurrency[hour], busy[hour]/seen[hour]) {
			t.Fatalf("concurrency hour %d: got %v want %v", hour, got.Concurrency[hour], busy[hour]/seen[hour])
		}
	}

	// Scope: the platform has none of it.
	p, err := s.Stats(ctx, store.ScopeFilter{Platform: true}, e.base, e.at(48*h), 24*h)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tokens.Totals.Total != 0 || len(p.Agents) != 0 || len(p.Questions.Pending) != 0 || p.Wait.P50 != nil || p.Context.AvgInputPerCall != nil || p.Tools.CallsPerRunAvg != nil {
		t.Fatalf("platform scope: %+v", p)
	}
}
