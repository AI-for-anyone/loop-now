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

	"github.com/redis/go-redis/v9"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
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

// 运行记录、用量与异常列表走完整链路：执行器 → 记录器 → 数据库 → 接口。
func TestObservabilityWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	a, ag := s.createAgent("观测")

	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=100,20,1000,0 !tool=bash 你好"})
	s.waitIdle(a)
	first := s.replies(a)[0]
	if u := first.Run.Usage; u.Calls != 2 || u.Input != 200 || u.Output != 40 || u.CacheRead != 2000 || u.Total != 2240 {
		t.Fatalf("回复的用量合计不对：%+v", u)
	}
	if m := first.Run.Model; m == nil || m.Provider != ag.Model.Provider || m.ModelID != ag.Model.ModelID {
		t.Fatalf("回复所用的模型不对：%+v，期望 %+v", m, ag.Model)
	}
	if first.Run.RunningAt == nil || first.Run.StartedAt == nil {
		t.Fatalf("回复的时间点不全：%+v", first.Run)
	}
	types := entryTypes(s.timeline(a, first.Run.ID.String()))
	if types[0] != "dispatched" || types[len(types)-1] != "ended" {
		t.Fatalf("时间线应以派发开始、结束收尾：%v", types)
	}
	order := []string{"dispatched", "phase:preparing", "workspace_sync", "phase:launching", "pi_started", "running", "model_call:assistant", "tool", "ended"}
	last := -1
	for _, want := range order {
		i := indexOf(types, want)
		if i < 0 || i < last {
			t.Fatalf("时间线缺少或顺序不对：%s 在 %v", want, types)
		}
		last = i
	}

	s.must(202, "POST", a+"/messages", map[string]any{"text": "再来"})
	s.waitIdle(a)
	second := s.replies(a)[1]
	if types := entryTypes(s.timeline(a, second.Run.ID.String())); indexOf(types, "pi_reused") < 0 || indexOf(types, "pi_started") >= 0 {
		t.Fatalf("第二条回复应复用进程：%v", types)
	}

	since := url.QueryEscape(time.Now().Add(-time.Hour).Format(time.RFC3339))
	usage := decode[apitypes.UsagePage](t, s.must(200, "GET", "/usage?from="+since, nil))
	if len(usage.Rows) != 1 || usage.Rows[0].Calls != 3 || usage.Rows[0].Total != 2240 || *usage.Rows[0].Model != ag.Model.ModelID {
		t.Fatalf("用量统计不对：%+v", usage.Rows)
	}

	s.must(202, "POST", a+"/messages", map[string]any{"text": "!error=模拟错误"})
	s.waitIdle(a)
	list := decode[apitypes.AnomalyPage](t, s.must(200, "GET", "/anomalies?from="+since, nil))
	if list.Total != 1 || list.Items[0].Kind != "run_failed" || *list.Items[0].ErrorCode != "pi_error" || list.Items[0].AgentName != "观测" {
		t.Fatalf("异常列表不对：%+v", list)
	}
}

