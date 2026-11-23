package pluginhost_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

type hostEnv struct {
	t       *testing.T
	st      *store.Store
	journal string
	specs   []pluginhost.Spec
	sup     *pluginhost.Supervisor
}

// newHostEnv creates a migrated test database, registers the plugins in specs, and prepares a supervisor that runs
// the fake plugin host (not yet started).
func newHostEnv(t *testing.T, mode string, specs []pluginhost.Spec, tune func(*pluginhost.Options)) *hostEnv {
	t.Helper()
	e := &hostEnv{t: t, st: sqlitetest.NewStore(t), journal: filepath.Join(t.TempDir(), "journal.jsonl"), specs: specs}
	for _, s := range specs {
		e.exec(`INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$1,'{}','1.0.0','{}','/x','installed')`, s.ID)
	}
	o := pluginhost.Options{
		Command: []string{os.Args[0], "-test.run=^TestFakeHostProcess$"},
		Dir:     t.TempDir(),
		Env:     append(os.Environ(), fakeHostEnv+"=1", "FAKE_HOST_JOURNAL="+e.journal, "FAKE_HOST_MODE="+mode),
		Store:   e.st,
		Specs:   func(context.Context) ([]pluginhost.Spec, error) { return e.specs, nil },
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),

		ReadyTimeout: 5 * time.Second, LoadTimeout: 5 * time.Second, CallTimeout: 300 * time.Millisecond, EventTimeout: 2 * time.Second,
		ShutdownGrace: 2 * time.Second, RestartDelay: 20 * time.Millisecond, PollInterval: 20 * time.Millisecond,
		Backoff: []time.Duration{20 * time.Millisecond},
	}
	if tune != nil {
		tune(&o)
	}
	e.sup = pluginhost.New(o)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.sup.Stop(ctx); err != nil {
			t.Errorf("stop the host supervisor: %v", err)
		}
	})
	return e
}

func (e *hostEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *hostEnv) scalar(sql string, args ...any) string {
	e.t.Helper()
	var v *string
	if err := e.st.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	if v == nil {
		return "<nil>"
	}
	return *v
}

// eventually checks cond repeatedly for up to 10 seconds and returns cond's last description.
func (e *hostEnv) eventually(what string, cond func() (bool, string)) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var ok bool
		if ok, last = cond(); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s: %s", what, last)
}

