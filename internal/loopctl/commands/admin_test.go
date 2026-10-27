package commands_test

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

func stub(t *testing.T) (*clitest.Stub, *env) {
	s := clitest.NewExternal(t)
	return s, newEnv(t).external(s)
}

// 每个只读管理命令发出对应的 GET 请求，data 原样为服务端响应体。
func TestReadCommands(t *testing.T) {
	cases := []struct {
		args   []string
		op     string
		sample any
		query  string
	}{
		{[]string{"model", "providers"}, "GET /model-providers", modelProviderList(), ""},
		{[]string{"model", "list", "anthropic"}, "GET /model-providers/{providerId}/models", modelList(), ""},
		{[]string{"cli-template", "list"}, "GET /cli-templates", map[string]any{"templates": []any{cliTemplate()}}, ""},
		{[]string{"project", "list"}, "GET /projects", map[string]any{"projects": []any{project()}}, ""},
		{[]string{"project", "get", projectUUID}, "GET /projects/{projectId}", project(), ""},
		{[]string{"agent", "list", "--project", projectUUID}, "GET /projects/{projectId}/agents", map[string]any{"agents": []any{agentListItem()}}, ""},
		{[]string{"agent", "list", "--platform"}, "GET /platform/agents", map[string]any{"agents": []any{}}, ""},
		{[]string{"agent", "get", agentUUID}, "GET /agents/{agentId}", agentDetail(), ""},
		{[]string{"agent", "status", agentUUID}, "GET /agents/{agentId}/status", agentStatus(), ""},
		{[]string{"agent", "history", agentUUID, "--limit", "20"}, "GET /agents/{agentId}/messages", messagePage(), "limit=20"},
		{[]string{"agent", "history", agentUUID, "--before", "0192f1a2-0000-7000-8000-0000000000f1"}, "GET /agents/{agentId}/messages", messagePage(), "before=0192f1a2-0000-7000-8000-0000000000f1"},
		{[]string{"agent", "skill", "list", agentUUID}, "GET /agents/{agentId}/skills", map[string]any{"skills": []any{agentSkillSummary()}}, ""},
		{[]string{"agent", "skill", "get", agentUUID, "0192f1a2-0000-7000-8000-0000000000d1"}, "GET /agents/{agentId}/skills/{agentSkillId}", agentSkill(), ""},
		{[]string{"skill", "list"}, "GET /skills", map[string]any{"skills": []any{skillTemplateSummary()}}, ""},
		{[]string{"skill", "get", "0192f1a2-0000-7000-8000-0000000000c1"}, "GET /skills/{skillId}", skillTemplate(), ""},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			s, e := stub(t)
			s.On(c.op, clitest.Sample{Status: 200, Body: c.sample})
			r := e.run(c.args...)
			r.ok(t)
			reqs := s.Requests()
			if len(reqs) != 1 || reqs[0].Op != c.op || reqs[0].RawQuery != c.query {
				t.Fatalf("请求 %+v", reqs)
			}
			var want, got any
			b, _ := jsonMarshal(c.sample)
			jsonUnmarshal(b, &want)
			jsonUnmarshal(r.Env.Data, &got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("data 应为服务端响应体\n得到 %s", r.Env.Data)
			}
			if r.Env.Meta.Identity != "external" || r.Env.Meta.ServerVersion != testVersion || r.Env.Meta.RequestID == "" || r.Env.Meta.Attempts != 1 {
				t.Fatalf("meta %+v", r.Env.Meta)
			}
			if len(r.Env.Warnings) != 0 {
				t.Fatalf("版本一致时不应有警告：%v", r.Env.Warnings)
			}
		})
	}
}

func TestAgentCreateAndUpdate(t *testing.T) {
	s, e := stub(t)
	s.On("POST /projects/{projectId}/agents", clitest.Sample{Status: 201, Body: agentDetail()})
	s.On("PATCH /agents/{agentId}", clitest.Sample{Status: 200, Body: agentDetail()})
	e.stdin = "你是开发助手"
	e.run("agent", "create", "--project", projectUUID, "--name", "dev", "--provider", "anthropic", "--model", "claude-x", "--prompt-file", "-").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"name": "dev", "model": map[string]any{"provider": "anthropic", "modelId": "claude-x"}, "prompt": "你是开发助手"}) {
		t.Fatalf("创建请求体 %v", b)
	}
	e.run("agent", "update", agentUUID, "--name", "dev2").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"name": "dev2"}) {
		t.Fatalf("只改名称时请求体 %v", b)
	}
	e.run("agent", "update", agentUUID, "--provider", "anthropic", "--model", "claude-y", "--prompt-file", writeFile(t, "p.txt", "新 Prompt"), "--expected-prompt-version", "1").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"model": map[string]any{"provider": "anthropic", "modelId": "claude-y"}, "prompt": "新 Prompt", "expectedPromptVersion": float64(1)}) {
		t.Fatalf("改模型时请求体 %v", b)
	}
	e.run("agent", "update", agentUUID, "--provider", "anthropic", "--model", "claude-z").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"model": map[string]any{"provider": "anthropic", "modelId": "claude-z"}}) {
		t.Fatalf("仅改模型不应增加 Prompt 条件：%v", b)
	}
	e.run("agent", "update", agentUUID, "--provider", "anthropic").fails(t, output.CodeInvalidArgument, 2)
	e.run("agent", "update", agentUUID).fails(t, output.CodeMissingArgument, 2)
}

