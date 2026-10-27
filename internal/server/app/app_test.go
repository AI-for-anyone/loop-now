package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// world 是一组可在多个服务端实例之间共享的外部状态：数据库 schema、Redis 前缀、工作根目录。
type world struct {
	t       *testing.T
	schema  string
	cfg     config.Config
	piAIDir string
	logTo   io.Writer // 为空时丢弃日志
}

func newWorld(t *testing.T) *world {
	t.Helper()
	real, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.1.0（读取模型目录需要）：%v", err)
	}
	schema := pgtest.NewSchema(t)
	_, prefix := redistest.New(t)
	return &world{t: t, schema: schema, piAIDir: real.PiAIDir, cfg: config.Config{
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second,
		DatabaseURL: pgtest.URL(t), RedisURL: redistest.URL(t),
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
		WorkRoot: filepath.Join(t.TempDir(), "work"), RedisKeyPrefix: prefix, DefaultOrgName: "默认组织", DefaultProjectName: "默认项目",
		PiBin: fakepibin.Path(t), PiVersion: "1.1.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second,
		PiStopGrace: 2 * time.Second, AbortTimeout: 3 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, LockTTL: 5 * time.Second, LockRenewInterval: time.Second, ReconcileInterval: 500 * time.Millisecond,
		SSEKeepalive: time.Second, EventStreamMaxLen: 1000, RunTextTTL: time.Hour, ShutdownTimeout: 10 * time.Second,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16,
		TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100,
		LoopctlDistDir: loopctlfake.Dir(t),
	}}
}

type server struct {
	t   *testing.T
	a   *app.App
	url string
}

func (w *world) start(mutate func(c *config.Config)) *server {
	w.t.Helper()
	cfg := w.cfg
	if mutate != nil {
		mutate(&cfg)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.t.Fatal(err)
	}
	logTo := w.logTo
	if logTo == nil {
		logTo = io.Discard
	}
	a, err := app.Build(context.Background(), &cfg, config.NewLogger(logTo, cfg.LogFormat, cfg.LogLevel),
		app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		w.t.Fatalf("启动服务端失败：%v", err)
	}
	go func() { _ = a.Serve() }()
	return &server{t: w.t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}

func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.a.Shutdown(ctx); err != nil {
		s.t.Errorf("停止服务端出错：%v", err)
	}
}

func (s *server) call(method, path string, body any) (int, []byte) {
	s.t.Helper()
	var r io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r, ct = bytes.NewReader(b), "application/octet-stream"
	default:
		j, _ := json.Marshal(b)
		r, ct = bytes.NewReader(j), "application/json"
	}
	req, _ := http.NewRequest(method, s.url+path, r)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (s *server) must(status int, method, path string, body any) []byte {
	s.t.Helper()
	got, out := s.call(method, path, body)
	if got != status {
		s.t.Fatalf("%s %s：期望 %d，实际 %d：%s", method, path, status, got, out)
	}
	return out
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("解析响应失败：%v：%s", err, b)
	}
	return v
}

func (s *server) createAgent(name string) (string, apitypes.Agent) {
	s.t.Helper()
	providers := decode[apitypes.ModelProviderList](s.t, s.must(200, "GET", "/model-providers", nil))
	provider := ""
	for _, p := range providers.Providers {
		if p.ID == "anthropic" {
			provider = p.ID
			break
		}
	}
	if provider == "" {
		s.t.Fatal("installed Pi fixture requires Anthropic catalog metadata")
	}
	models := decode[apitypes.ModelList](s.t, s.must(200, "GET", "/model-providers/"+provider+"/models", nil))
	ctx := decode[apitypes.Context](s.t, s.must(200, "GET", "/context", nil))
	ag := decode[apitypes.Agent](s.t, s.must(201, "POST", "/projects/"+ctx.Project.ID.String()+"/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": provider, "modelId": models.Models[0].ID}, "prompt": "你是测试助手"}))
	return "/agents/" + ag.ID.String(), ag
}