// entries reads the fake plugin host's journal.
func (e *hostEnv) entries() []map[string]any {
	f, err := os.Open(e.journal)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (e *hostEnv) count(typ, plugin, eventType string) int {
	n := 0
	for _, m := range e.entries() {
		if m["type"] == typ && (plugin == "" || m["plugin"] == plugin) && (eventType == "" || m["eventType"] == eventType) {
			n++
		}
	}
	return n
}

func (e *hostEnv) addEvent(typ string, at time.Time, plugins ...string) string {
	e.t.Helper()
	id, err := e.st.AddPluginEvent(context.Background(), store.PluginEvent{Type: typ, At: at, Data: json.RawMessage(`{"excerpt":"hi","id":"spoofed"}`)}, plugins)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func spec(id string, behaviour map[string]any, events ...string) pluginhost.Spec {
	raw, _ := json.Marshal(behaviour)
	return pluginhost.Spec{ID: id, Events: events, Permissions: []string{"state"}, Load: raw}
}

// Once the plugin host is ready, plugins are loaded in order; a plugin that fails to load goes into the error status.
// Observe events are delivered one by one in event time order and marked done after ack; events the plugin reports
// errors for are redelivered according to Backoff and marked failed once it is used up, without holding up later
// events. state_* writes go to the database through Go, and standard error and log go to the plugin host log.
func TestHostLoadsDeliversEventsAndKeepsState(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{
		spec("alpha", map[string]any{"stateProbe": true, "failEvents": []string{"x.fail"}}, "inbox.received", "x.fail", "run.ended"),
		spec("beta", map[string]any{"fail": "manifest and code subscriptions do not match"}, "inbox.received"),
	}, nil)
	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	// Written in the reverse order of event time: delivery follows event time.
	late := e.addEvent("run.ended", base.Add(2*time.Second), "alpha")
	failing := e.addEvent("x.fail", base.Add(time.Second), "alpha")
	early := e.addEvent("inbox.received", base, "alpha", "beta")

	e.sup.Start(false)
	// alpha starts delivering as soon as it is loaded, when beta may not be loaded yet; the plugin host only becomes
	// running once all are loaded (and their statuses written to the database).
	e.eventually("loading and delivery", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(event_id||'='||status||'/'||attempts, ',' ORDER BY event_id) FROM plugin_event_deliveries WHERE plugin_id='alpha'`)
		st := e.sup.Status().State
		return st == pluginhost.StateRunning && strings.Count(s, "delivered/1") == 2 && strings.Contains(s, failing+"=failed/2"), st + " " + s
	})
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='alpha'`); got != "active|" {
		t.Fatalf("alpha status: %s", got)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='beta'`); got != "error|manifest and code subscriptions do not match" {
		t.Fatalf("beta status: %s", got)
	}
	if got := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE plugin_id='beta' AND event_id=$1`, early); got != "pending" {
		t.Fatalf("events of a plugin that is not loaded should stay pending: %s", got)
	}
	var order []string
	for _, m := range e.entries() {
		if m["type"] == "event" && m["plugin"] == "alpha" {
			order = append(order, m["eventId"].(string))
			if ev := m["event"].(map[string]any); ev["id"] != m["eventId"] || ev["excerpt"] != "hi" || ev["projectId"] != nil {
				t.Fatalf("event object should carry the fixed and type-specific fields, fixed fields taking precedence: %v", ev)
			}
		}
	}
	if len(order) != 4 || order[0] != early || order[1] != failing || order[3] != failing || order[2] != late {
		t.Fatalf("delivery order (by event time, failed ones redelivered later): %v, want %s %s %s %s", order, early, failing, late, failing)
	}
	if got := e.scalar(`SELECT count(*)||'/'||count(duration_ms) FROM plugin_event_deliveries WHERE plugin_id='alpha' AND status='delivered'`); got != "2/2" {
		t.Fatalf("delivered deliveries should record the handling time: %s", got)
	}
	if got := e.scalar(`SELECT coalesce(duration_ms,'null') FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); got != "null" {
		t.Fatalf("a failed delivery should have no handling time: %s", got)
	}
	if got := e.scalar(`SELECT last_error FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); got != "boom" {
		t.Fatalf("failure reason: %s", got)
	}
	if got := e.scalar(`SELECT value FROM plugin_state WHERE plugin_id='alpha' AND project_id IS NULL AND key='probe/a'`); got != `{"n":1}` {
		t.Fatalf("state value: %s", got)
	}
	e.eventually("host log", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(coalesce(plugin_id,'-')||':'||level||':'||line, ' | ' ORDER BY id) FROM plugin_host_log`)
		return strings.Contains(s, "alpha:warn:loaded") && strings.Contains(s, `alpha:info:state {"n":1} [{"key":"probe/a","value":{"n":1}}]`) &&
			strings.Contains(s, "-:warn:fake host booting") && strings.Contains(s, "beta:error:Could not load the plugin: manifest and code subscriptions do not match"), s
	})
	st := e.sup.Status()
	if st.State != pluginhost.StateRunning || st.HostVersion != "fake-1" || len(st.Loaded) != 1 || st.Loaded[0] != "alpha" || st.LastCrash != nil {
		t.Fatalf("host status: %+v", st)
	}
}

// After an unexpected exit the plugin host restarts, and unacknowledged events are redelivered after the restart (at
// least once); after the 3rd exit within 5 minutes it is no longer restarted, plugins are marked host stopped, and
// the crash record tells which event of which plugin was being handled. A manual restart recovers.
func TestHostRestartPolicyAndCrashReport(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", map[string]any{"crashEvents": []string{"boom"}}, "boom", "inbox.received")},
		func(o *pluginhost.Options) {
			o.Backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
		})
	boom := e.addEvent("boom", time.Now(), "alpha")
	e.sup.Start(false)
	e.eventually("the host to stop restarting", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateStopped, st.State
	})
	st := e.sup.Status()
	if len(st.RecentExits) != 3 || st.LastCrash == nil || st.LastCrash.Last == nil || st.LastCrash.Last.Plugin != "alpha" || st.LastCrash.Last.EventID != boom || st.LastCrash.Last.EventType != "boom" {
		t.Fatalf("crash record: %+v %+v", st, st.LastCrash)
	}
	if !strings.Contains(st.LastCrash.Error, "exit status 5") || len(st.LastCrash.InFlight) != 1 {
		t.Fatalf("crash reason and in-flight requests: %+v", st.LastCrash)
	}
	if n := e.count("event", "alpha", "boom"); n != 3 {
		t.Fatalf("unacknowledged event should be redelivered after every restart: %d", n)
	}
	// First recorded as stopped, then the plugins are written as host stopped.
	e.eventually("the plugin marked host stopped", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`)
		return s == "host_stopped", s
	})
	// Not launched again after stopping: after a while the delivery count is unchanged.
	time.Sleep(200 * time.Millisecond)
	if n := e.count("load", "alpha", ""); n != 3 {
		t.Fatalf("should not start again after stopping: loaded %d times", n)
	}
	e.exec(`UPDATE plugin_event_deliveries SET status='delivered' WHERE event_id=$1`, boom)
	e.sup.Restart()
	e.eventually("manual restart", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.RecentExits) == 0, st.State
	})
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "active" {
		t.Fatalf("plugin should recover after the restart: %s", got)
	}
	ok := e.addEvent("inbox.received", time.Now(), "alpha")
	e.sup.Notify()
	e.eventually("normal delivery after restart", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE event_id=$1`, ok)
		return s == "delivered", s
	})
}

// A ready timeout and a protocol version mismatch each count as one failure under the restart policy.
func TestHostReadyTimeoutAndProtocolMismatch(t *testing.T) {
	for _, c := range []struct{ mode, want string }{{"no-ready", "ready"}, {"bad-protocol", "protocol version 99"}, {"crash-after-ready", "exit status 3"}} {
		t.Run(c.mode, func(t *testing.T) {
			e := newHostEnv(t, c.mode, []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, func(o *pluginhost.Options) {
				o.ReadyTimeout = 300 * time.Millisecond
				o.RestartLimit = 2
			})
			if c.mode == "crash-after-ready" {
				e.addEvent("inbox.received", time.Now(), "alpha")
			}
			e.sup.Start(false)
			e.eventually("the host to stop restarting", func() (bool, string) {
				st := e.sup.Status()
				return st.State == pluginhost.StateStopped, st.State
			})
			if st := e.sup.Status(); st.LastCrash == nil || !strings.Contains(st.LastCrash.Error, c.want) || len(st.RecentExits) != 2 {
				t.Fatalf("crash record: %+v", st.LastCrash)
			}
		})
	}
}

// Disabling globally makes the plugin host exit without counting as a crash, and plugins go back to installed;
// re-enabling launches the host and loads them. Reload unloads then loads, Unload only unloads. The host exits on its
// own after receiving shutdown.
func TestHostDisableReloadAndUnload(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, nil)
	e.sup.Start(true)
	e.eventually("disabled at start", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled, st.State
	})
	if n := e.count("load", "", ""); n != 0 {
		t.Fatalf("disabled at start, the host should not be launched: loaded %d times", n)
	}
	e.sup.SetDisabled(false)
	e.eventually("running after enable", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning, st.State
	})
	e.sup.SetDisabled(true)
	st := e.sup.Status()
	if st.State != pluginhost.StateDisabled && st.State != pluginhost.StateRunning {
		t.Fatalf("while disabling: %s", st.State)
	}
	e.eventually("disabled", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled && st.PID == 0, st.State
	})
	if st := e.sup.Status(); st.LastCrash != nil || len(st.RecentExits) != 0 || e.count("shutdown", "", "") != 1 {
		t.Fatalf("disabling is not a crash, the host should receive shutdown: %+v", st)
	}
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "installed" {
		t.Fatalf("plugin should go back to installed after disabling: %s", got)
	}
	e.sup.SetDisabled(false)
	e.eventually("re-enabled", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 1, st.State
	})
	ctx := context.Background()
	if err := e.sup.Reload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, m := range e.entries() {
		if m["plugin"] == "alpha" && (m["type"] == "load" || m["type"] == "unload") {
			seq = append(seq, m["type"].(string))
		}
	}
	if strings.Join(seq, ",") != "load,load,unload,load" {
		t.Fatalf("reload should unload then load: %v", seq)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{"config": map[string]any{}}); err != nil || e.count("config_changed", "alpha", "") != 1 {
		t.Fatalf("config change: %v", err)
	}
	if err := e.sup.Unload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if st := e.sup.Status(); len(st.Loaded) != 0 {
		t.Fatalf("should not be loaded after unload: %+v", st)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{}); err != pluginhost.ErrNotLoaded {
		t.Fatalf("want ErrNotLoaded for a plugin that is not loaded, got %v", err)
	}
}

// Synchronous calls go in load order: for intercept the first block wins, and a timeout or error allows by default
// but blocks with failClosed (the same for plugins that are not loaded); for modify each plugin sees the earlier
// changes, patches merge field by field, and a timeout is skipped; for claim the first handled wins, and a timeout
// counts as not handled. Plugins excluded by include do not take part; when disabled globally no plugin takes part.
func TestHostCallPolicies(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{
		spec("a", map[string]any{"intercept": "allow", "patch": map[string]any{"durationSeconds": 600}, "claim": "hang"}, "run.before_start", "sleep.before_schedule", "console.command"),
		spec("b", map[string]any{"intercept": "hang", "patch": map[string]any{"durationSeconds": 900, "note": "b"}, "claim": "no"}, "run.before_start", "sleep.before_schedule", "console.command"),
		spec("c", map[string]any{"intercept": "block", "modifyHang": true, "claim": "yes"}, "run.before_start", "sleep.before_schedule", "console.command"),
		{ID: "d", Events: []string{"node.before_submit"}, FailClosed: true, Load: json.RawMessage(`{"intercept":"error"}`)},
		{ID: "f", Events: []string{"task.before_start"}, FailClosed: true, Load: json.RawMessage(`{"fail":"cannot load"}`)},
	}, nil)
	e.sup.Start(false)
	e.eventually("loading", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 4, strings.Join(st.Loaded, ",")
	})
	ctx := context.Background()
	ev := func(typ string) map[string]any {
		return map[string]any{"id": "01J000000000000000000000AA", "type": typ, "durationSeconds": 60}
	}
	start := time.Now()
	out, err := e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), nil)
	if err != nil || !out.Blocked || out.Plugin != "c" || out.Reason != "Blocked by c." || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("intercept: b times out and allows, c blocks: %+v %v %s", out, err, time.Since(start))
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), func(id string) bool { return id != "c" }); out.Blocked {
		t.Fatalf("should allow once c is excluded: %+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("node.before_submit"), nil); !out.Blocked || out.Plugin != "d" || !strings.Contains(out.Reason, "check failed") {
		t.Fatalf("failClosed plugin should block when it errors: %+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); !out.Blocked || out.Plugin != "f" || !strings.Contains(out.Reason, "the plugin is not loaded") {
		t.Fatalf("failClosed plugin should block when it is not loaded: %+v", out)
	}
	out, err = e.sup.Call(ctx, pluginhost.Modify, ev("sleep.before_schedule"), nil)
	if err != nil || out.Blocked || out.Patch["durationSeconds"] != float64(900) || out.Patch["note"] != "b" || len(out.Patch) != 2 {
		t.Fatalf("modify: %+v %v", out, err)
	}
	for _, m := range e.entries() {
		if m["type"] == "call" && m["plugin"] == "b" && m["kind"] == "modify" {
			if got := m["event"].(map[string]any)["durationSeconds"]; got != float64(600) {
				t.Fatalf("b should see the event as changed by a: %v", got)
			}
		}
	}
	out, err = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), nil)
	if err != nil || !out.Handled || out.Plugin != "c" || !strings.Contains(string(out.Value), `"by":"c"`) {
		t.Fatalf("claim: a times out, b does not handle, c claims: %+v %v", out, err)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), func(id string) bool { return id != "c" }); out.Handled {
		t.Fatalf("no plugin should claim: %+v", out)
	}
	if _, err = e.sup.Call(ctx, pluginhost.Intercept, map[string]any{}, nil); err == nil {
		t.Fatal("an event without type should fail")
	}
	e.sup.SetDisabled(true)
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); out.Blocked {
		t.Fatalf("no plugin should take part when disabled globally: %+v", out)
	}
}

// Cleanup runs when the supervisor starts and every hour after that: observe events (with their delivery records)
// are kept for 30 days, plugin host logs for 7 days.
func TestHostPrunesOldEventsAndLogs(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, nil)
	old := e.addEvent("inbox.received", time.Now().Add(-31*24*time.Hour), "alpha")
	recent := e.addEvent("inbox.received", time.Now().Add(-29*24*time.Hour), "alpha")
	e.exec(`INSERT INTO plugin_host_log(at,plugin_id,level,line) VALUES(now()-691200000000,'alpha','info','old'),(now()-518400000000,'alpha','info','new')`)
	e.sup.Start(true) // cleans up even when disabled
	e.eventually("cleanup", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(id, ',' ORDER BY id) FROM plugin_events`) + " " + e.scalar(`SELECT string_agg(line, ',') FROM plugin_host_log`)
		return s == recent+" new", s
	})
	if got := e.scalar(`SELECT count(*) FROM plugin_event_deliveries WHERE event_id=$1`, old); got != "0" {
		t.Fatalf("delivery records should be deleted with the event: %s", got)
	}
}

