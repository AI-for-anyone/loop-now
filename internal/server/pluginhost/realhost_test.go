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

// 真实宿主：设置 LOOP_NOW_PLUGIN_HOST_ENTRY（构建好的 plugins/host/dist/main.js）时，用 node 运行它，
// 插件是写在临时目录里的 index.ts，load 载荷按协议的形状给出。场景与假宿主的测试相同，观察都经数据库与宿主日志。

type realPlugin struct {
	id          string
	events      []string
	permissions []string
	failClosed  bool
	schedules   []pluginmanifest.Schedule
	http        bool
	actions     []pluginmanifest.Action
	accounts    []string // 账号种类
	code        string   // index.ts
}

// newRealHostEnv 准备以真实宿主运行的监护（尚未 Start）；没有设置宿主入口时跳过。
func newRealHostEnv(t *testing.T, plugins []realPlugin, tune func(*pluginhost.Options)) *hostEnv {
	t.Helper()
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("没有设置 LOOP_NOW_PLUGIN_HOST_ENTRY")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("找不到 node")
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
		// 与服务端装配宿主时一样只给必要的环境变量（app.assemblePlugins）。
		o.Command = []string{node, entry}
		o.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + o.Dir, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT=" + o.Dir, "LOOP_NOW_API_URL=http://127.0.0.1:9/api/v1"}
		o.ReadyTimeout, o.LoadTimeout = 10*time.Second, 10*time.Second
		if tune != nil {
			tune(o)
		}
	})
}

