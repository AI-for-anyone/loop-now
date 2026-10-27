package commands_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func TestConfigCommands(t *testing.T) {
	e := newEnv(t)
	ca := writeCAFile(t)
	rel, _ := filepath.Rel(mustGetwd(t), ca)
	e.stdin = "  " + extToken + "  \nignored second line\n"
	d := e.run("config", "set", "default", "--server", "https://loop.example.com:8781/", "--ca-file", rel, "--token-stdin").ok(t)
	want := map[string]any{"name": "default", "server": "https://loop.example.com:8781", "caFile": ca, "tokenSet": true,
		"tokenId": "0192f1a2-b3c4-7d8e-9f0a-1b2c3d4e5f60", "tokenPrefix": "lne_0192f1a2"}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("摘要 %v", d)
	}
	path := config.Path(e.home)
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("权限 %o", info.Mode().Perm())
	}
	f, _, _ := config.Load(path)
	if f.Profiles["default"].Token != extToken {
		t.Fatal("令牌应去掉首尾空白后保存")
	}

	e.run("config", "set", "staging", "--server", "https://staging.example").ok(t)
	e.vars[conn.EnvProfile] = "staging"
	e.vars[conn.EnvURL] = "https://env.example"
	list := e.run("config", "list").ok(t)
	if sel := list["selected"].(map[string]any); sel["name"] != "staging" || sel["by"] != conn.EnvProfile || sel["exists"] != true {
		t.Fatalf("selected %v", sel)
	}
	if ov := list["envOverrides"].([]any); len(ov) != 1 || ov[0] != conn.EnvURL {
		t.Fatalf("envOverrides %v", ov)
	}
	if sel := e.run("config", "list", "--profile", "prod").ok(t)["selected"].(map[string]any); sel["name"] != "prod" || sel["by"] != "--profile" || sel["exists"] != false {
		t.Fatalf("selected %v", sel)
	}
	delete(e.vars, conn.EnvProfile)
	delete(e.vars, conn.EnvURL)

	e.run("config", "set", "default", "--clear-ca").ok(t)
	f, _, _ = config.Load(path)
	if f.Profiles["default"].CAFile != "" || f.Profiles["default"].Token != extToken {
		t.Fatalf("--clear-ca 只应清除 CA：%+v", f.Profiles["default"])
	}

	before, _ := os.ReadFile(path)
	e.stdin = agentToken + "\n"
	e.run("config", "set", "default", "--token-stdin").fails(t, output.CodeTokenKindMismatch, 3)
	e.stdin = "lne_short_x\n"
	e.run("config", "set", "default", "--token-stdin").fails(t, output.CodeInvalidArgument, 2)
	e.stdin = ""
	e.run("config", "set", "default", "--token-stdin").fails(t, output.CodeInvalidArgument, 2)
	e.run("config", "set", "default", "--server", "http://loop.example.com").fails(t, output.CodeInvalidServerURL, 3)
	e.run("config", "set", "default", "--ca-file", writeFile(t, "bad.pem", "not pem")).fails(t, output.CodeCAFileUnreadable, 3)
	e.run("config", "set", "bad name", "--clear-ca").fails(t, output.CodeInvalidArgument, 2)
	e.run("config", "set", "default").fails(t, output.CodeMissingArgument, 2)
	e.run("config", "set", "default", "--ca-file", ca, "--clear-ca").fails(t, output.CodeInvalidArgument, 2)
	if after, _ := os.ReadFile(path); string(before) != string(after) {
		t.Fatal("失败的 config set 改动了配置文件")
	}

	e.run("config", "delete", "staging", "--yes").ok(t)
	e.run("config", "delete", "staging", "--yes").fails(t, output.CodeProfileNotFound, 3)

	os.Chmod(path, 0o644)
	e.run("config", "list").fails(t, output.CodeConfigInsecurePermissions, 3)
	e.run("skill", "list").fails(t, output.CodeConfigInsecurePermissions, 3)

	ag := newEnv(t).agent("http://127.0.0.1:1")
	ag.run("config", "list").fails(t, output.CodeCommandNotAllowed, 2)
}

