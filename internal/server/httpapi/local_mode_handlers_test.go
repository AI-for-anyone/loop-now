package httpapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/localmode"
	"loop-now/internal/server/store"
)

// 本机模式的接口：开关按字段修改；项目与 Agent 的清单整体替换、带 revision、读回带 status；所有响应经契约校验。
func TestLocalModeEndpoints(t *testing.T) {
	e := newEnv(t)
	settings := decode[store.LocalModeSettings](t, e.expect(200, "GET", "/local-mode-settings", nil))
	if !settings.Directories || !settings.Git || !settings.CLI {
		t.Fatalf("默认应全开：%+v", settings)
	}
	settings = decode[store.LocalModeSettings](t, e.expect(200, "PATCH", "/local-mode-settings", map[string]any{"git": false}))
	if !settings.Directories || settings.Git || !settings.CLI {
		t.Fatalf("只改 git：%+v", settings)
	}
	e.expect(422, "PATCH", "/local-mode-settings", map[string]any{}, "X-Contract-Invalid-Request", "1")
	e.expect(422, "PATCH", "/local-mode-settings", map[string]any{"git": true, "other": 1}, "X-Contract-Invalid-Request", "1")

	root := t.TempDir()
	code := filepath.Join(root, "code")
	if err := os.MkdirAll(code, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectPath := "/projects/" + e.dc.Project.ID.String() + "/local-directories"
	got := decode[localmode.ProjectDirectories](t, e.expect(200, "GET", projectPath, nil))
	if got.Revision != 0 || len(got.Directories) != 0 {
		t.Fatalf("从未保存：%+v", got)
	}
	got = decode[localmode.ProjectDirectories](t, e.expect(200, "PUT", projectPath, map[string]any{"expectedRevision": 0, "directories": []map[string]any{
		{"path": code, "note": "仓库"}, {"path": filepath.Join(root, "missing"), "note": nil}, {"path": file},
	}}))
	if got.Revision != 1 || got.Directories[0].Status != "ok" || got.Directories[1].Status != "missing" || got.Directories[2].Status != "not_directory" || got.Directories[1].Note != nil {
		t.Fatalf("保存后：%+v", got)
	}
	stale := decode[apitypes.ErrorBody](t, e.expect(412, "PUT", projectPath, map[string]any{"expectedRevision": 0, "directories": []any{}}))
	if stale.Error.Code != "precondition_failed" {
		t.Fatalf("旧 revision：%+v", stale)
	}
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "PUT", projectPath, map[string]any{"expectedRevision": 1, "directories": []map[string]any{
		{"path": code}, {"path": "relative"}, {"path": code},
	}}))
	if bad.Error.Code != "validation_failed" || len(bad.Error.Fields) != 2 || bad.Error.Fields[0].Field != "directories[1].path" || bad.Error.Fields[1].Field != "directories[2].path" {
		t.Fatalf("字段错误：%+v", bad)
	}
	e.expect(404, "GET", "/projects/"+uuid.Must(uuid.NewV7()).String()+"/local-directories", nil)

	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "本机", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	agentPath := "/agents/" + agent.ID.String() + "/local-directories"
	own := decode[localmode.AgentDirectories](t, e.expect(200, "GET", agentPath, nil))
	if own.Revision != 0 || len(own.Directories) != 0 || own.ProjectID == nil || *own.ProjectID != e.dc.Project.ID || len(own.Inherited) != 3 || own.Inherited[0].Path != code {
		t.Fatalf("Agent 读回：%+v", own)
	}
	own = decode[localmode.AgentDirectories](t, e.expect(200, "PUT", agentPath, map[string]any{"expectedRevision": 0, "directories": []map[string]any{{"path": root, "note": "  上级  "}}}))
	if own.Revision != 1 || len(own.Directories) != 1 || *own.Directories[0].Note != "上级" || own.Directories[0].Status != "ok" || len(own.Inherited) != 3 {
		t.Fatalf("Agent 保存后：%+v", own)
	}
	e.expect(412, "PUT", agentPath, map[string]any{"expectedRevision": 7, "directories": []any{}})
	e.expect(404, "PUT", "/agents/"+uuid.Must(uuid.NewV7()).String()+"/local-directories", map[string]any{"expectedRevision": 0, "directories": []any{}})

	// 平台 Agent：projectId 为 null、没有继承。
	platform, err := e.st.CreateAgent(t.Context(), store.PlatformScope(), "平台本机", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	p := decode[localmode.AgentDirectories](t, e.expect(200, "GET", "/agents/"+platform.ID.String()+"/local-directories", nil))
	if p.ProjectID != nil || len(p.Inherited) != 0 {
		t.Fatalf("平台 Agent：%+v", p)
	}
}