// 重启前后：CLI 安装失败仍可见；进行中的回复以服务端重启中断出现在异常列表中。
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

	a, _ := s.createAgent("装 CLI")
	s.must(200, "PUT", a+"/clis/zz", nil)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "hi"})
	s.waitIdle(a)
	failed := s.replies(a)[0]
	if failed.Run.ErrorCode == nil || *failed.Run.ErrorCode != "cli_install_failed" {
		t.Fatalf("应以 CLI 安装失败结束：%+v", failed.Run)
	}
	var install *timelineEntry
	for _, e := range s.timeline(a, failed.Run.ID.String()) {
		if e.Type == "cli_install" {
			install = &e
		}
	}
	if install == nil || install.Result != "failed" || install.CLI != "zz" || install.Error == nil || !strings.Contains(*install.Error, "摘要不符") {
		t.Fatalf("时间线应有 CLI 安装失败：%+v", install)
	}

	b, _ := s.createAgent("长回复")
	s.must(202, "POST", b+"/messages", map[string]any{"text": "!sleep=10s 很长的回复"})
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
			if tp.Install.Status != "failed" || tp.Install.Error == nil || !strings.Contains(*tp.Install.Error, "摘要不符") {
				t.Fatalf("重启后应仍显示安装失败及原因：%+v", tp.Install)
			}
		}
	}
	if !found {
		t.Fatal("模板目录中没有 zz")
	}
	list := decode[apitypes.AnomalyPage](t, s2.must(200, "GET", "/anomalies", nil))
	kinds := map[string]int{}
	for _, it := range list.Items {
		kinds[it.Kind]++
	}
	if kinds["server_restart"] != 1 || kinds["run_failed"] != 1 {
		t.Fatalf("异常列表应有一条重启中断与一条安装失败：%+v", list.Items)
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

// 记录 Pi 进程启动事件时数据库写入失败：回复照常完成，日志中有警告。
func TestRecordFailureDoesNotAffectRun(t *testing.T) {
	w := newWorld(t)
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	st, err := store.Open(context.Background(), pgtest.URL(t), store.OpenOptions{SearchPath: w.schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Pool.Exec(context.Background(), `CREATE FUNCTION fail_pi_started() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.type = 'pi.started' THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_pi_started BEFORE INSERT ON lifecycle_events FOR EACH ROW EXECUTE FUNCTION fail_pi_started();`); err != nil {
		t.Fatal(err)
	}
	a, _ := s.createAgent("记录失败")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "你好"})
	s.waitIdle(a)
	if r := s.replies(a); len(r) != 1 || r[0].Run.Status != "completed" {
		t.Fatalf("记录失败不应影响回复：%+v", r)
	}
	if !strings.Contains(logs.String(), "写入运行记录失败") || !strings.Contains(logs.String(), "pi.started") {
		t.Fatalf("日志中应有记录失败的警告：\n%s", logs.String())
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
			t.Fatalf("JSON 日志的每行都应可以单独解析：%q：%v", l, err)
		}
		out = append(out, m)
	}
	return out
}

// 服务端日志：每条回复开始与结束各一行 info，统一带 runId、agentId；接口错误带 requestId；不含消息正文，不写日志文件。
func TestServerLogs(t *testing.T) {
	w := newWorld(t)
	w.cfg.LogFormat = "json"
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	a, ag := s.createAgent("日志")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "预热"})
	s.waitIdle(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=10,5 !tool=bash !tool=bash !tool=bash 秘密正文xyz"})
	s.waitIdle(a)
	run := s.replies(a)[1].Run

	lines := parseLogs(t, logs.String())
	var info []logLine
	for _, l := range lines {
		if l["level"] == "INFO" && l["runId"] == run.ID.String() {
			info = append(info, l)
		}
	}
	if len(info) != 2 || info[0]["msg"] != "回复开始" || info[1]["msg"] != "回复结束" {
		t.Fatalf("这条回复在 info 级别应只有开始与结束两行：%v", info)
	}
	end := info[1]
	if end["agentId"] != ag.ID.String() || end["status"] != "completed" || end["tokens"] != float64(30) {
		t.Fatalf("结束日志不对：%v", end)
	}
	if _, ok := end["durationMs"]; !ok {
		t.Fatalf("结束日志应带总用时：%v", end)
	}
	if strings.Contains(logs.String(), "秘密正文xyz") {
		t.Fatal("日志中不应出现消息正文")
	}

	// 失败的回复：结束日志带错误码。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!error=模拟失败 再来"})
	s.waitIdle(a)
	failed := s.replies(a)[2].Run
	found := false
	for _, l := range parseLogs(t, logs.String()) {
		if l["msg"] == "回复结束" && l["runId"] == failed.ID.String() {
			found = l["status"] == "failed" && l["errorCode"] == "pi_error"
		}
	}
	if !found {
		t.Fatalf("失败回复的结束日志应带错误码：%+v", failed)
	}

	// 接口内部出错：错误日志带请求编号，与响应头一致。
	st, err := store.Open(context.Background(), pgtest.URL(t), store.OpenOptions{SearchPath: w.schema})
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
		t.Fatalf("应返回 500：%d", resp.StatusCode)
	}
	rid := resp.Header.Get("Loop-Now-Request-Id")
	ok := false
	for _, l := range parseLogs(t, logs.String()) {
		if l["level"] == "ERROR" && l["requestId"] == rid {
			ok = true
		}
	}
	if !ok || rid == "" {
		t.Fatalf("接口错误日志应带请求编号 %q", rid)
	}
	if _, err := st.Pool.Exec(context.Background(), `ALTER TABLE usage_records_hidden RENAME TO usage_records`); err != nil {
		t.Fatal(err)
	}

	// 不写日志文件。
	_ = filepath.WalkDir(w.cfg.WorkRoot, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".log") {
			t.Errorf("服务端不应写日志文件：%s", p)
		}
		return nil
	})
}

