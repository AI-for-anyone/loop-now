package pluginhost_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

// Real plugin host: when LOOP_NOW_PLUGIN_HOST_ENTRY (the built plugins/host/dist/main.js) is set, it is run with
// node; plugins are index.ts files written to a temporary directory, and load payloads follow the shape of the
// protocol. The scenarios are the same as the fake host tests, and everything is observed through the database and
// the plugin host log.

type realPlugin struct {
	id          string
	events      []string
	permissions []string
	failClosed  bool
	schedules   []pluginmanifest.Schedule
	http        bool
	actions     []pluginmanifest.Action
	accounts    []string // account kinds
	code        string   // index.ts
}

// newRealHostEnv prepares a supervisor that runs the real plugin host (not yet started); it skips when the host
// entry point is not set.
func newRealHostEnv(t *testing.T, plugins []realPlugin, tune func(*pluginhost.Options)) *hostEnv {
	t.Helper()
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("LOOP_NOW_PLUGIN_HOST_ENTRY is not set")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	var specs []pluginhost.Spec
	for _, p := range plugins {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(p.code), 0o600); err != nil {
			t.Fatal(err)
		}
		orEmpty := func(v []string) []string {
			if v == nil {
				return []string{}
			}
			return v
		}
		actions, schedules := p.actions, p.schedules
		if actions == nil {
			actions = []pluginmanifest.Action{}
		}
		if schedules == nil {
			schedules = []pluginmanifest.Schedule{}
		}
		accounts := []any{}
		for _, k := range p.accounts {
			accounts = append(accounts, map[string]any{"kind": k, "label": k, "multiple": true, "fields": map[string]any{"type": "object"}})
		}
		load, _ := json.Marshal(map[string]any{
			"id": p.id, "version": "1.0.0", "root": dir, "entry": filepath.Join(dir, "index.ts"), "dataDir": t.TempDir(),
			"manifest": map[string]any{"events": orEmpty(p.events), "permissions": orEmpty(p.permissions), "failClosed": p.failClosed,
				"actions": actions, "channels": []any{}, "schedules": schedules, "http": p.http, "configSchema": map[string]any{}, "accounts": accounts},
			"config": map[string]any{"values": map[string]any{}, "secrets": map[string]any{}, "projects": map[string]any{}},
			"token":  "lnp_test_token", "apiUrl": "http://127.0.0.1:9/api/v1",
		})
		specs = append(specs, pluginhost.Spec{ID: p.id, Events: p.events, Permissions: p.permissions, FailClosed: p.failClosed, Schedules: p.schedules,
			AccountKinds: p.accounts, Load: load})
	}
	return newHostEnv(t, "", specs, func(o *pluginhost.Options) {
		// Only pass the necessary environment variables, the same as when the server assembles the plugin host
		// (app.assemblePlugins).
		o.Command = []string{node, entry}
		o.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + o.Dir, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT=" + o.Dir, "LOOP_NOW_API_URL=http://127.0.0.1:9/api/v1"}
		o.ReadyTimeout, o.LoadTimeout = 10*time.Second, 10*time.Second
		if tune != nil {
			tune(o)
		}
	})
}

// hostLog returns the plugin host log, one plugin:level:content per line, in write order.
func (e *hostEnv) hostLog() string {
	return e.scalar(`SELECT string_agg(coalesce(plugin_id,'-')||':'||level||':'||line, ' | ' ORDER BY id) FROM plugin_host_log`)
}