func mustGetwd(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestProfileSelection(t *testing.T) {
	s := clitest.NewExternal(t)
	s.On("GET /skills", clitest.Sample{Status: 200, Body: map[string]any{"skills": []any{}}})
	e := newEnv(t)
	e.stdin = extToken + "\n"
	e.run("config", "set", "staging", "--server", s.URL(), "--ca-file", s.CAFile(), "--token-stdin").ok(t)
	e.run("config", "set", "default", "--server", "https://unreachable.invalid").ok(t)

	r := e.run("skill", "list", "--profile", "staging")
	r.ok(t)
	if r.Env.Meta.Profile != "staging" || r.Env.Meta.Identity != "external" {
		t.Fatalf("meta %+v", r.Env.Meta)
	}
	e.run("skill", "list", "--profile", "prod").fails(t, output.CodeProfileNotFound, 3)
	if len(s.Requests()) != 1 {
		t.Fatal("profile 不存在时不应发出请求")
	}
	// 环境变量优先于 profile。
	e.vars[conn.EnvProfile] = "default"
	e.vars[conn.EnvURL] = s.URL()
	e.vars[conn.EnvCAFile] = s.CAFile()
	e.vars[conn.EnvToken] = extToken
	if r := e.run("skill", "list"); r.Code != 0 || r.Env.Meta.Profile != "default" {
		t.Fatalf("%s", r.Stdout)
	}
	// Agent 身份与外部参数同时出现。
	e.vars[conn.EnvAgentToken] = agentToken
	e.run("inbox", "list").fails(t, output.CodeIdentityConflict, 3)
}

func TestConnectionErrors(t *testing.T) {
	e := newEnv(t)
	e.block = true
	e.run("whoami").fails(t, output.CodeConnectionNotConfigured, 3)
	e.vars[conn.EnvAgentToken] = extToken
	e.vars[conn.EnvAgentURL] = "http://127.0.0.1:1/agent/v1"
	e.run("inbox", "list").fails(t, output.CodeTokenKindMismatch, 3)
	e.vars[conn.EnvAgentToken] = agentToken
	e.run("inbox", "list", "--server", "https://x.example").fails(t, output.CodeIdentityConflict, 3)
	if e.dialCount() != 0 {
		t.Fatal("配置错误时不应发出请求")
	}

	s := clitest.NewExternal(t)
	x := newEnv(t)
	x.vars[conn.EnvURL], x.vars[conn.EnvToken] = s.URL(), extToken
	x.run("skill", "list").fails(t, output.CodeTLSVerifyFailed, 3)
	x.vars[conn.EnvURL] = strings.Replace(s.URL(), "https://", "http://", 1)
	x.run("skill", "list").fails(t, output.CodeInvalidServerURL, 3)
}

func TestTimeoutFlag(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	e := newEnv(t).agent(srv.URL)
	start := time.Now()
	r := e.run("inbox", "send", "@console", "--text", "构建完成", "--timeout", "200ms")
	r.fails(t, output.CodeTimeout, 4)
	if r.Env.Meta.Attempts != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("发送不应重试：%+v", r.Env.Meta)
	}
}