// waitIdle 等到 Agent 完全空闲：没有进行中或排队的执行，且忙闲锁已交接释放
// （执行结束写库与交接锁之间有短暂窗口，只看状态时紧接着发送的消息可能被判为正忙）。
func (s *server) waitIdle(a string) apitypes.AgentStatus {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := decode[apitypes.AgentStatus](s.t, s.must(200, "GET", a+"/status", nil))
		owner, err := s.a.Coord.LockOwner(context.Background(), strings.TrimPrefix(a, "/agents/"))
		if !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 && err == nil && owner == "" {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("等待空闲超时")
	return apitypes.AgentStatus{}
}

func (s *server) waitRunning(a string) apitypes.Run {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := decode[apitypes.AgentStatus](s.t, s.must(200, "GET", a+"/status", nil))
		if st.CurrentRun != nil && st.CurrentRun.Status == "running" {
			return *st.CurrentRun
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("等待执行开始超时")
	return apitypes.Run{}
}

func (s *server) replies(a string) []apitypes.Message {
	s.t.Helper()
	page := decode[apitypes.MessagePage](s.t, s.must(200, "GET", a+"/messages?limit=200", nil))
	var out []apitypes.Message
	for _, m := range page.Messages {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

func fakepiAlive(t *testing.T, workRoot string) bool {
	out, _ := exec.Command("pgrep", "-f", workRoot).Output()
	return strings.TrimSpace(string(out)) != ""
}

// TestEndToEndWithFakePi 是任务 13.1 的脚本化验收。
func TestEndToEndWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()

	a, ag := s.createPlatformAgent("验收助手")
	tmpl := decode[apitypes.SkillTemplate](t, s.must(201, "POST", "/skills", map[string]any{"skillMd": "---\nname: notes\ndescription: 记笔记\n---\n"}))
	s.must(201, "POST", a+"/skills", map[string]any{"templateId": tmpl.ID})
	s.must(200, "PUT", a+"/folder/content?path=docs/guide/a.md", []byte("资料内容"))
	s.must(200, "PUT", a+"/clis/jq", nil)
	s.must(200, "PUT", a+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk-test"})

	s.must(202, "POST", a+"/messages", map[string]any{"text": "你好"})
	st := s.waitIdle(a)
	if r := s.replies(a); len(r) != 1 || !strings.Contains(r[0].Text, "你好") || r[0].Run.Status != "completed" {
		t.Fatalf("第一轮回复不对：%+v", r)
	}
	agentRoot := filepath.Join(w.cfg.WorkRoot, "agents", ag.ID.String())
	if b, err := os.ReadFile(filepath.Join(agentRoot, "workspace", "docs", "guide", "a.md")); err != nil || string(b) != "资料内容" {
		t.Fatalf("上传的文件应直接写入 Agent 文件夹：%q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "skills", "notes", "SKILL.md")); err != nil {
		t.Fatal("Skill 应同步")
	}
	if st.Session.State != "active" {
		t.Fatalf("会话文件写盘后应记录：%+v", st.Session)
	}

	// 忙时排队与打断。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=1s 很长的回复"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "排队的", "whenBusy": "queue"})
	s.must(202, "POST", a+"/messages", map[string]any{"text": "打断的", "whenBusy": "interrupt"})
	s.waitIdle(a)
	r := s.replies(a)
	if len(r) != 4 || r[1].Run.Status != "interrupted" || !strings.Contains(r[2].Text, "打断的") || !strings.Contains(r[3].Text, "排队的") {
		var b strings.Builder
		for _, m := range r {
			fmt.Fprintf(&b, "%s|%s; ", m.Run.Status, m.Text)
		}
		t.Fatalf("打断与排队的顺序不对：%s", b.String())
	}

	s.must(204, "DELETE", a, nil)
	if _, err := os.Stat(filepath.Join(agentRoot, "home")); !os.IsNotExist(err) {
		t.Fatal("删除 Agent 后独立 HOME 应被删除")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "workspace", "docs")); err != nil {
		t.Fatal("删除 Agent 后工作区应保留")
	}
}

// TestRestartGraceful 对应任务 11.2（正常停止）。
func TestRestartGraceful(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, _ := s.createAgent("重启测试")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=10s 很长"})
	run := s.waitRunning(a)
	time.Sleep(300 * time.Millisecond)
	// 多实例预留：锁的值、执行记录与 pi.pid 都带本实例 ID。
	inst := s.a.InstanceID.String()
	agentID := strings.TrimPrefix(a, "/agents/")
	if owner, _ := s.a.Coord.LockOwner(context.Background(), agentID); owner != run.ID.String()+"|"+inst {
		t.Fatalf("锁的值应为 <执行 ID>|<实例 ID>：%s", owner)
	}
	var runInst string
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT instance_id::text FROM runs WHERE id = $1`, run.ID).Scan(&runInst); err != nil || runInst != inst {
		t.Fatalf("runs.instance_id 应为本实例：%s %v", runInst, err)
	}
	pidJSON, err := os.ReadFile(filepath.Join(w.cfg.WorkRoot, "agents", agentID, "pi.pid"))
	if err != nil || !strings.Contains(string(pidJSON), inst) {
		t.Fatalf("pi.pid 应带本实例 ID：%s %v", pidJSON, err)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "排队的", "whenBusy": "queue"})
	s.stop()
	if fakepiAlive(t, w.cfg.WorkRoot) {
		t.Fatal("正常停止后不应留下 Pi 进程")
	}

	s2 := w.start(nil)
	defer s2.stop()
	s2.waitIdle(a)
	r := s2.replies(a)
	if len(r) != 2 || r[0].Run.ID != run.ID || r[0].Run.Status != "interrupted" || *r[0].Run.EndReason != "server_restart" || r[0].Text == "" {
		t.Fatalf("原回复应标为服务端重启中断并保留文字：%+v", r)
	}
	if !strings.Contains(r[1].Text, "排队的") || r[1].Run.Status != "completed" {
		t.Fatalf("排队消息应照常处理：%+v", r[1])
	}
}

// TestRestartAfterCrash 对应任务 11.2（强制结束）：遗留的 Pi 进程在下次启动时被结束。
func TestRestartAfterCrash(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, _ := s.createAgent("崩溃测试")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s 很长"})
	s.waitRunning(a)
	// 模拟进程被强制结束：不走优雅退出，不结束 Pi 进程，只停掉对话协调与 HTTP。
	_ = s.a.Conv.Stop(context.Background())
	if !fakepiAlive(t, w.cfg.WorkRoot) {
		t.Fatal("模拟崩溃时 Pi 进程应仍在运行")
	}
	s2 := w.start(nil)
	defer s2.stop()
	s2.waitIdle(a)
	if fakepiAlive(t, w.cfg.WorkRoot) {
		// 新实例可能已为后续消息启动进程；这里没有后续消息，应没有任何 Pi 进程。
		t.Fatal("下次启动时应结束遗留的 Pi 进程")
	}
	if r := s2.replies(a); len(r) != 1 || *r[0].Run.EndReason != "server_restart" {
		t.Fatalf("遗留执行应标为服务端重启中断：%+v", r)
	}
	_ = s.a.Executor.Shutdown(context.Background())
}

// TestSessionFiles 对应任务 11.4。
func TestSessionFiles(t *testing.T) {
	w := newWorld(t)
	s := w.start(func(c *config.Config) { c.PiIdleTimeout = 300 * time.Millisecond })
	defer s.stop()
	a, _ := s.createAgent("会话测试")

	// 首条消息被 Pi 拒绝、未写会话文件：之后不算丢失。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!rejectprompt !nosession"})
	st := s.waitIdle(a)
	if st.Session.State != "none" {
		t.Fatalf("未写盘的会话不应被记录：%+v", st.Session)
	}
	time.Sleep(800 * time.Millisecond) // 等空闲退出
	s.must(202, "POST", a+"/messages", map[string]any{"text": "第二条"})
	st = s.waitIdle(a)
	if r := s.replies(a); r[len(r)-1].Run.Status != "completed" || st.Session.State != "active" {
		t.Fatalf("应从新会话开始正常回复：%+v %+v", r[len(r)-1], st.Session)
	}

	// 已写盘的会话文件被删除：判为丢失，确认重开后恢复。
	time.Sleep(800 * time.Millisecond)
	matches, _ := filepath.Glob(filepath.Join(w.cfg.WorkRoot, "agents", "*", "sessions", "*.jsonl"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "第三条"})
	st = s.waitIdle(a)
	if r := s.replies(a); *r[len(r)-1].Run.ErrorCode != "session_missing" || st.Session.State != "missing" {
		t.Fatalf("会话文件被删后应判为丢失：%+v", st.Session)
	}
	if code, _ := s.call("POST", a+"/messages", map[string]any{"text": "第四条"}); code != http.StatusConflict {
		t.Fatalf("会话丢失时应拒绝新消息：%d", code)
	}
	sess := decode[apitypes.SessionInfo](t, s.must(200, "POST", a+"/session/reset", nil))
	if sess.Epoch != 2 {
		t.Fatalf("会话序号应加一：%+v", sess)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "第五条"})
	s.waitIdle(a)
	if r := s.replies(a); r[len(r)-1].Run.Status != "completed" || r[len(r)-1].SessionEpoch != 2 {
		t.Fatalf("重开后应正常回复：%+v", r[len(r)-1])
	}
}

// 项目层：默认项目改名后重启，名称保持（不按配置改回或新建）；删除项目时停止其中 Agent 的 Pi 进程，工作文件夹保留。
func TestProjectsWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	ctx := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PATCH", "/projects/"+ctx.Project.ID.String(), map[string]any{"name": "主项目"})

	p := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "临时"}))
	providers := decode[apitypes.ModelProviderList](t, s.must(200, "GET", "/model-providers", nil))
	models := decode[apitypes.ModelList](t, s.must(200, "GET", "/model-providers/"+providers.Providers[0].ID+"/models", nil))
	ag := decode[apitypes.Agent](t, s.must(201, "POST", "/projects/"+p.ID.String()+"/agents", map[string]any{"name": "甲",
		"model": map[string]string{"provider": providers.Providers[0].ID, "modelId": models.Models[0].ID}}))
	a := "/agents/" + ag.ID.String()
	s.must(202, "POST", a+"/messages", map[string]any{"text": "你好"})
	s.waitIdle(a)
	agentRoot := filepath.Join(w.cfg.WorkRoot, "agents", ag.ID.String())
	if !fakepiAlive(t, agentRoot) {
		t.Fatal("回复后 Pi 应常驻")
	}
	s.must(204, "DELETE", "/projects/"+p.ID.String(), nil)
	deadline := time.Now().Add(5 * time.Second)
	for fakepiAlive(t, agentRoot) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if fakepiAlive(t, agentRoot) {
		t.Fatal("删除项目后其中 Agent 的 Pi 进程应停止")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "workspace")); err != nil {
		t.Fatalf("删除项目后工作文件夹应保留：%v", err)
	}
	s.stop()

	s2 := w.start(nil)
	defer s2.stop()
	if got := decode[apitypes.Context](t, s2.must(200, "GET", "/context", nil)); got.Project.ID != ctx.Project.ID || got.Project.Name != "主项目" {
		t.Fatalf("重启后默认项目应是同一个且保持新名称：%+v", got.Project)
	}
	if l := decode[apitypes.ProjectList](t, s2.must(200, "GET", "/projects", nil)); len(l.Projects) != 1 {
		t.Fatalf("重启后不应多出项目：%+v", l)
	}
}

func (s *server) createPlatformAgent(name string) (string, apitypes.Agent) {
	s.t.Helper()
	providers := decode[apitypes.ModelProviderList](s.t, s.must(200, "GET", "/model-providers", nil))
	provider := ""
	for _, p := range providers.Providers {
		if p.ID == "anthropic" {
			provider = p.ID
			break
		}
	}
	if provider == "" {
		s.t.Fatal("installed Pi fixture requires Anthropic catalog metadata")
	}
	models := decode[apitypes.ModelList](s.t, s.must(200, "GET", "/model-providers/"+provider+"/models", nil))

	ag := decode[apitypes.Agent](s.t, s.must(201, "POST", "/platform/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": provider, "modelId": models.Models[0].ID}, "prompt": "你是测试助手"}))
	return "/agents/" + ag.ID.String(), ag
}