// hostLog 返回宿主日志，每行 插件:级别:内容，按写入顺序。
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
	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	late := e.addEvent("run.ended", base.Add(2*time.Second), "alpha")
	failing := e.addEvent("agent.created", base.Add(time.Second), "alpha")
	early := e.addEvent("inbox.received", base, "alpha", "beta")

	e.sup.Start(false)
	e.eventually("投递完成", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(event_id||'='||status||'/'||attempts, ',' ORDER BY event_id) FROM plugin_event_deliveries WHERE plugin_id='alpha'`)
		return strings.Count(s, "delivered/1") == 2 && strings.Contains(s, failing+"=failed/2"), s
	})
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='alpha'`); got != "active|" {
		t.Errorf("alpha 状态：%s", got)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='beta'`); !strings.HasPrefix(got, "error|") || !strings.Contains(got, "run.ended") {
		t.Errorf("beta 订阅了清单外的事件，应加载失败并说明原因：%s", got)
	}
	if got := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE plugin_id='beta' AND event_id=$1`, early); got != "pending" {
		t.Errorf("未加载的插件的事件应保留待投：%s", got)
	}
	if got := e.scalar(`SELECT last_error FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); !strings.Contains(got, "boom") {
		t.Errorf("失败原因：%s", got)
	}
	if got := e.scalar(`SELECT value::text FROM plugin_state WHERE plugin_id='alpha' AND project_id IS NULL AND key='probe/a'`); got != `{"n": 1}` {
		t.Errorf("状态：%s", got)
	}
	e.eventually("宿主日志", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, `alpha:info:state {"n":1}`) && strings.Contains(s, "to stderr") && strings.Contains(s, "beta:error:加载失败"), s
	})
	logs := e.hostLog()
	if !strings.Contains(logs, "alpha:info:ev inbox.received "+early+" hi null") {
		t.Errorf("事件对象应带固定字段与类型字段，固定字段优先：%s", logs)
	}
	order := regexp.MustCompile(`alpha:info:ev [a-z.]+ ([0-9A-Z]{26})`).FindAllStringSubmatch(logs, -1)
	var ids []string
	for _, m := range order {
		ids = append(ids, m[1])
	}
	if strings.Join(ids, ",") != strings.Join([]string{early, failing, late, failing}, ",") {
		t.Errorf("投递顺序（按事件时间，失败的稍后重投）：%v，期望 %s %s %s %s", ids, early, failing, late, failing)
	}
	if st := e.sup.Status(); st.State != pluginhost.StateRunning || st.HostVersion == "" || len(st.Loaded) != 1 || st.Loaded[0] != "alpha" || st.LastCrash != nil {
		t.Errorf("宿主状态：%+v", st)
	}
	t.Logf("宿主日志：%s", logs)
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
	e.eventually("宿主停止重启", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateStopped, st.State
	})
	st := e.sup.Status()
	if len(st.RecentExits) != 3 || st.LastCrash == nil || st.LastCrash.Last == nil || st.LastCrash.Last.Plugin != "alpha" || st.LastCrash.Last.EventID != boom {
		t.Fatalf("崩溃记录：%+v %+v", st, st.LastCrash)
	}
	if !strings.Contains(st.LastCrash.Error, "exit status 5") || len(st.LastCrash.InFlight) != 1 {
		t.Errorf("崩溃原因与未完成的请求：%+v", st.LastCrash)
	}
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "host_stopped" {
		t.Errorf("插件应标为宿主已停止：%s", got)
	}
	e.exec(`UPDATE plugin_event_deliveries SET status='delivered' WHERE event_id=$1`, boom)
	e.sup.Restart()
	e.eventually("人工重启", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.RecentExits) == 0, st.State
	})
	ok := e.addEvent("inbox.received", time.Now(), "alpha")
	e.sup.Notify()
	e.eventually("重启后照常投递", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE event_id=$1`, ok)
		return s == "delivered", s
	})
	if n := strings.Count(e.hostLog(), "alpha:info:start"); n != 4 {
		t.Errorf("三次崩溃前各加载一次，人工重启后再加载一次：%d", n)
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
	time.Sleep(100 * time.Millisecond)
	if st := e.sup.Status(); st.State != pluginhost.StateDisabled {
		t.Fatalf("启动时已停用，不应拉起宿主：%+v", st)
	}
	e.sup.SetDisabled(false)
	e.eventually("启用后运行", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 1, st.State
	})
	e.sup.SetDisabled(true)
	e.eventually("停用", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled && st.PID == 0, st.State
	})
	if st := e.sup.Status(); st.LastCrash != nil || len(st.RecentExits) != 0 {
		t.Errorf("停用不是崩溃：%+v", st)
	}
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "installed" {
		t.Errorf("停用后插件回到已安装：%s", got)
	}
	e.eventually("停用时运行 stop", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "alpha:info:stop"), s })
	e.sup.SetDisabled(false)
	e.eventually("重新启用", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 1, st.State
	})
	ctx := context.Background()
	if err := e.sup.Reload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{"config": map[string]any{"values": map[string]any{"x": 1}, "secrets": map[string]any{}, "projects": map[string]any{}}}); err != nil {
		t.Errorf("配置变化：%v", err)
	}
	if err := e.sup.Unload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{}); err != pluginhost.ErrNotLoaded {
		t.Errorf("未加载的插件：%v", err)
	}
	e.eventually("生命周期日志", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, "config changed") && strings.Count(s, "alpha:info:stop") == 3, s
	})
	var seq []string
	for _, m := range regexp.MustCompile(`alpha:info:(start|stop) \d+`).FindAllString(e.hostLog(), -1) {
		seq = append(seq, strings.TrimPrefix(m, "alpha:info:"))
	}
	// 每次加载都重新执行模块，模块里的计数每次从 1 开始：启用、停用、重新启用、Reload（卸载再加载）、卸载。
	if strings.Join(seq, ",") != "start 1,stop 1,start 1,stop 1,start 1,stop 1" {
		t.Errorf("生命周期顺序：%v", seq)
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
  api.on('run.before_start', () => ({ block: true, reason: '被 c 阻止' }))
  api.on('sleep.before_schedule', () => new Promise(() => {}))
  api.on('console.command', () => ({ handled: true, by: 'c' }))
}`},
		{id: "d", events: []string{"node.before_submit"}, failClosed: true, code: `export default function (api) { api.on('node.before_submit', () => { throw new Error('检查出错') }) }`},
		{id: "f", events: []string{"task.before_start"}, failClosed: true, code: `export default function () { throw new Error('加载不了') }`},
	}, func(o *pluginhost.Options) { o.CallTimeout = 300 * time.Millisecond })
	e.sup.Start(false)
	e.eventually("加载", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.Loaded) == 4, strings.Join(st.Loaded, ",")
	})
	ctx := context.Background()
	ev := func(typ string) map[string]any {
		return map[string]any{"id": "01J000000000000000000000AA", "type": typ, "durationSeconds": 60}
	}
	start := time.Now()
	out, err := e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), nil)
	if err != nil || !out.Blocked || out.Plugin != "c" || out.Reason != "被 c 阻止" || time.Since(start) < 300*time.Millisecond {
		t.Errorf("拦截：b 超时放行，c 阻止：%+v %v %s", out, err, time.Since(start))
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), func(id string) bool { return id != "c" }); out.Blocked {
		t.Errorf("排除 c 后应放行：%+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("node.before_submit"), nil); !out.Blocked || out.Plugin != "d" || !strings.Contains(out.Reason, "检查出错") {
		t.Errorf("failClosed 的插件出错时应阻止：%+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); !out.Blocked || out.Plugin != "f" || !strings.Contains(out.Reason, "插件未加载") {
		t.Errorf("failClosed 的插件未加载时应阻止：%+v", out)
	}
	out, err = e.sup.Call(ctx, pluginhost.Modify, ev("sleep.before_schedule"), nil)
	if err != nil || out.Blocked || out.Patch["durationSeconds"] != float64(900) || out.Patch["note"] != "b" || len(out.Patch) != 2 {
		t.Errorf("修改：%+v %v", out, err)
	}
	e.eventually("b 看到 a 的修改", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "b:info:b saw 600"), s })
	out, err = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), nil)
	if err != nil || !out.Handled || out.Plugin != "c" || !strings.Contains(string(out.Value), `"by":"c"`) {
		t.Errorf("认领：a 超时、b 未处理、c 认领：%+v %v", out, err)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), func(id string) bool { return id != "c" }); out.Handled {
		t.Errorf("没有插件认领：%+v", out)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='f'`); !strings.Contains(got, "加载不了") {
		t.Errorf("f 的加载失败原因：%s", got)
	}
}

