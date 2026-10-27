package httpapi_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/httpapi"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// quickExec 立即完成回复；消息含 "block" 时阻塞，直到被打断或放行。
type quickExec struct {
	mu      sync.Mutex
	running map[uuid.UUID]chan struct{}
}

func (q *quickExec) Execute(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
	ch := make(chan struct{})
	q.mu.Lock()
	q.running[req.AgentID] = ch
	q.mu.Unlock()
	defer func() { q.mu.Lock(); delete(q.running, req.AgentID); q.mu.Unlock() }()
	sink.Phase(executor.PhaseLaunching)
	sink.Running(1)
	sink.Tool("t1", "bash", executor.ToolStarted)
	sink.TextDelta("你")
	sink.TextDelta("好")
	if strings.Contains(req.Message, "block") {
		select {
		case <-ch:
			return executor.Result{Outcome: executor.OutcomeInterrupted, Text: "你好", ConfigVersion: 1}, nil
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	sink.Tool("t1", "bash", executor.ToolSucceeded)
	if strings.Contains(req.Message, "fail") {
		return executor.Result{Outcome: executor.OutcomeFailed, Text: "你好", ErrorCode: executor.ErrWorkspaceSyncFailed,
			ErrorMessage: "来源 app：克隆失败", ConfigVersion: 1}, nil
	}
	return executor.Result{Outcome: executor.OutcomeCompleted, Text: "你好", ConfigVersion: 1}, nil
}

func (q *quickExec) Interrupt(_ context.Context, id, _ uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if ch := q.running[id]; ch != nil {
		close(ch)
		delete(q.running, id)
	}
	return nil
}
func (q *quickExec) Release(context.Context, uuid.UUID) error { return nil }
func (q *quickExec) Process(uuid.UUID) executor.ProcessInfo {
	return executor.ProcessInfo{State: executor.ProcessStopped}
}
func (q *quickExec) Shutdown(context.Context) error { return nil }

type env struct {
	t      *testing.T
	srv    *httptest.Server
	api    *httpapi.Server
	v      *contracttest.Validator
	client *http.Client
	co     *coord.Coord
	st     *store.Store
	conv   *conversation.Service
	dc     store.DefaultContext
	rdb    *redis.Client

	ext          *httptest.Server // 对外端口（按需启动）
	extV, agentV *contracttest.Validator
}

// agents 返回默认项目的 Agent 列表与创建路径。
func (e *env) agents() string { return "/projects/" + e.dc.Project.ID.String() + "/agents" }

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st := pgtest.NewStore(t)
	rdb, prefix := redistest.New(t)
	co := coord.New(rdb, coord.Options{Prefix: prefix, LockTTL: 5 * time.Second, StreamMaxLen: 1000, RunTextTTL: time.Hour, OverviewMaxLen: 1000})
	dc, err := st.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	tmpls, _ := clitemplate.Load("")
	models := modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X", Input: []string{"text"}}}},
	})
	limits := apitypes.Limits{MaxUploadFile: 1 << 20, MaxFolderUpload: 1 << 20, MaxMessage: 1 << 16, MaxInboxMessage: 1 << 16, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096,
		TextEdit: apitypes.TextEdit{MaxBytes: 1024, Extensions: []string{".md", ".txt"}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ex := &quickExec{running: map[uuid.UUID]chan struct{}{}}
	cfg := &agentconfig.Service{Store: st, Models: models, Templates: tmpls, Releaser: ex, OrgID: dc.Organization.ID,
		Limits: agentconfig.Limits{MaxUploadFile: limits.MaxUploadFile, MaxSkillArchive: limits.MaxSkillArchive, MaxSkillEntries: limits.MaxSkillEntries,
			MaxCredential: limits.MaxCredential, Text: content.TextRules{Extensions: limits.TextEdit.Extensions, MaxBytes: limits.TextEdit.MaxBytes}}, Logger: log}
	coordinator := modelauth.NewCoordinator(st, nil, modelauth.CoordinatorOptions{}, log)
	modelAuth := modelauth.NewService(st, nil, coordinator, models, modelauth.ServiceOptions{}, log)
	projectCredentials := modelauth.NewProjectService(modelAuth, tmpls, limits.MaxCredential)
	cfg.ModelAuth = modelAuth
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := modelAuth.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	conv := conversation.New(st, co, ex, conversation.Options{InstanceID: uuid.Must(uuid.NewV7()), MaxMessage: limits.MaxMessage,
		LockRenewInterval: time.Second, ReconcileInterval: time.Second}, log)
	cfg.AgentChanged, cfg.AgentRemoved = conv.EmitAgentStatus, conv.EmitAgentStatus
	if err := conv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conv.Stop(context.Background()) })
	api := &httpapi.Server{
		O: httpapi.Options{AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
			SSEKeepalive: 500 * time.Millisecond, Limits: limits, Defaults: dc},
		Config: cfg, ModelAuth: modelAuth, ProjectCredentials: projectCredentials, Conv: conv, Models: models, Templates: tmpls, Installer: clitemplate.NewInstaller(t.TempDir(), time.Minute, nil),
		Store: st, Coord: co, Records: &runrecord.Service{Store: st}, Log: log,
		Inbox: &inbox.Service{St: st, Co: co, Conv: conv, Log: log, O: inbox.Options{MaxMessage: limits.MaxInboxMessage, PreviewChars: 200,
			PageDefault: 20, PageMax: 100, DefaultOrgID: dc.Organization.ID}},
		Folders: folders.New(st, nil, folders.Options{WorkRoot: t.TempDir(), ContextDir: t.TempDir(), MaxUpload: limits.MaxFolderUpload, GitBin: "git", CloneTimeout: time.Minute}, log),
	}
	api.Plugins = &plugins.Service{Store: st, Root: filepath.Join(t.TempDir(), "plugins"), GitBin: "git", PlatformVersion: "dev", Log: log}
	if err := api.Plugins.Init(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	api.Plugins.APIURL = srv.URL + "/api/v1"
	v := contracttest.New(t, srv.URL)
	return &env{t: t, srv: srv, api: api, v: v, client: v.Client(), co: co, st: st, conv: conv, dc: dc, rdb: rdb}
}

