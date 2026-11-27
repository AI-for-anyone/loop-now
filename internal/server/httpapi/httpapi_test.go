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
	"testing/fstest"
	"time"

	"github.com/google/uuid"

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
	"loop-now/internal/server/localmode"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/webui"
	"loop-now/internal/testutil/sqlitetest"
)

// quickExec completes replies immediately; a message containing "block" blocks until it is interrupted or released.
type quickExec struct {
	mu       sync.Mutex
	running  map[uuid.UUID]chan struct{}
	sessions []executor.ManagedSession
	// When set, the platform commands' maintenance operations return these errors.
	stopErr, exportErr, listErr error
	// When compactSkip is set, manual compaction completes normally as "nothing compacted", with it as the reason.
	compactSkip string
}

func (q *quickExec) Execute(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
	if req.Origin == executor.OriginConsoleCompact {
		if q.compactSkip != "" {
			return executor.Result{Outcome: executor.OutcomeCompleted, Compaction: &executor.CompactionResult{Skipped: q.compactSkip}, ConfigVersion: 1}, nil
		}
		return executor.Result{Outcome: executor.OutcomeCompleted, Compaction: &executor.CompactionResult{TokensBefore: 4200, EstimatedTokensAfter: 350}, ConfigVersion: 1}, nil
	}
	ch := make(chan struct{})
	q.mu.Lock()
	q.running[req.AgentID] = ch
	q.mu.Unlock()
	defer func() { q.mu.Lock(); delete(q.running, req.AgentID); q.mu.Unlock() }()
	sink.Phase(executor.PhaseLaunching, "pi")
	sink.Running(1, executor.Selection{})
	sink.Tool("t1", "bash", executor.ToolStarted)
	sink.TextDelta("Hel")
	sink.TextDelta("lo")
	if strings.Contains(req.Message, "block") {
		select {
		case <-ch:
			return executor.Result{Outcome: executor.OutcomeInterrupted, Text: "Hello", ConfigVersion: 1}, nil
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	sink.Tool("t1", "bash", executor.ToolSucceeded)
	if strings.Contains(req.Message, "fail") {
		return executor.Result{Outcome: executor.OutcomeFailed, Text: "Hello", ErrorCode: executor.ErrWorkspaceSyncFailed,
			ErrorMessage: "source app: clone failed", ConfigVersion: 1}, nil
	}
	return executor.Result{Outcome: executor.OutcomeCompleted, Text: "Hello", ConfigVersion: 1}, nil
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
func (q *quickExec) StopIdle(context.Context, uuid.UUID, string) (bool, error) {
	if q.stopErr != nil {
		return false, q.stopErr
	}
	return true, nil
}
func (q *quickExec) ExportSession(_ context.Context, _ uuid.UUID, _, _, format string) (executor.SessionExport, error) {
	if q.exportErr != nil {
		return executor.SessionExport{}, q.exportErr
	}
	if format == "jsonl" {
		return executor.SessionExport{Filename: "session.jsonl", ContentType: "application/x-ndjson", Data: []byte("{}\n")}, nil
	}
	return executor.SessionExport{Filename: "session.html", ContentType: "text/html; charset=utf-8", Data: []byte("<html>exported</html>")}, nil
}
func (q *quickExec) ResolveSession(ctx context.Context, agentID uuid.UUID, runtime, sessionID, currentFile string) (*executor.ManagedSession, error) {
	all, err := q.ListSessions(ctx, agentID, runtime, currentFile)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == sessionID {
			return &all[i], nil
		}
	}
	return nil, nil
}
func (q *quickExec) ListSessions(_ context.Context, _ uuid.UUID, _, currentFile string) ([]executor.ManagedSession, error) {
	if q.listErr != nil {
		return nil, q.listErr
	}
	out := append([]executor.ManagedSession(nil), q.sessions...)
	for i := range out {
		out[i].Current = out[i].Path == currentFile
	}
	return out, nil
}

type env struct {
	t      *testing.T
	ex     *quickExec
	srv    *httptest.Server
	api    *httpapi.Server
	v      *contracttest.Validator
	client *http.Client
	co     *coord.Coord
	st     *store.Store
	conv   *conversation.Service
	dc     store.DefaultContext

	ext          *httptest.Server // external port (started on demand)
	extV, agentV *contracttest.Validator
}

// agents returns the path of the default project's Agent list, which is also the create path.
func (e *env) agents() string { return "/projects/" + e.dc.Project.ID.String() + "/agents" }

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	co := coord.New(coord.Options{Prefix: "test:", StreamMaxLen: 1000, OverviewMaxLen: 1000})
	dc, err := st.EnsureDefaults(ctx, "Default organization", "Default project")
	if err != nil {
		t.Fatal(err)
	}
	tmpls, _ := clitemplate.Load("")
	models := modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X", Input: []string{"text"}}}},
	})
	limits := apitypes.Limits{MaxMessageImages: 4, MaxMessageImageBytes: 5 << 20, MaxMessageImagePixels: 20_000_000, MaxUploadFile: 1 << 20, MaxFolderUpload: 1 << 20, MaxMessage: 1 << 16, MaxInboxMessage: 1 << 16, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096,
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
		AbortPollInterval: time.Second, ReconcileInterval: time.Second}, log)
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
		Folders:   folders.New(st, nil, folders.Options{WorkRoot: t.TempDir(), ContextDir: t.TempDir(), MaxUpload: limits.MaxFolderUpload, GitBin: "git", CloneTimeout: time.Minute}, log),
		LocalMode: &localmode.Service{Store: st},
	}
	api.Plugins = &plugins.Service{Store: st, Root: filepath.Join(t.TempDir(), "plugins"), GitBin: "git", PlatformVersion: "dev", Log: log}
	if err := api.Plugins.Init(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	api.Plugins.APIURL = srv.URL + "/api/v1"
	v := contracttest.New(t, srv.URL)
	return &env{t: t, srv: srv, api: api, v: v, client: v.Client(), co: co, st: st, conv: conv, dc: dc, ex: ex}
}