// 第三阶段：action、http 与 schedule（插件会话接上之前预期失败）。
func TestRealHostActionsHTTPAndSchedules(t *testing.T) {
	var now atomic.Int64
	base := time.Date(2026, 10, 27, 9, 0, 30, 0, time.Local)
	now.Store(base.UnixNano())
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", http: true,
		actions:   []pluginmanifest.Action{{ID: "resend", Label: "重发", Where: []string{"agent"}}},
		schedules: []pluginmanifest.Schedule{{ID: "sweep", Cron: "* * * * *"}},
		code: `export default function (api) {
  api.actions.register('resend', async (input, ctx) => ({ notice: 'resent ' + input.note }))
  api.http.handle(async (req) => new Response('echo ' + req.method + ' ' + new URL(req.url).pathname + ' ' + (await req.text()), { status: 201, headers: { 'x-plugin': 'alpha' } }))
  api.schedule.on('sweep', async () => { api.log.info('swept') })
}`}}, func(o *pluginhost.Options) {
		o.Now = func() time.Time { return time.Unix(0, now.Load()) }
		o.ScheduleTick = 10 * time.Millisecond
	})
	e.sup.Start(false)
	e.eventually("加载", func() (bool, string) {
		st := e.sup.Status()
		return len(st.Loaded) == 1, e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='alpha'`)
	})
	ctx := context.Background()
	raw, err := e.sup.Request(ctx, pluginhost.TypeAction, "alpha", map[string]any{"actionId": "resend", "where": map[string]any{"agentId": "01a17d40-0000-7000-8000-000000000000"}, "input": map[string]any{"note": "x"}, "timeoutMs": 5000}, 5*time.Second)
	if err != nil || !strings.Contains(string(raw), `"notice":"resent x"`) {
		t.Errorf("action：%s %v", raw, err)
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
		t.Errorf("http：%s %v", raw, err)
	}
	now.Store(base.Add(time.Minute).UnixNano())
	e.eventually("schedule", func() (bool, string) { s := e.hostLog(); return strings.Contains(s, "alpha:info:swept"), s })
}

// accounts_list：插件在启用了它的项目里按添加先后读到某种账号（values 含 secret 字段）；没有启用的项目被拒绝。
func TestRealHostAccountsList(t *testing.T) {
	ctx := context.Background()
	e := newRealHostEnv(t, []realPlugin{{id: "alpha", permissions: []string{"accounts.read"}, accounts: []string{"mail"}, code: ""}}, nil)
	dc, err := e.st.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.st.CreateProject(ctx, dc.Organization.ID, "别的项目")
	if err != nil {
		t.Fatal(err)
	}
	project, other := dc.Project.ID, second.ID
	e.exec(`INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,'alpha',true)`, project)
	for _, name := range []string{"值班", "备份"} {
		if _, err := e.st.CreatePluginAccount(ctx, store.PluginAccount{PluginID: "alpha", ProjectID: project, Kind: "mail", Name: name,
			Values: map[string]any{"addr": name + "@x", "password": "pw-" + name}}, false); err != nil {
			t.Fatal(err)
		}
	}
	// 插件代码要用到项目 ID：在写好的 index.ts 里替换。
	writeCode(t, e, "alpha", `export default function (api) {
  api.lifecycle.on('start', async () => {
    const list = await api.accounts.list('`+project.String()+`', 'mail')
    api.log.info('accounts ' + JSON.stringify(list.map((a) => [a.name, a.kind, a.values.password])))
    try { await api.accounts.list('`+other.String()+`', 'mail') } catch (err) { api.log.info('denied ' + err.message) }
  })
}`)
	e.sup.Start(false)
	e.eventually("读取账号", func() (bool, string) {
		s := e.hostLog()
		return strings.Contains(s, "alpha:info:denied"), s
	})
	logs := e.hostLog()
	if !strings.Contains(logs, `alpha:info:accounts [["值班","mail","pw-值班"],["备份","mail","pw-备份"]]`) || !strings.Contains(logs, "插件没有在这个项目里启用") {
		t.Fatalf("宿主日志：%s", logs)
	}
}

// writeCode 改写已登记插件的 index.ts（load 载荷里的 entry）。
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
	t.Fatalf("没有插件 %s", id)
}