func TestVersionComparison(t *testing.T) {
	s, e := stub(t)
	s.On("GET /skills", clitest.Sample{Status: 200, Body: map[string]any{"skills": []any{}}})
	s.Version = "9.9.9"
	r := e.run("skill", "list")
	r.ok(t)
	if len(r.Env.Warnings) != 1 || r.Env.Warnings[0].Code != output.WarnVersionMismatch {
		t.Fatalf("%v", r.Env.Warnings)
	}
	d := r.Env.Warnings[0].Details.(map[string]any)
	if d["clientVersion"] != testVersion || d["serverVersion"] != "9.9.9" || !strings.HasPrefix(d["downloadPath"].(string), "/downloads/loopctl/loopctl-") {
		t.Fatalf("%v", d)
	}
	// 契约要求每个响应都带版本头；不带版本头的服务端不符合契约，用不校验契约的桩模拟。
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"kind":"agent","address":"dev","name":"dev"}`)
	}))
	defer bare.Close()
	if r := newEnv(t).agent(bare.URL).run("whoami"); r.Code != 0 || len(r.Env.Warnings) != 1 || r.Env.Warnings[0].Code != output.WarnServerVersionUnknown {
		t.Fatalf("%s", r.Stdout)
	}
	// 失败的请求同样比对版本。
	s.Version = "9.9.9"
	s.On("GET /agents/{agentId}", clitest.Sample{Status: 404, Body: apiError("not_found", "Agent 不存在")})
	r = e.run("agent", "get", agentUUID)
	r.fails(t, "not_found", 7)
	if len(r.Env.Warnings) != 1 || r.Env.Meta.ServerVersion != "9.9.9" {
		t.Fatalf("%s", r.Stdout)
	}
}

// 时间线、对话历史的新取法、用量、异常与健康状态，按 api/openapi.yaml（含 add-observability）的契约验证。
func TestObservabilityCommands(t *testing.T) {
	query := func(s *clitest.Stub) url.Values {
		reqs := s.Requests()
		q, _ := url.ParseQuery(reqs[len(reqs)-1].RawQuery)
		return q
	}
	t.Run("定位到一条失败的回复", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /agents/{agentId}/messages", clitest.Sample{Status: 200, Body: messagePage()})
		s.On("GET /agents/{agentId}/runs/{runId}/timeline", clitest.Sample{Status: 200, Body: runTimeline()})
		e.run("agent", "history", agentUUID, "--around-run", runUUID, "--limit", "9").ok(t)
		if q := query(s); q.Get("aroundRun") != runUUID || q.Get("limit") != "9" || len(q) != 2 {
			t.Fatalf("%v", q)
		}
		e.run("agent", "history", agentUUID, "--after", "0192f1a2-0000-7000-8000-0000000000f1").ok(t)
		if q := query(s); q.Get("after") != "0192f1a2-0000-7000-8000-0000000000f1" || len(q) != 1 {
			t.Fatalf("%v", q)
		}
		d := e.run("agent", "timeline", agentUUID, runUUID).ok(t)
		if d["run"].(map[string]any)["errorCode"] != "pi_error" || len(d["entries"].([]any)) != 3 {
			t.Fatalf("%v", d)
		}
		n := len(s.Requests())
		e.run("agent", "history", agentUUID, "--before", runUUID, "--after", runUUID).fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != n {
			t.Fatal("同时指定两种位置时不应发出请求")
		}
	})
	t.Run("查看今天的用量", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /usage", clitest.Sample{Status: 200, Body: usagePage()})
		d := e.run("usage", "list", "--from", "2026-10-13T00:00:00+08:00").ok(t)
		if q := query(s); q.Get("from") != "2026-10-13T00:00:00+08:00" || q.Has("to") {
			t.Fatalf("%v", q)
		}
		if rows := d["rows"].([]any); len(rows) != 1 || strings.Contains(fmtJSON(d), "cost") {
			t.Fatalf("%v", d)
		}
		e.run("usage", "list", "--from", "2026-10-13T00:00:00Z", "--to", "2026-10-14T00:00:00Z").ok(t)
		if q := query(s); q.Get("to") != "2026-10-14T00:00:00Z" {
			t.Fatalf("%v", q)
		}
		n := len(s.Requests())
		e.run("usage", "list", "--from", "昨天").fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != n {
			t.Fatal("时间格式不对时不应发出请求")
		}
	})
	t.Run("翻看异常", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /anomalies", clitest.Sample{Status: 200, Body: anomalyPage("c2")}, clitest.Sample{Status: 200, Body: anomalyPage(nil)})
		first := e.run("anomaly", "list", "--limit", "20", "--from", "2026-10-12T00:00:00Z").ok(t)
		if q := query(s); q.Get("limit") != "20" || q.Get("from") != "2026-10-12T00:00:00Z" || q.Has("cursor") {
			t.Fatalf("%v", q)
		}
		second := e.run("anomaly", "list", "--limit", "20", "--cursor", first["nextCursor"].(string)).ok(t)
		if q := query(s); q.Get("cursor") != "c2" {
			t.Fatalf("%v", q)
		}
		if first["total"] != second["total"] || second["nextCursor"] != nil {
			t.Fatalf("%v %v", first, second)
		}
	})
	t.Run("健康状态", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /health", clitest.Sample{Status: 200, Body: health(true)}, clitest.Sample{Status: 503, Body: health(false)})
		if d := e.run("health").ok(t); d["ok"] != true {
			t.Fatalf("%v", d)
		}
		r := e.run("health")
		r.fails(t, output.CodeServerUnhealthy, 4)
		if r.Env.Error.HTTPStatus != 503 || !strings.Contains(r.Env.Error.Message, "postgres") || strings.Contains(r.Env.Error.Message, "redis") || r.Env.Meta.Attempts != 3 {
			t.Fatalf("%s", r.Stdout)
		}
		var details map[string]any
		json.Unmarshal(r.Env.Error.Details, &details)
		if details["postgres"].(map[string]any)["ok"] != false {
			t.Fatalf("%v", details)
		}
	})
}

// 契约之外的情况：网关在 /health 上返回统一错误结构的 503；Agent 身份不能查看健康状态。
func TestHealthEdgeCases(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(conn.HeaderVersion, testVersion)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"code":"internal_error","message":"网关"}}`)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	e := newEnv(t)
	e.vars[conn.EnvURL], e.vars[conn.EnvToken] = srv.URL, extToken
	e.vars[conn.EnvCAFile] = writeFile(t, "ca.pem", string(pemCert(srv)))
	e.run("health").fails(t, "internal_error", 4)
	newEnv(t).agent(srv.URL).run("health").fails(t, output.CodeCommandNotAllowed, 2)
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// 项目层：列出与创建 Agent 必须指定范围；项目可以写名称；用量与异常的范围可选（tasks 6.12、6.13）。
func TestProjectScope(t *testing.T) {
	last := func(s *clitest.Stub) clitest.Recorded {
		reqs := s.Requests()
		return reqs[len(reqs)-1]
	}
	t.Run("没有指定范围", func(t *testing.T) {
		s, e := stub(t)
		for _, args := range [][]string{
			{"agent", "list"},
			{"agent", "create", "--name", "dev", "--provider", "anthropic", "--model", "claude-x"},
		} {
			r := e.run(args...)
			r.fails(t, output.CodeMissingArgument, 2)
			if !strings.Contains(r.Env.Error.Message, "loopctl project list") {
				t.Fatalf("说明中应提示 project list：%s", r.Env.Error.Message)
			}
		}
		e.run("agent", "list", "--project", "默认项目", "--platform").fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != 0 {
			t.Fatal("没有指定范围时不应发出请求")
		}
	})
	t.Run("按项目名列出", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /projects", clitest.Sample{Status: 200, Body: map[string]any{"projects": []any{project()}}})
		s.On("GET /projects/{projectId}/agents", clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}})
		d := e.run("agent", "list", "--project", "默认项目").ok(t)
		if got := ops(s); !reflect.DeepEqual(got, []string{"GET /projects", "GET /projects/{projectId}/agents"}) {
			t.Fatalf("%v", got)
		}
		if last(s).Path != "/api/v1/projects/"+projectUUID+"/agents" {
			t.Fatal(last(s).Path)
		}
		item := d["agents"].([]any)[0].(map[string]any)
		if item["scope"] != "project" || item["projectId"] != projectUUID || item["isManager"] != false {
			t.Fatalf("新字段应原样透传：%v", item)
		}
		r := e.run("agent", "list", "--project", "不存在的项目")
		r.fails(t, output.CodeNotFound, 7)
		if r.Env.Error.HTTPStatus != 0 || len(s.Requests()) != 3 {
			t.Fatalf("项目名解析不到时不应再请求 Agent 列表：%s", r.Stdout)
		}
	})
	t.Run("在平台下创建", func(t *testing.T) {
		s, e := stub(t)
		s.On("POST /platform/agents", clitest.Sample{Status: 201, Body: agentDetail()})
		e.run("agent", "create", "--platform", "--name", "reviewer", "--provider", "anthropic", "--model", "claude-x").ok(t)
		if got := ops(s); !reflect.DeepEqual(got, []string{"POST /platform/agents"}) {
			t.Fatalf("%v", got)
		}
		if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"name": "reviewer", "model": map[string]any{"provider": "anthropic", "modelId": "claude-x"}}) {
			t.Fatalf("请求体中不应带范围：%v", b)
		}
	})
	t.Run("按名称查看项目", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /projects", clitest.Sample{Status: 200, Body: map[string]any{"projects": []any{project()}}})
		s.On("GET /projects/{projectId}", clitest.Sample{Status: 200, Body: project()})
		if d := e.run("project", "get", "默认项目").ok(t); d["isDefault"] != true || d["managerAgentId"] != nil {
			t.Fatalf("%v", d)
		}
		e.run("project", "get", "不存在的项目").fails(t, output.CodeNotFound, 7)
	})
	t.Run("用量与异常的范围", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /projects", clitest.Sample{Status: 200, Body: map[string]any{"projects": []any{project()}}})
		s.On("GET /usage", clitest.Sample{Status: 200, Body: usagePage()})
		s.On("GET /anomalies", clitest.Sample{Status: 200, Body: anomalyPage(nil)})
		e.run("usage", "list", "--platform").ok(t)
		if q := last(s).RawQuery; q != "platform=true" {
			t.Fatal(q)
		}
		e.run("anomaly", "list", "--project", "默认项目").ok(t)
		if q := last(s).RawQuery; q != "projectId="+projectUUID {
			t.Fatal(q)
		}
		e.run("usage", "list").ok(t)
		if q := last(s).RawQuery; q != "" {
			t.Fatalf("不指定范围时为全部：%s", q)
		}
		n := len(s.Requests())
		e.run("usage", "list", "--project", projectUUID, "--platform").fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != n {
			t.Fatal("同时指定两种范围时不应发出请求")
		}
	})
	t.Run("项目 ID 不存在", func(t *testing.T) {
		s, e := stub(t)
		s.On("GET /usage", clitest.Sample{Status: 404, Body: apiError("not_found", "项目不存在")})
		s.On("GET /anomalies", clitest.Sample{Status: 404, Body: apiError("not_found", "项目不存在")})
		for _, cmd := range []string{"usage", "anomaly"} {
			r := e.run(cmd, "list", "--project", "0192f1a2-0000-7000-8000-0000000000ff")
			r.fails(t, "not_found", 7)
			if r.Env.Error.HTTPStatus != 404 {
				t.Fatalf("%s", r.Stdout)
			}
		}
	})
}