func TestAgentDeleteAndSessionReset(t *testing.T) {
	s, e := stub(t)
	s.On("DELETE /agents/{agentId}", clitest.Sample{Status: 204}, clitest.Sample{Status: 409, Body: apiError("agent_not_idle", "Agent 正忙")})
	r := e.run("agent", "delete", agentUUID, "--yes")
	r.ok(t)
	if string(r.Env.Data) != "null" {
		t.Fatalf("删除成功时 data 应为 null：%s", r.Env.Data)
	}
	r = e.run("agent", "delete", agentUUID, "--yes")
	r.fails(t, "agent_not_idle", 8)
	if r.Env.Error.HTTPStatus != 409 || r.Env.Error.Category != output.CategoryConflict {
		t.Fatalf("%+v", r.Env.Error)
	}

	s.On("POST /agents/{agentId}/session/reset", clitest.Sample{Status: 200, Body: map[string]any{"state": "active", "epoch": 2}},
		clitest.Sample{Status: 409, Body: apiError("agent_not_idle", "Agent 正忙")})
	if d := e.run("agent", "session", "reset", agentUUID, "--yes").ok(t); d["epoch"] != float64(2) {
		t.Fatalf("%v", d)
	}
	e.run("agent", "session", "reset", agentUUID, "--yes").fails(t, "agent_not_idle", 8)
}

func TestCliAndCredentials(t *testing.T) {
	s, e := stub(t)
	s.On("PUT /agents/{agentId}/clis/{cli}", clitest.Sample{Status: 200, Body: agentCli()})
	s.On("DELETE /agents/{agentId}/clis/{cli}", clitest.Sample{Status: 204})
	s.On("PUT /agents/{agentId}/clis/{cli}/credential", clitest.Sample{Status: 200, Body: credentialMeta()})
	s.On("DELETE /agents/{agentId}/clis/{cli}/credential", clitest.Sample{Status: 204})
	s.On("PUT /agents/{agentId}/model-credential", clitest.Sample{Status: 200, Body: credentialMeta()})
	s.On("DELETE /agents/{agentId}/model-credential", clitest.Sample{Status: 204})

	e.run("agent", "cli", "add", agentUUID, "gh").ok(t)
	e.run("agent", "cli", "remove", agentUUID, "gh", "--yes").ok(t)

	const ghSecret = "ghp_CREDENTIALvalue123"
	e.stdin = `{"token":"` + ghSecret + `"}`
	r := e.run("agent", "credential", "set-cli", agentUUID, "gh", "--from-file", "-")
	if d := r.ok(t); d["status"] != "set" {
		t.Fatalf("%v", d)
	}
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"fields": map[string]any{"token": ghSecret}}) {
		t.Fatalf("%v", b)
	}
	if strings.Contains(r.Stdout, ghSecret) {
		t.Fatal("输出中出现了凭证值")
	}
	e.run("agent", "credential", "set-model", agentUUID, "--from-file", writeFile(t, "model.json", `{"provider":"anthropic","key":"sk-MODEL"}`)).ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"provider": "anthropic", "key": "sk-MODEL"}) {
		t.Fatalf("%v", b)
	}
	e.run("agent", "credential", "clear-cli", agentUUID, "gh", "--yes").ok(t)
	e.run("agent", "credential", "clear-model", agentUUID, "--yes").ok(t)

	n := len(s.Requests())
	e.stdin = `{"token": SECRET-NOT-JSON`
	r = e.run("agent", "credential", "set-cli", agentUUID, "gh", "--from-file", "-")
	r.fails(t, output.CodeInvalidArgument, 2)
	if strings.Contains(r.Stdout, "SECRET") || len(s.Requests()) != n {
		t.Fatalf("格式错误时不应带出内容，也不应发出请求：%s", r.Stdout)
	}
}