// do sends a request (validated against the contract) and returns the status code and the response body.
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
		e.t.Fatalf("%s %s: want %d, got %d: %s", method, path, status, got, out)
	}
	return out
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode response: %v: %s", err, b)
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

// TestRouteCoverage compares the route table and the contract in both directions: paths and methods, operationId, and
// x-access in the console contract.
func TestRouteCoverage(t *testing.T) {
	e := newEnv(t)
	check := func(file string, routes []httpapi.Route, withAccess bool) {
		t.Helper()
		meta := contracttest.LoadOperations(t, file)
		seen := map[string]bool{}
		for _, r := range routes {
			// A path parameter spanning several segments ({path...}) is written {path} in the contract.
			key := r.Method + " " + strings.ReplaceAll(r.Pattern, "...}", "}")
			seen[key] = true
			m, ok := meta[key]
			if !ok {
				t.Errorf("%s: registered route is not in the contract: %s", file, key)
				continue
			}
			if m.OperationID != r.Op {
				t.Errorf("%s: operationId of %s should be %s, the route table has %s", file, key, m.OperationID, r.Op)
			}
			if withAccess && m.Access != r.Access.String() {
				t.Errorf("%s: x-access of %s is %q, the route table has %q", file, key, m.Access, r.Access)
			}
			if withAccess && m.PluginScope != httpapi.PluginScopes()[r.Op] {
				t.Errorf("%s: x-plugin-scope of %s is %q, the plugin scope table has %q", file, key, m.PluginScope, httpapi.PluginScopes()[r.Op])
			}
		}
		for key, m := range meta {
			if !seen[key] {
				t.Errorf("%s: contract operation is not implemented: %s", file, key)
			}
			if withAccess && m.Access != "console" && m.Access != "console-and-external" {
				t.Errorf("%s: %s has a missing or invalid x-access: %q", file, key, m.Access)
			}
		}
	}
	if (httpapi.Route{}).Access != httpapi.ConsoleOnly {
		t.Fatal("routes should default to console only, so a new route is never exposed externally for lack of a declaration")
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
		t.Fatal("a response that violates the contract should be caught")
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
		t.Errorf("a Host not on the list should be rejected: %d", r.StatusCode)
	}
	if r := send("POST", "/api/v1/platform/agents", "https://evil.example.com", "", strings.NewReader(`{"name":"x","model":{"provider":"anthropic","modelId":"claude-x"}}`)); r.StatusCode != http.StatusForbidden {
		t.Errorf("an origin not on the allowlist should be rejected: %d", r.StatusCode)
	}
	if _, body, _ := e.do("GET", e.agents(), nil); len(decode[map[string][]any](t, body)["agents"]) != 0 {
		t.Error("a rejected write request should not write to the database")
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
		t.Errorf("a preflight from an allowed origin should pass: %d %v", pre.StatusCode, pre.Header)
	}
	_ = r
	if r := send("GET", "/api/v1/context", "", "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("a local request without Origin should pass: %d", r.StatusCode)
	}
	r = send("GET", "/api/v1/health", "http://localhost:5173", "", nil)
	if r.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || r.Header.Get("Access-Control-Expose-Headers") != "ETag, Loop-Now-Request-Id, Loop-Now-Version, Content-Disposition, Content-Length" {
		t.Errorf("responses to an allowed origin should expose ETag, the request headers, Content-Disposition and Content-Length: %v", r.Header)
	}
	if r := send("GET", "/healthz", "", "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("/healthz should be available: %d", r.StatusCode)
	}
	// The console served by the server itself is same-origin: the Origin on write requests equals this request's
	// scheme://Host, so it needs no allowlist and gets no CORS headers.
	if r := send("POST", "/api/v1/platform/agents", e.srv.URL, "", strings.NewReader(`{"name":"same-origin","model":{"provider":"anthropic","modelId":"claude-x"}}`)); r.StatusCode != http.StatusCreated || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a same-origin write request should pass without CORS headers: %d %v", r.StatusCode, r.Header)
	}
	// Another allowed host name on the same port is not same-origin.
	other := strings.Replace(e.srv.URL, "127.0.0.1", "localhost", 1)
	if r := send("POST", "/api/v1/platform/agents", other, "", strings.NewReader(`{"name":"cross-origin","model":{"provider":"anthropic","modelId":"claude-x"}}`)); r.StatusCode != http.StatusForbidden {
		t.Errorf("a request whose Host differs from its Origin is not same-origin: %d", r.StatusCode)
	}
	// Without an allowlist (LOOP_NOW_CONSOLE_ORIGINS=none), even the development port 5173 is rejected, while
	// same-origin still passes.
	e.api.O.ConsoleOrigins = nil
	locked := httptest.NewServer(e.api.Handler())
	defer locked.Close()
	e.srv = locked
	if r := send("GET", "/api/v1/context", "http://localhost:5173", "", nil); r.StatusCode != http.StatusForbidden {
		t.Errorf("without an allowlist, cross-origin requests should be rejected: %d", r.StatusCode)
	}
	if r := send("GET", "/api/v1/context", locked.URL, "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("without an allowlist, same-origin requests should pass: %d", r.StatusCode)
	}
}

