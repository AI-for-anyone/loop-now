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
	"loop-now/internal/testutil/pgtest"
)

type hostEnv struct {
	t       *testing.T
	st      *store.Store
	journal string
	specs   []pluginhost.Spec
	sup     *pluginhost.Supervisor
}

// newHostEnv 建一个迁移好的测试库，登记 specs 里的插件，并准备以假宿主运行的监护（尚未 Start）。
func newHostEnv(t *testing.T, mode string, specs []pluginhost.Spec, tune func(*pluginhost.Options)) *hostEnv {
	t.Helper()
	e := &hostEnv{t: t, st: pgtest.NewStore(t), journal: filepath.Join(t.TempDir(), "journal.jsonl"), specs: specs}
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
			t.Errorf("停止宿主监护：%v", err)
		}
	})
	return e
}

func (e *hostEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s：%v", sql, err)
	}
}

func (e *hostEnv) scalar(sql string, args ...any) string {
	e.t.Helper()
	var v *string
	if err := e.st.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		e.t.Fatalf("%s：%v", sql, err)
	}
	if v == nil {
		return "<nil>"
	}
	return *v
}

// eventually 在 10 秒内反复检查 cond，返回 cond 最后一次的说明。
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
	e.t.Fatalf("等待%s超时：%s", what, last)
}

// entries 读出假宿主的记录。
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