// Scheduled jobs: when the clock reaches a minute that cron matches, a schedule request is sent, at most once per
// minute; the minute in which the plugin was loaded does not fire. An entry whose previous run has not responded yet
// skips this run and logs it; missed minutes are not made up.
func TestHostSchedules(t *testing.T) {
	var now, reads atomic.Int64
	base := time.Date(2026, 10, 27, 9, 0, 30, 0, time.Local)
	now.Store(base.UnixNano())
	sp := spec("cron", map[string]any{"hangSchedules": []string{"slow"}})
	sp.Schedules = []pluginmanifest.Schedule{{ID: "sweep", Cron: "* * * * *"}, {ID: "slow", Cron: "* * * * *"}, {ID: "hourly", Cron: "0 * * * *"}}
	e := newHostEnv(t, "", []pluginhost.Spec{sp}, func(o *pluginhost.Options) {
		o.Now = func() time.Time { reads.Add(1); return time.Unix(0, now.Load()) }
		o.ScheduleTick = 10 * time.Millisecond
	})
	e.sup.Start(false)
	// When the schedule goroutine starts, it reads the clock once per entry to record the load minute, then once per
	// tick: two extra reads mean one tick has completed at 09:00, and only after that can the clock be moved without
	// it being taken as the load minute.
	e.eventually("the schedule goroutine to start", func() (bool, string) {
		return reads.Load() >= int64(len(sp.Schedules))+2, fmt.Sprint(reads.Load())
	})
	if n := e.count("schedule", "cron", ""); n != 0 {
		t.Fatalf("the load minute should not fire: %d", n)
	}
	schedules := func(id string) int {
		n := 0
		for _, m := range e.entries() {
			if m["type"] == "schedule" && m["scheduleId"] == id {
				n++
			}
		}
		return n
	}
	now.Store(base.Add(time.Minute).UnixNano())
	e.eventually("firing at 09:01", func() (bool, string) {
		return schedules("sweep") == 1 && schedules("slow") == 1, fmt.Sprint(schedules("sweep"), schedules("slow"))
	})
	time.Sleep(50 * time.Millisecond)
	if schedules("sweep") != 1 {
		t.Fatal("should fire only once per minute")
	}
	now.Store(base.Add(59 * time.Minute).UnixNano()) // jump to 09:59: the minutes in between are not made up
	e.eventually("firing at 09:59", func() (bool, string) { return schedules("sweep") == 2, fmt.Sprint(schedules("sweep")) })
	if schedules("slow") != 1 || schedules("hourly") != 0 {
		t.Fatalf("slow should be skipped as its previous run has not responded, hourly is not due: %d %d", schedules("slow"), schedules("hourly"))
	}
	e.eventually("the skip to be logged", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(line, '|') FROM plugin_host_log WHERE plugin_id='cron'`)
		return strings.Contains(s, "Scheduled job slow has not finished its previous run"), s
	})
	now.Store(base.Add(60 * time.Minute).UnixNano())
	e.eventually("hourly firing at 10:00", func() (bool, string) { return schedules("hourly") == 1, "" })
	for _, m := range e.entries() {
		if m["type"] == "schedule" && m["scheduleId"] == "hourly" {
			if at, _ := time.Parse(time.RFC3339, m["at"].(string)); !at.Equal(time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)) {
				t.Fatalf("at should be the firing minute: %v", m["at"])
			}
		}
	}
}