// do 发送请求（经契约校验），返回状态码与响应体。
func (e *env) do(method, path string, body any, hdr ...string) (int, []byte, http.Header) {
	e.t.Helper()
	var r io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r, ct = bytes.NewReader(b), "application/octet-stream"
	case *multipartBody:
		r, ct = bytes.NewReader(b.data), b.contentType
	default:
		j, _ := json.Marshal(b)
		r, ct = bytes.NewReader(j), "application/json"
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, r)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, resp.Header
}

func (e *env) expect(status int, method, path string, body any, hdr ...string) []byte {
	e.t.Helper()
	got, out, _ := e.do(method, path, body, hdr...)
	if got != status {
		e.t.Fatalf("%s %s：期望 %d，实际 %d：%s", method, path, status, got, out)
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

type multipartBody struct {
	data        []byte
	contentType string
}

func zipUpload(t *testing.T, name, desc string) *multipartBody {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create(name + "/SKILL.md")
	fmt.Fprintf(w, "---\nname: %s\ndescription: %s\n---\n", name, desc)
	w, _ = zw.Create(name + "/scripts/run.sh")
	_, _ = w.Write([]byte("echo hi"))
	_ = zw.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("archive", name+".zip")
	_, _ = fw.Write(zbuf.Bytes())
	_ = mw.Close()
	return &multipartBody{data: buf.Bytes(), contentType: mw.FormDataContentType()}
}

// TestRouteCoverage 双向比对路由表与契约：路径与方法、operationId，以及控制台契约中的 x-access。
func TestRouteCoverage(t *testing.T) {
	e := newEnv(t)
	check := func(file string, routes []httpapi.Route, withAccess bool) {
		t.Helper()
		meta := contracttest.LoadOperations(t, file)
		seen := map[string]bool{}
		for _, r := range routes {
			key := r.Method + " " + r.Pattern
			seen[key] = true
			m, ok := meta[key]
			if !ok {
				t.Errorf("%s：已注册的路由不在契约中：%s", file, key)
				continue
			}
			if m.OperationID != r.Op {
				t.Errorf("%s：%s 的 operationId 应为 %s，路由表中为 %s", file, key, m.OperationID, r.Op)
			}
			if withAccess && m.Access != r.Access.String() {
				t.Errorf("%s：%s 的 x-access 为 %q，路由表中为 %q", file, key, m.Access, r.Access)
			}
			if withAccess && m.PluginScope != httpapi.PluginScopes()[r.Op] {
				t.Errorf("%s：%s 的 x-plugin-scope 为 %q，插件范围表中为 %q", file, key, m.PluginScope, httpapi.PluginScopes()[r.Op])
			}
		}
		for key, m := range meta {
			if !seen[key] {
				t.Errorf("%s：契约中的操作没有实现：%s", file, key)
			}
			if withAccess && m.Access != "console" && m.Access != "console-and-external" {
				t.Errorf("%s：%s 缺少 x-access 或取值不对：%q", file, key, m.Access)
			}
		}
	}
	if (httpapi.Route{}).Access != httpapi.ConsoleOnly {
		t.Fatal("路由默认应只限控制台，新路由不会因没有声明而对外开放")
	}
	check("openapi.yaml", e.api.Routes(), true)
	check("agent-openapi.yaml", e.api.AgentRoutes(), false)
}

func TestContractCatchesViolation(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"organization": 1, "extra": true}`))
	}))
	defer broken.Close()
	v := contracttest.New(t, broken.URL)
	resp, err := v.Client().Get(broken.URL + "/api/v1/context")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(v.Failures()) == 0 {
		t.Fatal("不符合契约的响应应被发现")
	}
}

func TestHostOriginAndCORS(t *testing.T) {
	e := newEnv(t)
	plain := &http.Client{}
	send := func(method, path, origin, host string, body io.Reader) *http.Response {
		req, _ := http.NewRequest(method, e.srv.URL+path, body)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if host != "" {
			req.Host = host
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := send("GET", "/api/v1/context", "", "evil.example.com", nil); r.StatusCode != http.StatusForbidden {
		t.Errorf("Host 不在名单中应拒绝：%d", r.StatusCode)
	}
	if r := send("POST", "/api/v1/platform/agents", "https://evil.example.com", "", strings.NewReader(`{"name":"x","model":{"provider":"anthropic","modelId":"claude-x"}}`)); r.StatusCode != http.StatusForbidden {
		t.Errorf("非白名单来源应拒绝：%d", r.StatusCode)
	}
	if _, body, _ := e.do("GET", e.agents(), nil); len(decode[map[string][]any](t, body)["agents"]) != 0 {
		t.Error("被拒绝的写请求不应写库")
	}
	r := send("OPTIONS", "/api/v1/platform/agents", "http://localhost:5173", "", nil)
	req, _ := http.NewRequest("OPTIONS", e.srv.URL+"/api/v1/platform/agents", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type, last-event-id")
	pre, _ := plain.Do(req)
	pre.Body.Close()
	if pre.StatusCode != http.StatusNoContent || pre.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Last-Event-ID") {
		t.Errorf("白名单来源的预检应放行：%d %v", pre.StatusCode, pre.Header)
	}
	_ = r
	if r := send("GET", "/api/v1/context", "", "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("无 Origin 的本机请求应放行：%d", r.StatusCode)
	}
	r = send("GET", "/api/v1/health", "http://localhost:5173", "", nil)
	if r.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || r.Header.Get("Access-Control-Expose-Headers") != "ETag, Loop-Now-Request-Id, Loop-Now-Version" {
		t.Errorf("白名单来源的响应应带 CORS 头并暴露 ETag：%v", r.Header)
	}
	if r := send("GET", "/healthz", "", "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("/healthz 应可用：%d", r.StatusCode)
	}
}

func TestAllEndpoints(t *testing.T) {
	e := newEnv(t)
	ctxBody := decode[apitypes.Context](t, e.expect(200, "GET", "/context", nil))
	if ctxBody.Limits.MaxMessage != 1<<16 || ctxBody.Project.Name != "默认项目" {
		t.Fatalf("上下文不对：%+v", ctxBody)
	}
	e.expect(200, "GET", "/health", nil)
	e.expect(200, "GET", "/model-providers", nil)
	e.expect(200, "GET", "/model-providers/anthropic/models", nil)
	e.expect(404, "GET", "/model-providers/nope/models", nil)
	e.expect(200, "GET", "/cli-templates", nil)

	// Agent
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "POST", e.agents(), map[string]any{"name": "", "model": map[string]string{"provider": "anthropic", "modelId": "nope"}}, "X-Contract-Invalid-Request", "1"))
	if len(bad.Error.Fields) != 2 {
		t.Fatalf("应给出字段级错误：%+v", bad)
	}
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}, "prompt": "p"}))
	e.expect(409, "POST", e.agents(), map[string]any{"name": "助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	a := "/agents/" + agent.ID.String()
	e.expect(200, "GET", e.agents(), nil)
	e.expect(200, "GET", a, nil)
	e.expect(200, "PATCH", a, map[string]any{"prompt": "新的", "expectedPromptVersion": 1})
	e.expect(404, "GET", "/agents/"+uuid.NewString(), nil)

	// Agent 文件夹：直接读写工作目录
	e.expect(200, "PUT", a+"/folder/content?path=guide/a.md", []byte("v1"))
	e.expect(409, "PUT", a+"/folder/content?path=guide/a.md&create=true", []byte("v2"))
	e.expect(200, "PUT", a+"/folder/content?path=guide/a.md", []byte("v2"))
	_, body, _ := e.do("GET", a+"/folder/content?path=guide/a.md", nil)
	if string(body) != "v2" {
		t.Fatalf("读取内容不对：%q", body)
	}
	e.expect(200, "GET", a+"/folder/entries", nil)
	e.expect(201, "POST", a+"/folder/directories", map[string]any{"path": "docs"})
	e.expect(200, "POST", a+"/folder/move", map[string]any{"from": "guide/a.md", "to": "docs/a.md"})
	e.expect(422, "GET", a+"/folder/entries?path=../x", nil)
	e.expect(204, "DELETE", a+"/folder/entries?path=docs", nil)
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/folder/entries", nil)

	// 可用 CLI 与凭证
	e.expect(200, "PUT", a+"/clis/gh", nil)
	e.expect(200, "PUT", a+"/clis/jq", nil)
	private := e.platformAgent("端点私有凭证")
	privatePath := "/agents/" + private.ID.String()
	e.expect(200, "PUT", privatePath+"/clis/gh", nil)
	e.expect(200, "PUT", privatePath+"/clis/jq", nil)

	cred := decode[apitypes.CredentialMeta](t, e.expect(200, "PUT", privatePath+"/clis/gh/credential", map[string]any{"fields": map[string]string{"token": "ghp_x"}}))
	if cred.Status != "set" || cred.EnvNames[0] != "GH_TOKEN" {
		t.Fatalf("凭证元数据不对：%+v", cred)
	}
	e.expect(422, "PUT", privatePath+"/clis/jq/credential", map[string]any{"fields": map[string]string{"x": "y"}})
	e.expect(200, "PUT", privatePath+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk"})
	detail := decode[apitypes.Agent](t, e.expect(200, "GET", privatePath, nil))
	if detail.ModelCredential == nil || len(detail.Clis) != 2 || strings.Contains(string(e.expect(200, "GET", privatePath, nil)), "ghp_x") {
		t.Fatalf("详情不对或泄露明文：%+v", detail)
	}
	e.expect(204, "DELETE", privatePath+"/model-credential", nil)
	e.expect(204, "DELETE", privatePath+"/clis/gh/credential", nil)
	e.expect(204, "DELETE", a+"/clis/jq", nil)

	// Skills
	md := decode[apitypes.SkillTemplate](t, e.expect(201, "POST", "/skills", map[string]any{"skillMd": "---\nname: notes\ndescription: 记笔记\n---\n"}))
	zipped := decode[apitypes.SkillTemplate](t, e.expect(201, "POST", "/skills", zipUpload(t, "pdf-tools", "PDF")))
	e.expect(409, "POST", "/skills", zipUpload(t, "pdf-tools", "PDF"))
	e.expect(200, "GET", "/skills", nil)
	e.expect(200, "GET", "/skills/"+zipped.ID.String(), nil)
	e.expect(200, "PUT", "/skills/"+zipped.ID.String()+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\ndescription: 新\n---\n"})
	e.expect(422, "PUT", "/skills/"+zipped.ID.String()+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\n---\n"})
	e.expect(200, "PUT", "/skills/"+zipped.ID.String(), zipUpload(t, "pdf-tools", "替换"))
	k := decode[apitypes.AgentSkill](t, e.expect(201, "POST", a+"/skills", map[string]any{"templateId": zipped.ID}))
	ks := a + "/skills/" + k.ID.String()
	e.expect(200, "GET", a+"/skills", nil)
	e.expect(200, "GET", ks, nil)
	e.expect(200, "PUT", ks+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\ndescription: 我的\n---\n"})
	e.expect(200, "POST", ks+"/recopy", nil)
	e.expect(204, "DELETE", "/skills/"+zipped.ID.String(), nil)
	e.expect(409, "POST", ks+"/recopy", nil)
	e.expect(204, "DELETE", ks, nil)
	e.expect(204, "DELETE", "/skills/"+md.ID.String(), nil)

	// 对话
	sent := decode[apitypes.SendMessageResult](t, e.expect(202, "POST", a+"/messages", map[string]any{"text": "block 你好"}))
	busy := decode[apitypes.ErrorBody](t, e.expect(409, "POST", a+"/messages", map[string]any{"text": "第二条"}))
	if busy.Error.Code != "agent_busy" {
		t.Fatalf("应返回 agent_busy：%+v", busy)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "排队", "whenBusy": "queue"})
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "打断", "whenBusy": "interrupt"})
	e.expect(422, "POST", a+"/messages", map[string]any{"text": "  "})
	e.expect(404, "GET", "/agents/not-a-uuid", nil, "X-Contract-Invalid-Request", "1")
	e.expect(200, "GET", a+"/status", nil)
	waitIdle(t, e, a)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=10", nil))
	if len(page.Messages) != 6 || page.Messages[0].Run.ID != sent.Run.ID {
		t.Fatalf("历史不对：%d", len(page.Messages))
	}
	e.expect(200, "GET", a+"/messages?limit=2&before="+page.Messages[2].ID.String(), nil)
	e.expect(422, "GET", a+"/messages?limit=500", nil, "X-Contract-Invalid-Request", "1")
	e.expect(200, "POST", a+"/session/reset", nil)

	e.expect(204, "DELETE", a, nil)
	e.expect(404, "GET", a, nil)
}

// waitIdle 等到 Agent 完全空闲：数据库中没有进行中或排队的执行，且忙闲锁已交接释放。
// 执行结束写库与交接锁之间有一个短暂的窗口，只看状态接口时紧接着发送的消息可能被判为正忙。
func waitIdle(t *testing.T, e *env, a string) {
	t.Helper()
	agentID := strings.TrimPrefix(a, "/agents/")
	for range 200 {
		st := decode[apitypes.AgentStatus](t, e.expect(200, "GET", a+"/status", nil))
		owner, err := e.co.LockOwner(context.Background(), agentID)
		if !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 && err == nil && owner == "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待空闲超时")
}

type sseEvent struct{ id, event, data string }

// readSSE 读取事件流，把事件发到通道，直到连接关闭。
func readSSE(t *testing.T, e *env, a, lastID string) (<-chan sseEvent, func()) {
	return readSSEPath(t, e, a+"/events", lastID)
}

// readSSEPath 同 readSSE，但使用完整路径（可带查询参数）。
func readSSEPath(t *testing.T, e *env, path, lastID string) (<-chan sseEvent, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/v1"+path, nil)
	req.Header.Set("Origin", "http://localhost:5173")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("事件流应带 CORS 头：%v", resp.Header)
	}
	ch := make(chan sseEvent, 100)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.event != "" {
					ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return ch, cancel
}

func next(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("事件流已关闭")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("等待事件超时")
	}
	return sseEvent{}
}

func TestSSE(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "a", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	a := "/agents/" + agent.ID.String()

	ch, stop := readSSE(t, e, a, "")
	snap := next(t, ch)
	if snap.event != "snapshot" || snap.id == "" {
		t.Fatalf("首个事件应是快照：%+v", snap)
	}
	e.v.ValidateSSE(snap.event, []byte(snap.data))

	e.expect(202, "POST", a+"/messages", map[string]any{"text": "你好"})
	var events []sseEvent
	var text strings.Builder
	for {
		ev := next(t, ch)
		e.v.ValidateSSE(ev.event, []byte(ev.data))
		events = append(events, ev)
		if ev.event == "run.text" {
			text.WriteString(decode[apitypes.SseRunText](t, []byte(ev.data)).Delta)
		}
		if ev.event == "run.tool" && (strings.Contains(ev.data, "args") || strings.Contains(ev.data, "result")) {
			t.Fatalf("工具事件不应含参数或输出：%s", ev.data)
		}
		if ev.event == "run.ended" {
			break
		}
	}
	stop()
	if text.String() != "你好" {
		t.Fatalf("文字增量拼接不对：%q", text.String())
	}
	types := map[string]bool{}
	for _, ev := range events {
		types[ev.event] = true
	}
	for _, want := range []string{"message.created", "run.updated", "run.text", "run.tool", "run.ended"} {
		if !types[want] {
			t.Errorf("缺少事件 %s", want)
		}
	}

	// 断线后带上次事件 ID 重连：不推快照，补发之后的事件。
	mid := events[1].id
	ch2, stop2 := readSSE(t, e, a, mid)
	first := next(t, ch2)
	if first.event == "snapshot" || first.id != events[2].id {
		t.Fatalf("续传应从上次事件之后开始：%+v，期望 %s", first, events[2].id)
	}
	stop2()

	// 超出保留范围：推送快照。
	ch3, stop3 := readSSE(t, e, a, "1-0")
	if ev := next(t, ch3); ev.event != "snapshot" {
		t.Fatalf("超出保留范围应推送快照：%+v", ev)
	}
	stop3()

	// 会话重开事件。
	ch4, stop4 := readSSE(t, e, a, "")
	next(t, ch4)
	e.expect(200, "POST", a+"/session/reset", nil)
	ev := next(t, ch4)
	e.v.ValidateSSE(ev.event, []byte(ev.data))
	if ev.event != "session.updated" {
		t.Fatalf("应推送会话更新：%+v", ev)
	}
	stop4()
}

var _ = store.Agent{}