// 宿主就绪后按顺序加载插件；加载失败的插件进入错误状态。观察类事件按事件时间逐条投递，ack 后标为完成，
// 插件报错的按 Backoff 重投、用尽后标为 failed，不挡住后面的事件。state_* 经 Go 写库，标准错误与 log 进宿主日志。
func TestHostLoadsDeliversEventsAndKeepsState(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{
		spec("alpha", map[string]any{"stateProbe": true, "failEvents": []string{"x.fail"}}, "inbox.received", "x.fail", "run.ended"),
		spec("beta", map[string]any{"fail": "清单与代码订阅不符"}, "inbox.received"),
	}, nil)
	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	// 写入顺序与事件时间相反：投递按事件时间。
	late := e.addEvent("run.ended", base.Add(2*time.Second), "alpha")
	failing := e.addEvent("x.fail", base.Add(time.Second), "alpha")
	early := e.addEvent("inbox.received", base, "alpha", "beta")

	e.sup.Start(false)
	// alpha 加载后就开始投递，这时 beta 可能还没加载完；全部加载完（状态都已写库）宿主才进入运行中。
	e.eventually("加载与投递完成", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(event_id||'='||status||'/'||attempts, ',' ORDER BY event_id) FROM plugin_event_deliveries WHERE plugin_id='alpha'`)
		st := e.sup.Status().State
		return st == pluginhost.StateRunning && strings.Count(s, "delivered/1") == 2 && strings.Contains(s, failing+"=failed/2"), st + " " + s
	})
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='alpha'`); got != "active|" {
		t.Fatalf("alpha 状态：%s", got)
	}
	if got := e.scalar(`SELECT status||'|'||coalesce(last_error,'') FROM plugins WHERE id='beta'`); got != "error|清单与代码订阅不符" {
		t.Fatalf("beta 状态：%s", got)
	}
	if got := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE plugin_id='beta' AND event_id=$1`, early); got != "pending" {
		t.Fatalf("未加载的插件的事件应保留待投：%s", got)
	}
	var order []string
	for _, m := range e.entries() {
		if m["type"] == "event" && m["plugin"] == "alpha" {
			order = append(order, m["eventId"].(string))
			if ev := m["event"].(map[string]any); ev["id"] != m["eventId"] || ev["excerpt"] != "hi" || ev["projectId"] != nil {
				t.Fatalf("事件对象应带固定字段与类型字段，固定字段优先：%v", ev)
			}
		}
	}
	if len(order) != 4 || order[0] != early || order[1] != failing || order[3] != failing || order[2] != late {
		t.Fatalf("投递顺序（按事件时间，失败的稍后重投）：%v，期望 %s %s %s %s", order, early, failing, late, failing)
	}
	if got := e.scalar(`SELECT count(*)::text||'/'||count(duration_ms)::text FROM plugin_event_deliveries WHERE plugin_id='alpha' AND status='delivered'`); got != "2/2" {
		t.Fatalf("送达的投递应记下处理时长：%s", got)
	}
	if got := e.scalar(`SELECT coalesce(duration_ms::text,'null') FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); got != "null" {
		t.Fatalf("失败的投递没有处理时长：%s", got)
	}
	if got := e.scalar(`SELECT last_error FROM plugin_event_deliveries WHERE plugin_id='alpha' AND event_id=$1`, failing); got != "boom" {
		t.Fatalf("失败原因：%s", got)
	}
	if got := e.scalar(`SELECT value::text FROM plugin_state WHERE plugin_id='alpha' AND project_id IS NULL AND key='probe/a'`); got != `{"n": 1}` {
		t.Fatalf("状态：%s", got)
	}
	e.eventually("宿主日志", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(coalesce(plugin_id,'-')||':'||level||':'||line, ' | ' ORDER BY id) FROM plugin_host_log`)
		return strings.Contains(s, "alpha:warn:loaded") && strings.Contains(s, `alpha:info:state {"n":1} [{"key":"probe/a","value":{"n":1}}]`) &&
			strings.Contains(s, "-:warn:fake host booting") && strings.Contains(s, "beta:error:加载失败：清单与代码订阅不符"), s
	})
	st := e.sup.Status()
	if st.State != pluginhost.StateRunning || st.HostVersion != "fake-1" || len(st.Loaded) != 1 || st.Loaded[0] != "alpha" || st.LastCrash != nil {
		t.Fatalf("宿主状态：%+v", st)
	}
}

// 宿主意外退出后重启，未确认的事件在重启后重投（至少一次）；5 分钟内第 3 次退出后不再重启，
// 插件标为宿主已停止，崩溃记录说明当时在处理哪个插件的哪个事件。人工重启后恢复。
func TestHostRestartPolicyAndCrashReport(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", map[string]any{"crashEvents": []string{"boom"}}, "boom", "inbox.received")},
		func(o *pluginhost.Options) {
			o.Backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
		})
	boom := e.addEvent("boom", time.Now(), "alpha")
	e.sup.Start(false)
	e.eventually("宿主停止重启", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateStopped, st.State
	})
	st := e.sup.Status()
	if len(st.RecentExits) != 3 || st.LastCrash == nil || st.LastCrash.Last == nil || st.LastCrash.Last.Plugin != "alpha" || st.LastCrash.Last.EventID != boom || st.LastCrash.Last.EventType != "boom" {
		t.Fatalf("崩溃记录：%+v %+v", st, st.LastCrash)
	}
	if !strings.Contains(st.LastCrash.Error, "exit status 5") || len(st.LastCrash.InFlight) != 1 {
		t.Fatalf("崩溃原因与未完成的请求：%+v", st.LastCrash)
	}
	if n := e.count("event", "alpha", "boom"); n != 3 {
		t.Fatalf("未确认的事件每次重启后都应重投：%d", n)
	}
	// 先记为已停止，再把插件写成宿主已停止。
	e.eventually("插件标为宿主已停止", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`)
		return s == "host_stopped", s
	})
	// 停止后不再拉起：过一会儿投递次数不变。
	time.Sleep(200 * time.Millisecond)
	if n := e.count("load", "alpha", ""); n != 3 {
		t.Fatalf("停止后不应再启动：加载了 %d 次", n)
	}
	e.exec(`UPDATE plugin_event_deliveries SET status='delivered' WHERE event_id=$1`, boom)
	e.sup.Restart()
	e.eventually("人工重启", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning && len(st.RecentExits) == 0, st.State
	})
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "active" {
		t.Fatalf("重启后插件应恢复：%s", got)
	}
	ok := e.addEvent("inbox.received", time.Now(), "alpha")
	e.sup.Notify()
	e.eventually("重启后照常投递", func() (bool, string) {
		s := e.scalar(`SELECT status FROM plugin_event_deliveries WHERE event_id=$1`, ok)
		return s == "delivered", s
	})
}

// 就绪超时与协议版本不符都算一次失败，按重启策略计数。
func TestHostReadyTimeoutAndProtocolMismatch(t *testing.T) {
	for _, c := range []struct{ mode, want string }{{"no-ready", "就绪"}, {"bad-protocol", "协议版本为 99"}, {"crash-after-ready", "exit status 3"}} {
		t.Run(c.mode, func(t *testing.T) {
			e := newHostEnv(t, c.mode, []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, func(o *pluginhost.Options) {
				o.ReadyTimeout = 300 * time.Millisecond
				o.RestartLimit = 2
			})
			if c.mode == "crash-after-ready" {
				e.addEvent("inbox.received", time.Now(), "alpha")
			}
			e.sup.Start(false)
			e.eventually("宿主停止重启", func() (bool, string) {
				st := e.sup.Status()
				return st.State == pluginhost.StateStopped, st.State
			})
			if st := e.sup.Status(); st.LastCrash == nil || !strings.Contains(st.LastCrash.Error, c.want) || len(st.RecentExits) != 2 {
				t.Fatalf("崩溃记录：%+v", st.LastCrash)
			}
		})
	}
}