func TestRealHostLoadsDeliversEventsAndKeepsState(t *testing.T) {
	e := newRealHostEnv(t, []realPlugin{
		{id: "alpha", events: []string{"inbox.received", "agent.created", "run.ended"}, permissions: []string{"state"}, code: `export default function (api) {
  api.lifecycle.on('start', async () => {
    await api.state.set('probe/a', { n: 1 })
    api.log.info('state ' + JSON.stringify(await api.state.get('probe/a')) + ' ' + JSON.stringify(await api.state.list('probe/')))
  })
  api.on('inbox.received', (ev) => { api.log.info('ev ' + ev.type + ' ' + ev.id + ' ' + ev.excerpt + ' ' + ev.projectId) })
  api.on('agent.created', (ev) => { api.log.info('ev ' + ev.type + ' ' + ev.id); throw new Error('boom') })
  api.on('run.ended', (ev) => { api.log.info('ev ' + ev.type + ' ' + ev.id); console.error('to stderr') })
}`},
		{id: "beta", events: []string{"inbox.received"}, code: `export default function (api) { api.on('run.ended', () => {}) }`},
	}, nil)
	// Recent event times: events older than EventRetention (30 days) are pruned when the host starts.
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	late := e.addEvent("run.ended", base.Add(2*time.Second), "alpha")
	failing := e.addEvent("agent.created", base.Add(time.Second), "alpha")
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
		t.Errorf("alpha status: %s", got)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='beta'`); !strings.HasPrefix(got, "error|") || !strings.Contains(got, "run.ended") {
		t.Errorf("beta subscribes to an event outside its manifest and should fail to load with the reason: %s", got)
	}
	if got := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE plugin_id='beta' AND event_id=$1`, early); got != "pending" {
		t.Errorf("events of a plugin that is not loaded should stay pending: %s", got)
	}
	if got := e.scalar(`SELECT last_error FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); !strings.Contains(got, "boom") {
		t.Errorf("failure reason: %s", got)
	}
	if got := e.scalar(`SELECT value FROM plugin_state WHERE plugin_id='alpha' AND project_id IS NULL AND key='probe/a'`); got != `{"n":1}` {
		t.Errorf("state value: %s", got)
	}
	e.eventually("host log", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, `alpha:info:state {"n":1}`) && strings.Contains(s, "to stderr") && strings.Contains(s, "beta:error:Could not load the plugin"), s
	})
	logs := e.hostLog()
	if !strings.Contains(logs, "alpha:info:ev inbox.received "+early+" hi null") {
		t.Errorf("event object should carry the fixed and type-specific fields, fixed fields taking precedence: %s", logs)
	}
	order := regexp.MustCompile(`alpha:info:ev [a-z.]+ ([0-9A-Z]{26})`).FindAllStringSubmatch(logs, -1)
	var ids []string
	for _, m := range order {
		ids = append(ids, m[1])
	}
	if strings.Join(ids, ",") != strings.Join([]string{early, failing, late, failing}, ",") {
		t.Errorf("delivery order (by event time, failed ones redelivered later): %v, want %s %s %s %s", ids, early, failing, late, failing)
	}
	if st := e.sup.Status(); st.State != pluginhost.StateRunning || st.HostVersion == "" || len(st.Loaded) != 1 || st.Loaded[0] != "alpha" || st.LastCrash != nil {
		t.Errorf("host status: %+v", st)
	}
	t.Logf("host log: %s", logs)
}

func TestRealHostRestartPolicyAndCrashReport(t *testing.T) {
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", events: []string{"agent.created", "inbox.received"}, code: `export default function (api) {
  api.lifecycle.on('start', () => api.log.info('start'))
  api.on('agent.created', () => process.exit(5))
  api.on('inbox.received', () => {})
}`}}, func(o *pluginhost.Options) {
		o.Backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	})
	boom := e.addEvent("agent.created", time.Now(), "alpha")
	e.sup.Start(false)
	e.eventually("the host to stop restarting", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateStopped, st.State
	})
	st := e.sup.Status()
	if len(st.RecentExits) != 3 || st.LastCrash == nil || st.LastCrash.Last == nil || st.LastCrash.Last.Plugin != "alpha" || st.LastCrash.Last.EventID != boom {
		t.Fatalf("crash record: %+v %+v", st, st.LastCrash)
	}
	if !strings.Contains(st.LastCrash.Error, "exit status 5") || len(st.LastCrash.InFlight) != 1 {
		t.Errorf("crash reason and in-flight requests: %+v", st.LastCrash)
	}
	// First recorded as stopped, then the plugins are written as host stopped.
	e.eventually("the plugin marked host stopped", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`)
		return s == "host_stopped", s
	})
	e.exec(`UPDATE plugin_event_deliveries SET status='delivered' WHERE event_id=$1`, boom)
	e.sup.Restart()
	e.eventually("manual restart", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.RecentExits) == 0, st.State
	})
	ok := e.addEvent("inbox.received", time.Now(), "alpha")
	e.sup.Notify()
	e.eventually("normal delivery after restart", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE event_id=$1`, ok)
		return s == "delivered", s
	})
	if n := strings.Count(e.hostLog(), "alpha:info:start"); n != 4 {
		t.Errorf("should load once before each of the three crashes and once more after the manual restart: %d", n)
	}
}

func TestRealHostDisableReloadAndUnload(t *testing.T) {
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", events: []string{"inbox.received"}, code: `let loads = 0
export default function (api) {
  loads++
  api.lifecycle.on('start', () => api.log.info('start ' + loads))
  api.lifecycle.on('stop', () => api.log.info('stop ' + loads))
  api.config.onChange(() => api.log.info('config changed'))
}`}}, nil)
	e.sup.Start(true)
	e.eventually("disabled at start, the host not launched", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled, st.State
	})
	e.sup.SetDisabled(false)
	e.eventually("running after enable", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 1, st.State
	})
	e.sup.SetDisabled(true)
	e.eventually("disabled", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled && st.PID == 0, st.State
	})
	if st := e.sup.Status(); st.LastCrash != nil || len(st.RecentExits) != 0 {
		t.Errorf("disabling is not a crash: %+v", st)
	}
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "installed" {
		t.Errorf("plugin should go back to installed after disabling: %s", got)
	}
	e.eventually("stop to run on disable", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "alpha:info:stop"), s })
	e.sup.SetDisabled(false)
	e.eventually("re-enabled", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 1, st.State
	})
	ctx := context.Background()
	if err := e.sup.Reload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{"config": map[string]any{"values": map[string]any{"x": 1}, "secrets": map[string]any{}, "projects": map[string]any{}}}); err != nil {
		t.Errorf("config change: %v", err)
	}
	if err := e.sup.Unload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{}); err != pluginhost.ErrNotLoaded {
		t.Errorf("want ErrNotLoaded for a plugin that is not loaded, got %v", err)
	}
	e.eventually("the lifecycle log", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, "config changed") && strings.Count(s, "alpha:info:stop") == 3, s
	})
	var seq []string
	for _, m := range regexp.MustCompile(`alpha:info:(start|stop) \d+`).FindAllString(e.hostLog(), -1) {
		seq = append(seq, strings.TrimPrefix(m, "alpha:info:"))
	}
	// Every load runs the module again, so the counter in the module starts at 1 each time: enable, disable,
	// re-enable, Reload (unload then load), unload.
	if strings.Join(seq, ",") != "start 1,stop 1,start 1,stop 1,start 1,stop 1" {
		t.Errorf("lifecycle order: %v", seq)
	}
}

func TestRealHostCallPolicies(t *testing.T) {
	events := []string{"run.before_start", "sleep.before_schedule", "console.command"}
	e := newRealHostEnv(t, []realPlugin{
		{id: "a", events: events, code: `export default function (api) {
  api.on('run.before_start', () => ({}))
  api.on('sleep.before_schedule', () => ({ patch: { durationSeconds: 600 } }))
  api.on('console.command', () => new Promise(() => {}))
}`},
		{id: "b", events: events, code: `export default function (api) {
  api.on('run.before_start', () => new Promise(() => {}))
  api.on('sleep.before_schedule', (ev) => { api.log.info('b saw ' + ev.durationSeconds); return { patch: { durationSeconds: 900, note: 'b' } } })
  api.on('console.command', () => ({ handled: false }))
}`},
		{id: "c", events: events, code: `export default function (api) {
  api.on('run.before_start', () => ({ block: true, reason: 'Blocked by c.' }))
  api.on('sleep.before_schedule', () => new Promise(() => {}))
  api.on('console.command', () => ({ handled: true, by: 'c' }))
}`},
		{id: "d", events: []string{"node.before_submit"}, failClosed: true, code: `export default function (api) { api.on('node.before_submit', () => { throw new Error('check failed') }) }`},
		{id: "f", events: []string{"task.before_start"}, failClosed: true, code: `export default function () { throw new Error('cannot load') }`},
	}, func(o *pluginhost.Options) { o.CallTimeout = 300 * time.Millisecond })
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
		t.Errorf("intercept: b times out and allows, c blocks: %+v %v %s", out, err, time.Since(start))
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), func(id string) bool { return id != "c" }); out.Blocked {
		t.Errorf("should allow once c is excluded: %+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("node.before_submit"), nil); !out.Blocked || out.Plugin != "d" || !strings.Contains(out.Reason, "check failed") {
		t.Errorf("failClosed plugin should block when it errors: %+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); !out.Blocked || out.Plugin != "f" || !strings.Contains(out.Reason, "the plugin is not loaded") {
		t.Errorf("failClosed plugin should block when it is not loaded: %+v", out)
	}
	out, err = e.sup.Call(ctx, pluginhost.Modify, ev("sleep.before_schedule"), nil)
	if err != nil || out.Blocked || out.Patch["durationSeconds"] != float64(900) || out.Patch["note"] != "b" || len(out.Patch) != 2 {
		t.Errorf("modify: %+v %v", out, err)
	}
	e.eventually("b to see the change of a", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "b:info:b saw 600"), s })
	out, err = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), nil)
	if err != nil || !out.Handled || out.Plugin != "c" || !strings.Contains(string(out.Value), `"by":"c"`) {
		t.Errorf("claim: a times out, b does not handle, c claims: %+v %v", out, err)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), func(id string) bool { return id != "c" }); out.Handled {
		t.Errorf("no plugin should claim: %+v", out)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='f'`); !strings.Contains(got, "cannot load") {
		t.Errorf("load failure reason of f: %s", got)
	}
}