// readOverviewSnapshot 连接概览事件流（可带 Last-Event-ID），返回首个事件。
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
	s.t.Fatal("没有收到概览事件")
	return "", nil
}

// 8.1：强制结束后重启，遗留回复记为服务端重启中断并出现在异常列表；概览快照显示正确状态。
func TestRecoveryCrashRecords(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, ag := s.createAgent("崩溃记录")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s 很长"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "排队的", "whenBusy": "queue"})
	_ = s.a.Conv.Stop(context.Background()) // 模拟强制结束：不走优雅退出
	s2 := w.start(nil)
	defer s2.stop()
	defer func() { _ = s.a.Executor.Shutdown(context.Background()) }()
	s2.waitIdle(a)
	r := s2.replies(a)
	if len(r) != 2 || *r[0].Run.EndReason != "server_restart" || r[1].Run.Status != "completed" {
		t.Fatalf("重启后的回复不对：%+v", r)
	}
	es := s2.timeline(a, r[0].Run.ID.String())
	if last := es[len(es)-1]; last.Type != "ended" {
		t.Fatalf("重启中断的回复应以结束条目收尾：%+v", es)
	}
	list := decode[apitypes.AnomalyPage](t, s2.must(200, "GET", "/anomalies", nil))
	if list.Total != 1 || list.Items[0].Kind != "server_restart" || list.Items[0].RunID != r[0].Run.ID {
		t.Fatalf("异常列表应有一条服务端重启中断：%+v", list)
	}
	ev, data := s2.firstOverviewEvent("")
	snap := decode[apitypes.SseOverviewSnapshot](t, data)
	if ev != "snapshot" || len(snap.Agents) != 1 || snap.Agents[0].AgentID != ag.ID || snap.Agents[0].Status.Busy ||
		snap.Agents[0].Status.Queued.Normal != 0 || snap.Agents[0].LastRun == nil || snap.Agents[0].LastRun.Status != "completed" {
		t.Fatalf("重启后的概览快照不对：%s", data)
	}
}

// 8.2：Redis 数据丢失（含概览事件流）：重连得到快照，数据库中的运行记录与用量不受影响。
func TestRecoveryRedisLoss(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	a, _ := s.createAgent("Redis 丢失")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!usage=10,5 你好"})
	s.waitIdle(a)
	_, data := s.firstOverviewEvent("")
	_ = data
	before := s.replies(a)[0]

	opts, _ := redis.ParseURL(w.cfg.RedisURL)
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	ctx := context.Background()
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, w.cfg.RedisKeyPrefix+"*", 200).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			rdb.Unlink(ctx, keys...)
		}
		if cursor = next; cursor == 0 {
			break
		}
	}
	if ev, _ := s.firstOverviewEvent("9999999999999-0"); ev != "snapshot" {
		t.Fatalf("事件流丢失后重连应推送快照：%s", ev)
	}
	after := s.replies(a)[0]
	if after.Run.Usage != before.Run.Usage || len(s.timeline(a, after.Run.ID.String())) == 0 {
		t.Fatalf("Redis 丢失不应影响运行记录与用量：%+v %+v", before.Run.Usage, after.Run.Usage)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "继续"})
	s.waitIdle(a)
}

// 8.3：打断超时强制结束：记录停止原因，回复以已打断结束，时间线以强制结束与结束条目收尾。
func TestRecoveryAbortTimeout(t *testing.T) {
	w := newWorld(t)
	s := w.start(func(c *config.Config) { c.AbortTimeout = time.Second })
	defer s.stop()
	a, _ := s.createAgent("打断超时")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=10s !noabort 很长"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "打断", "whenBusy": "interrupt"})
	s.waitIdle(a)
	first := s.replies(a)[0]
	if first.Run.Status != "interrupted" || first.Run.AbortRequestedAt == nil {
		t.Fatalf("应以已打断结束并记录发起打断时间：%+v", first.Run)
	}
	types := entryTypes(s.timeline(a, first.Run.ID.String()))
	ia, is, ie := indexOf(types, "abort_requested"), indexOf(types, "pi_stopped"), indexOf(types, "ended")
	if ia < 0 || is < ia || ie != len(types)-1 || indexOf(types, "pi_exited") >= 0 {
		t.Fatalf("时间线应依次有发起打断、强制结束、结束：%v", types)
	}
	if indexOf(types, "model_call:assistant") >= 0 {
		t.Fatalf("被强制结束的那次调用没有用量报告：%v", types)
	}
}