// 全局停用让宿主退出、不计入崩溃，插件回到已安装；重新启用后拉起并加载。Reload 先卸载再加载，Unload 只卸载。
// 宿主收到 shutdown 后自行退出。
func TestHostDisableReloadAndUnload(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, nil)
	e.sup.Start(true)
	e.eventually("启动时停用", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled, st.State
	})
	if n := e.count("load", "", ""); n != 0 {
		t.Fatalf("启动时已停用，不应拉起宿主：加载了 %d 次", n)
	}
	e.sup.SetDisabled(false)
	e.eventually("启用后运行", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateRunning, st.State
	})
	e.sup.SetDisabled(true)
	st := e.sup.Status()
	if st.State != pluginhost.StateDisabled && st.State != pluginhost.StateRunning {
		t.Fatalf("停用中：%s", st.State)
	}
	e.eventually("停用", func() (bool, string) {
		st := e.sup.Status()
		return st.State == pluginhost.StateDisabled && st.PID == 0, st.State
	})
	if st := e.sup.Status(); st.LastCrash != nil || len(st.RecentExits) != 0 || e.count("shutdown", "", "") != 1 {
		t.Fatalf("停用不是崩溃，宿主应收到 shutdown：%+v", st)
	}
	if got := e.scalar(`SELECT status FROM plugins WHERE id='alpha'`); got != "installed" {
		t.Fatalf("停用后插件回到已安装：%s", got)
	}
	e.sup.SetDisabled(false)
	e.eventually("重新启用", func() (bool, string) {
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
		t.Fatalf("重新加载应先卸载再加载：%v", seq)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{"config": map[string]any{}}); err != nil || e.count("config_changed", "alpha", "") != 1 {
		t.Fatalf("配置变化：%v", err)
	}
	if err := e.sup.Unload(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if st := e.sup.Status(); len(st.Loaded) != 0 {
		t.Fatalf("卸载后不应再加载：%+v", st)
	}
	if err := e.sup.ConfigChanged(ctx, "alpha", map[string]any{}); err != pluginhost.ErrNotLoaded {
		t.Fatalf("未加载的插件：%v", err)
	}
}

// 同步调用按加载顺序进行：拦截类第一个 block 生效，超时或出错默认放行、failClosed 改为阻止（未加载的也一样）；
// 修改类每个插件看到前面的修改，patch 按字段合并，超时跳过；认领类第一个 handled 生效，超时当作未处理。
// include 排除的插件不参与；全局停用时没有插件参与。
func TestHostCallPolicies(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{
		spec("a", map[string]any{"intercept": "allow", "patch": map[string]any{"durationSeconds": 600}, "claim": "hang"}, "run.before_start", "sleep.before_schedule", "console.command"),
		spec("b", map[string]any{"intercept": "hang", "patch": map[string]any{"durationSeconds": 900, "note": "b"}, "claim": "no"}, "run.before_start", "sleep.before_schedule", "console.command"),
		spec("c", map[string]any{"intercept": "block", "modifyHang": true, "claim": "yes"}, "run.before_start", "sleep.before_schedule", "console.command"),
		{ID: "d", Events: []string{"node.before_submit"}, FailClosed: true, Load: json.RawMessage(`{"intercept":"error"}`)},
		{ID: "f", Events: []string{"task.before_start"}, FailClosed: true, Load: json.RawMessage(`{"fail":"加载不了"}`)},
	}, nil)
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
		t.Fatalf("拦截：b 超时放行，c 阻止：%+v %v %s", out, err, time.Since(start))
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("run.before_start"), func(id string) bool { return id != "c" }); out.Blocked {
		t.Fatalf("排除 c 后应放行：%+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("node.before_submit"), nil); !out.Blocked || out.Plugin != "d" || !strings.Contains(out.Reason, "检查出错") {
		t.Fatalf("failClosed 的插件出错时应阻止：%+v", out)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); !out.Blocked || out.Plugin != "f" || !strings.Contains(out.Reason, "插件未加载") {
		t.Fatalf("failClosed 的插件未加载时应阻止：%+v", out)
	}
	out, err = e.sup.Call(ctx, pluginhost.Modify, ev("sleep.before_schedule"), nil)
	if err != nil || out.Blocked || out.Patch["durationSeconds"] != float64(900) || out.Patch["note"] != "b" || len(out.Patch) != 2 {
		t.Fatalf("修改：%+v %v", out, err)
	}
	for _, m := range e.entries() {
		if m["type"] == "call" && m["plugin"] == "b" && m["kind"] == "modify" {
			if got := m["event"].(map[string]any)["durationSeconds"]; got != float64(600) {
				t.Fatalf("b 应看到 a 改过的事件：%v", got)
			}
		}
	}
	out, err = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), nil)
	if err != nil || !out.Handled || out.Plugin != "c" || !strings.Contains(string(out.Value), `"by":"c"`) {
		t.Fatalf("认领：a 超时、b 未处理、c 认领：%+v %v", out, err)
	}
	if out, _ = e.sup.Call(ctx, pluginhost.Claim, ev("console.command"), func(id string) bool { return id != "c" }); out.Handled {
		t.Fatalf("没有插件认领：%+v", out)
	}
	if _, err = e.sup.Call(ctx, pluginhost.Intercept, map[string]any{}, nil); err == nil {
		t.Fatal("事件缺少 type 应报错")
	}
	e.sup.SetDisabled(true)
	if out, _ = e.sup.Call(ctx, pluginhost.Intercept, ev("task.before_start"), nil); out.Blocked {
		t.Fatalf("全局停用时没有插件参与：%+v", out)
	}
}