func TestAgentSkills(t *testing.T) {
	s, e := stub(t)
	sk := "0192f1a2-0000-7000-8000-0000000000d1"
	s.On("POST /agents/{agentId}/skills", clitest.Sample{Status: 201, Body: agentSkill()})
	s.On("PUT /agents/{agentId}/skills/{agentSkillId}/skill-md", clitest.Sample{Status: 200, Body: agentSkill()})
	s.On("POST /agents/{agentId}/skills/{agentSkillId}/recopy", clitest.Sample{Status: 200, Body: agentSkill()})
	s.On("DELETE /agents/{agentId}/skills/{agentSkillId}", clitest.Sample{Status: 204})

	e.run("agent", "skill", "add", agentUUID, "--template", "0192f1a2-0000-7000-8000-0000000000c1").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"templateId": "0192f1a2-0000-7000-8000-0000000000c1"}) {
		t.Fatalf("%v", b)
	}
	e.stdin = "---\nname: review\ndescription: d\n---\n正文\n"
	e.run("agent", "skill", "edit", agentUUID, sk, "--skill-md", "-").ok(t)
	if b := lastBody(t, s); b["content"] != e.stdin {
		t.Fatalf("%v", b)
	}
	e.run("agent", "skill", "recopy", agentUUID, sk, "--yes").ok(t)
	e.run("agent", "skill", "remove", agentUUID, sk, "--yes").ok(t)
}

func TestSkillLibrary(t *testing.T) {
	id := "0192f1a2-0000-7000-8000-0000000000c1"
	t.Run("从目录创建", func(t *testing.T) {
		s, e := stub(t)
		s.On("POST /skills", clitest.Sample{Status: 201, Body: skillTemplate()})
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644)
		os.MkdirAll(filepath.Join(dir, "scripts", "lib"), 0o755)
		os.WriteFile(filepath.Join(dir, "scripts", "lib", "run.sh"), []byte("echo hi"), 0o755)
		e.run("skill", "create", "--dir", dir).ok(t)
		files := archiveFiles(t, s.Requests()[0])
		if !reflect.DeepEqual(files, map[string]string{"SKILL.md": "---\nname: review\n---\n", "scripts/lib/run.sh": "echo hi"}) {
			t.Fatalf("压缩包内容 %v", files)
		}
	})
	t.Run("目录中有符号链接", func(t *testing.T) {
		s, e := stub(t)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x"), 0o644)
		os.Symlink("/etc/passwd", filepath.Join(dir, "link"))
		e.run("skill", "create", "--dir", dir).fails(t, output.CodeSymlinkNotSupported, 2)
		if len(s.Requests()) != 0 {
			t.Fatal("不应发出请求")
		}
	})
	t.Run("从 zip 与 SKILL.md 创建", func(t *testing.T) {
		s, e := stub(t)
		s.On("POST /skills", clitest.Sample{Status: 201, Body: skillTemplate()})
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("SKILL.md")
		w.Write([]byte("z"))
		zw.Close()
		e.run("skill", "create", "--zip", writeFile(t, "s.zip", buf.String())).ok(t)
		if files := archiveFiles(t, s.Requests()[0]); files["SKILL.md"] != "z" {
			t.Fatalf("%v", files)
		}
		e.stdin = "---\nname: review\n---\n"
		e.run("skill", "create", "--skill-md", "-").ok(t)
		if b := lastBody(t, s); b["skillMd"] != e.stdin {
			t.Fatalf("%v", b)
		}
		e.run("skill", "create").fails(t, output.CodeMissingArgument, 2)
	})
	t.Run("替换、编辑与删除", func(t *testing.T) {
		s, e := stub(t)
		s.On("PUT /skills/{skillId}", clitest.Sample{Status: 200, Body: skillTemplate()})
		s.On("PUT /skills/{skillId}/skill-md", clitest.Sample{Status: 200, Body: skillTemplate()})
		s.On("DELETE /skills/{skillId}", clitest.Sample{Status: 204})
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("new"), 0o644)
		e.run("skill", "replace", id, "--dir", dir, "--yes").ok(t)
		e.run("skill", "edit", id, "--skill-md", writeFile(t, "SKILL.md", "edited")).ok(t)
		if b := lastBody(t, s); b["content"] != "edited" {
			t.Fatalf("%v", b)
		}
		e.run("skill", "delete", id, "--yes").ok(t)
		want := []string{"PUT /skills/{skillId}", "PUT /skills/{skillId}/skill-md", "DELETE /skills/{skillId}"}
		if got := ops(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("%v", got)
		}
	})
}

// archiveFiles 从 multipart 请求中取出 zip 并列出其中的文件。
func archiveFiles(t *testing.T, req clitest.Recorded) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(bytes.NewReader(req.Body), params["boundary"])
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "archive" {
		t.Fatalf("没有 archive 字段：%v", err)
	}
	data, _ := io.ReadAll(part)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	var names []string
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
		names = append(names, f.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("条目没有按路径排序：%v", names)
	}
	return out
}