// Phase three: action, http and schedule (expected to fail until the plugin session has hooked them up).
func TestRealHostActionsHTTPAndSchedules(t *testing.T) {
	var now, reads atomic.Int64
	base := time.Date(2026, 10, 27, 9, 0, 30, 0, time.Local)
	now.Store(base.UnixNano())
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", http: true,
		actions:   []pluginmanifest.Action{{ID: "resend", Label: pluginmanifest.PlainText("Resend"), Where: []string{"agent"}}},
		schedules: []pluginmanifest.Schedule{{ID: "sweep", Cron: "* * * * *"}},
		code: `export default function (api) {
  api.actions.register('resend', async (input, ctx) => ({ notice: 'resent ' + input.note }))
  api.http.handle(async (req) => new Response('echo ' + req.method + ' ' + new URL(req.url).pathname + ' ' + (await req.text()), { status: 201, headers: { 'x-plugin': 'alpha' } }))
  api.schedule.on('sweep', async () => { api.log.info('swept') })
}`}}, func(o *pluginhost.Options) {
		o.Now = func() time.Time { reads.Add(1); return time.Unix(0, now.Load()) }
		o.ScheduleTick = 10 * time.Millisecond
	})
	e.sup.Start(false)
	// When the schedule goroutine starts, it reads the clock once to record the load minute, then once per tick: only
	// after two extra reads can the clock be moved without it being taken as the load minute.
	e.eventually("loading", func() (bool, string) {
		st := e.sup.Status()
		return len(st.Loaded) == 1 && reads.Load() >= 3, e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='alpha'`)
	})
	ctx := context.Background()
	raw, err := e.sup.Request(ctx, pluginhost.TypeAction, "alpha", map[string]any{"actionId": "resend", "where": map[string]any{"agentId": "01a17d40-0000-7000-8000-000000000000"}, "input": map[string]any{"note": "x"}, "timeoutMs": 5000}, 5*time.Second)
	if err != nil || !strings.Contains(string(raw), `"notice":"resent x"`) {
		t.Errorf("action: %s %v", raw, err)
	}
	raw, err = e.sup.Request(ctx, pluginhost.TypeHTTP, "alpha", map[string]any{"via": "external", "method": "POST", "path": "/cb", "query": "a=1",
		"headers": map[string][]string{"content-type": {"text/plain"}}, "body": []byte("hello"), "timeoutMs": 5000}, 5*time.Second)
	var res struct {
		Status  int                 `json:"status"`
		Headers map[string][]string `json:"headers"`
		Body    []byte              `json:"body"`
	}
	_ = json.Unmarshal(raw, &res)
	if err != nil || res.Status != 201 || string(res.Body) != "echo POST /cb hello" || res.Headers["x-plugin"] == nil {
		t.Errorf("http: %s %v", raw, err)
	}
	now.Store(base.Add(time.Minute).UnixNano())
	e.eventually("schedule", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "alpha:info:swept"), s })
}

// accounts_list: in a project that has the plugin enabled, the plugin reads the accounts of a kind in the order they
// were added (values include secret fields); a project without it enabled is rejected.
func TestRealHostAccountsList(t *testing.T) {
	ctx := context.Background()
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", permissions: []string{"accounts.read"}, accounts: []string{"mail"}, code: ""}}, nil)
	dc, err := e.st.EnsureDefaults(ctx, "Default organization", "Default project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.st.CreateProject(ctx, dc.Organization.ID, "Other project")
	if err != nil {
		t.Fatal(err)
	}
	project, other := dc.Project.ID, second.ID
	e.exec(`INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,'alpha',true)`, project)
	for _, name := range []string{"on-call", "backup"} {
		if _, err := e.st.CreatePluginAccount(ctx, store.PluginAccount{PluginID: "alpha", ProjectID: project, Kind: "mail", Name: name,
			Values: map[string]any{"addr": name + "@x", "password": "pw-" + name}}, false); err != nil {
			t.Fatal(err)
		}
	}
	// The plugin code needs the project ID: substitute it into the written index.ts.
	writeCode(t, e, "alpha", `export default function (api) {
  api.lifecycle.on('start', async () => {
    const list = await api.accounts.list('`+project.String()+`', 'mail')
    api.log.info('accounts ' + JSON.stringify(list.map((a) => [a.name, a.kind, a.values.password])))
    try { await api.accounts.list('`+other.String()+`', 'mail') } catch (err) { api.log.info('denied ' + err.message) }
  })
}`)
	e.sup.Start(false)
	e.eventually("reading accounts", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, "alpha:info:denied"), s
	})
	logs := e.hostLog()
	if !strings.Contains(logs, `alpha:info:accounts [["on-call","mail","pw-on-call"],["backup","mail","pw-backup"]]`) || !strings.Contains(logs, "the plugin is not enabled in this project") {
		t.Fatalf("host log: %s", logs)
	}
}

// writeCode rewrites the index.ts of a registered plugin (the entry in the load payload).
func writeCode(t *testing.T, e *hostEnv, id, code string) {
	t.Helper()
	for _, s := range e.specs {
		if s.ID != id {
			continue
		}
		var load struct {
			Entry string `json:"entry"`
		}
		if err := json.Unmarshal(s.Load, &load); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(load.Entry, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no plugin %s", id)
}