// On the console port, paths outside the API go to the console's static files; the API, the Agent API and /healthz are
// unaffected, and the external port does not serve the console.
func TestConsoleServedBesideAPI(t *testing.T) {
	e := newEnv(t)
	e.api.O.Console = webui.Handler(fstest.MapFS{"index.html": {Data: []byte("<title>console</title>")}})
	srv := httptest.NewServer(e.api.Handler())
	defer srv.Close()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, p := range []string{"/", "/agents/x"} {
		if code, body := get(p); code != 200 || !strings.Contains(body, "console") {
			t.Errorf("%s: %d %q", p, code, body)
		}
	}
	if code, body := get("/api/v1/context"); code != 200 || strings.Contains(body, "console") {
		t.Errorf("the console should not take over the API: %d", code)
	}
	if code, body := get("/api/v1/no-such-route"); code != 404 || strings.Contains(body, "console") {
		t.Errorf("a missing path under the API should not fall back to the console: %d %q", code, body)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Errorf("/healthz: %d", code)
	}
	ext := httptest.NewServer(e.api.ExternalHandler(nil))
	defer ext.Close()
	resp, err := http.Get(ext.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "console") {
		t.Errorf("the external port should not serve the console: %d", resp.StatusCode)
	}
}

func TestAllEndpoints(t *testing.T) {
	e := newEnv(t)
	ctxBody := decode[apitypes.Context](t, e.expect(200, "GET", "/context", nil))
	if ctxBody.Limits.MaxMessage != 1<<16 || ctxBody.Project.Name != "Default project" {
		t.Fatalf("unexpected context: %+v", ctxBody)
	}
	e.expect(200, "GET", "/health", nil)
	e.expect(200, "GET", "/model-providers", nil)
	e.expect(200, "GET", "/model-providers/anthropic/models", nil)
	e.expect(404, "GET", "/model-providers/nope/models", nil)
	e.expect(200, "GET", "/cli-templates", nil)

	// Agent
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "POST", e.agents(), map[string]any{"name": "", "model": map[string]string{"provider": "anthropic", "modelId": "nope"}}, "X-Contract-Invalid-Request", "1"))
	if len(bad.Error.Fields) != 2 {
		t.Fatalf("want field-level errors: %+v", bad)
	}
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Assistant", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}, "prompt": "p"}))
	e.expect(409, "POST", e.agents(), map[string]any{"name": "Assistant", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	a := "/agents/" + agent.ID.String()
	e.expect(200, "GET", e.agents(), nil)
	e.expect(200, "GET", a, nil)
	e.expect(200, "PATCH", a, map[string]any{"prompt": "new", "expectedPromptVersion": 1})
	e.expect(404, "GET", "/agents/"+uuid.NewString(), nil)

	// Agent folder: reads and writes the working directory directly
	e.expect(200, "PUT", a+"/folder/content?path=guide/a.md", []byte("v1"))
	e.expect(409, "PUT", a+"/folder/content?path=guide/a.md&create=true", []byte("v2"))
	e.expect(200, "PUT", a+"/folder/content?path=guide/a.md", []byte("v2"))
	_, body, _ := e.do("GET", a+"/folder/content?path=guide/a.md", nil)
	if string(body) != "v2" {
		t.Fatalf("unexpected content read back: %q", body)
	}
	e.expect(200, "GET", a+"/folder/entries", nil)
	e.expect(201, "POST", a+"/folder/directories", map[string]any{"path": "docs"})
	e.expect(200, "POST", a+"/folder/move", map[string]any{"from": "guide/a.md", "to": "docs/a.md"})
	e.expect(422, "GET", a+"/folder/entries?path=../x", nil)
	e.expect(204, "DELETE", a+"/folder/entries?path=docs", nil)
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/folder/entries", nil)

	// Available CLIs and credentials
	e.expect(200, "PUT", a+"/clis/gh", nil)
	e.expect(200, "PUT", a+"/clis/jq", nil)
	private := e.platformAgent("Endpoint private credentials")
	privatePath := "/agents/" + private.ID.String()
	e.expect(200, "PUT", privatePath+"/clis/gh", nil)
	e.expect(200, "PUT", privatePath+"/clis/jq", nil)

	cred := decode[apitypes.CredentialMeta](t, e.expect(200, "PUT", privatePath+"/clis/gh/credential", map[string]any{"fields": map[string]string{"token": "ghp_x"}}))
	if cred.Status != "set" || cred.EnvNames[0] != "GH_TOKEN" {
		t.Fatalf("unexpected credential metadata: %+v", cred)
	}
	e.expect(422, "PUT", privatePath+"/clis/jq/credential", map[string]any{"fields": map[string]string{"x": "y"}})
	e.expect(200, "PUT", privatePath+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk"})
	detail := decode[apitypes.Agent](t, e.expect(200, "GET", privatePath, nil))
	if detail.ModelCredential == nil || len(detail.Clis) != 2 || strings.Contains(string(e.expect(200, "GET", privatePath, nil)), "ghp_x") {
		t.Fatalf("wrong detail or plaintext leaked: %+v", detail)
	}
	e.expect(204, "DELETE", privatePath+"/model-credential", nil)
	e.expect(204, "DELETE", privatePath+"/clis/gh/credential", nil)
	e.expect(204, "DELETE", a+"/clis/jq", nil)

	// Skills
	md := decode[apitypes.SkillTemplate](t, e.expect(201, "POST", "/skills", map[string]any{"skillMd": "---\nname: notes\ndescription: Take notes\n---\n"}))
	zipped := decode[apitypes.SkillTemplate](t, e.expect(201, "POST", "/skills", zipUpload(t, "pdf-tools", "PDF")))
	e.expect(409, "POST", "/skills", zipUpload(t, "pdf-tools", "PDF"))
	e.expect(200, "GET", "/skills", nil)
	e.expect(200, "GET", "/skills/"+zipped.ID.String(), nil)
	e.expect(200, "PUT", "/skills/"+zipped.ID.String()+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\ndescription: New\n---\n"})
	e.expect(422, "PUT", "/skills/"+zipped.ID.String()+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\n---\n"})
	e.expect(200, "PUT", "/skills/"+zipped.ID.String(), zipUpload(t, "pdf-tools", "Replacement"))
	k := decode[apitypes.AgentSkill](t, e.expect(201, "POST", a+"/skills", map[string]any{"templateId": zipped.ID}))
	ks := a + "/skills/" + k.ID.String()
	e.expect(200, "GET", a+"/skills", nil)
	e.expect(200, "GET", ks, nil)
	e.expect(200, "PUT", ks+"/skill-md", map[string]any{"content": "---\nname: pdf-tools\ndescription: Mine\n---\n"})
	e.expect(200, "POST", ks+"/recopy", nil)
	e.expect(204, "DELETE", "/skills/"+zipped.ID.String(), nil)
	e.expect(409, "POST", ks+"/recopy", nil)
	e.expect(204, "DELETE", ks, nil)
	e.expect(204, "DELETE", "/skills/"+md.ID.String(), nil)

	// Conversation
	sent := decode[apitypes.SendMessageResult](t, e.expect(202, "POST", a+"/messages", map[string]any{"text": "block Hello"}))
	busy := decode[apitypes.ErrorBody](t, e.expect(409, "POST", a+"/messages", map[string]any{"text": "second"}))
	if busy.Error.Code != "agent_busy" {
		t.Fatalf("want agent_busy: %+v", busy)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "queued", "whenBusy": "queue"})
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "interrupt", "whenBusy": "interrupt"})
	e.expect(422, "POST", a+"/messages", map[string]any{"text": "  "}, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/agents/not-a-uuid", nil, "X-Contract-Invalid-Request", "1")
	e.expect(200, "GET", a+"/status", nil)
	waitIdle(t, e, a)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages?limit=10", nil))
	if len(page.Messages) != 6 || page.Messages[0].Run.ID != sent.Run.ID {
		t.Fatalf("unexpected history: %d", len(page.Messages))
	}
	e.expect(200, "GET", a+"/messages?limit=2&before="+page.Messages[2].ID.String(), nil)
	e.expect(422, "GET", a+"/messages?limit=500", nil, "X-Contract-Invalid-Request", "1")
	e.expect(200, "POST", a+"/session/reset", nil)

	e.expect(204, "DELETE", a, nil)
	e.expect(404, "GET", a, nil)
}

// waitIdle waits until the Agent is fully idle: no running or queued runs in the database, and the busy lock has been
// handed over and released. There is a short window between a run writing its end to the database and handing over the
// lock; when looking only at the status endpoint, a message sent right afterwards may be judged busy.
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
	t.Fatal("timed out waiting for idle")
}

type sseEvent struct{ id, event, data string }

// readSSE reads the event stream and sends the events to a channel until the connection closes.
func readSSE(t *testing.T, e *env, a, lastID string) (<-chan sseEvent, func()) {
	return readSSEPath(t, e, a+"/events", lastID)
}

// readSSEPath is like readSSE but takes the full path (query parameters allowed).
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
		t.Fatalf("the event stream should carry CORS headers: %v", resp.Header)
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
			t.Fatal("event stream closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
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
		t.Fatalf("the first event should be a snapshot: %+v", snap)
	}
	e.v.ValidateSSE(snap.event, []byte(snap.data))

	e.expect(202, "POST", a+"/messages", map[string]any{"text": "Hello"})
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
			t.Fatalf("tool events should not contain arguments or output: %s", ev.data)
		}
		if ev.event == "run.ended" {
			break
		}
	}
	stop()
	if text.String() != "Hello" {
		t.Fatalf("wrong concatenated text deltas: %q", text.String())
	}
	types := map[string]bool{}
	for _, ev := range events {
		types[ev.event] = true
	}
	for _, want := range []string{"message.created", "run.updated", "run.text", "run.tool", "run.ended"} {
		if !types[want] {
			t.Errorf("missing event %s", want)
		}
	}

	// Reconnecting with the last event ID after a disconnect: no snapshot, the later events are sent.
	mid := events[1].id
	ch2, stop2 := readSSE(t, e, a, mid)
	first := next(t, ch2)
	if first.event == "snapshot" || first.id != events[2].id {
		t.Fatalf("resuming should start after the last event: %+v, want %s", first, events[2].id)
	}
	stop2()

	// Outside the retained range: a snapshot is pushed.
	ch3, stop3 := readSSE(t, e, a, "1-0")
	if ev := next(t, ch3); ev.event != "snapshot" {
		t.Fatalf("outside the retained range a snapshot should be pushed: %+v", ev)
	}
	stop3()

	// Session reopen event.
	ch4, stop4 := readSSE(t, e, a, "")
	next(t, ch4)
	e.expect(200, "POST", a+"/session/reset", nil)
	ev := next(t, ch4)
	e.v.ValidateSSE(ev.event, []byte(ev.data))
	if ev.event != "session.updated" {
		t.Fatalf("want a session update pushed: %+v", ev)
	}
	stop4()
}

var _ = store.Agent{}

// trimStream appends more filler events than the event stream retains (1000 in env), so that all earlier events are
// trimmed: the same as the events in Redis being lost, or the event stream starting empty after a server restart.
func trimStream(t *testing.T, add func() (string, error)) {
	t.Helper()
	for range 1001 {
		if _, err := add(); err != nil {
			t.Fatal(err)
		}
	}
}