// 监护启动时与之后每小时清理：观察类事件（连同投递记录）保留 30 天，宿主日志保留 7 天。
func TestHostPrunesOldEventsAndLogs(t *testing.T) {
	e := newHostEnv(t, "", []pluginhost.Spec{spec("alpha", nil, "inbox.received")}, nil)
	old := e.addEvent("inbox.received", time.Now().Add(-31*24*time.Hour), "alpha")
	recent := e.addEvent("inbox.received", time.Now().Add(-29*24*time.Hour), "alpha")
	e.exec(`INSERT INTO plugin_host_log(at,plugin_id,level,line) VALUES(now()-interval '8 days','alpha','info','旧'),(now()-interval '6 days','alpha','info','新')`)
	e.sup.Start(true) // 停用时也清理
	e.eventually("清理", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(id, ',' ORDER BY id) FROM plugin_events`) + " " + e.scalar(`SELECT string_agg(line, ',') FROM plugin_host_log`)
		return s == recent+" 新", s
	})
	if got := e.scalar(`SELECT count(*)::text FROM plugin_event_deliveries WHERE event_id=$1`, old); got != "0" {
		t.Fatalf("投递记录随事件删除：%s", got)
	}
}

// 定时任务：时钟走到 cron 匹配的那一分钟时发 schedule 请求，每分钟至多一次；加载时所在的那一分钟不触发。
// 上一次还没有回应的条目跳过这一次并记入日志；错过的分钟不补。
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
	// 定时协程启动时每个条目读一次时钟记下加载的那一分钟，之后每个刻度读一次：多读两次说明已按 09:00 走完一个刻度，
	// 这之后再拨钟才不会被当成加载时所在的那一分钟。
	e.eventually("定时协程开始", func() (bool, string) {
		return reads.Load() >= int64(len(sp.Schedules))+2, fmt.Sprint(reads.Load())
	})
	if n := e.count("schedule", "cron", ""); n != 0 {
		t.Fatalf("加载时所在的那一分钟不触发：%d", n)
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
	e.eventually("09:01 触发", func() (bool, string) {
		return schedules("sweep") == 1 && schedules("slow") == 1, fmt.Sprint(schedules("sweep"), schedules("slow"))
	})
	time.Sleep(50 * time.Millisecond)
	if schedules("sweep") != 1 {
		t.Fatal("同一分钟只触发一次")
	}
	now.Store(base.Add(59 * time.Minute).UnixNano()) // 跳到 09:59：中间的分钟不补
	e.eventually("09:59 触发", func() (bool, string) { return schedules("sweep") == 2, fmt.Sprint(schedules("sweep")) })
	if schedules("slow") != 1 || schedules("hourly") != 0 {
		t.Fatalf("slow 上一次没有回应应跳过，hourly 未到：%d %d", schedules("slow"), schedules("hourly"))
	}
	e.eventually("跳过记入日志", func() (bool, string) {
		s := e.scalar(`SELECT string_agg(line, '|') FROM plugin_host_log WHERE plugin_id='cron'`)
		return strings.Contains(s, "定时任务 slow 上一次还没有结束"), s
	})
	now.Store(base.Add(60 * time.Minute).UnixNano())
	e.eventually("10:00 触发 hourly", func() (bool, string) { return schedules("hourly") == 1, "" })
	for _, m := range e.entries() {
		if m["type"] == "schedule" && m["scheduleId"] == "hourly" {
			if at, _ := time.Parse(time.RFC3339, m["at"].(string)); !at.Equal(time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)) {
				t.Fatalf("at 是触发的那一分钟：%v", m["at"])
			}
		}
	}
}
