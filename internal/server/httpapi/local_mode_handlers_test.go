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

// Local mode endpoints: the switches are changed per field; the project and Agent lists are replaced as a whole, carry
// a revision, and read back with status; all responses are validated against the contract.
func TestLocalModeEndpoints(t *testing.T) {
	e := newEnv(t)
	settings := decode[store.LocalModeSettings](t, e.expect(200, "GET", "/local-mode-settings", nil))
	if !settings.Directories || !settings.Git || !settings.CLI || settings.Logins {
		t.Fatalf("directories, git and CLI are on by default, logins off: %+v", settings)
	}
	if settings = decode[store.LocalModeSettings](t, e.expect(200, "PATCH", "/local-mode-settings", map[string]any{"logins": true})); !settings.Logins || !settings.Git {
		t.Fatalf("turn on logins only: %+v", settings)
	}
	settings = decode[store.LocalModeSettings](t, e.expect(200, "PATCH", "/local-mode-settings", map[string]any{"git": false}))
	if !settings.Directories || settings.Git || !settings.CLI {
		t.Fatalf("change git only: %+v", settings)
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
		t.Fatalf("never saved: %+v", got)
	}
	got = decode[localmode.ProjectDirectories](t, e.expect(200, "PUT", projectPath, map[string]any{"expectedRevision": 0, "directories": []map[string]any{
		{"path": code, "note": "repository"}, {"path": filepath.Join(root, "missing"), "note": nil}, {"path": file},
	}}))
	if got.Revision != 1 || got.Directories[0].Status != "ok" || got.Directories[1].Status != "missing" || got.Directories[2].Status != "not_directory" || got.Directories[1].Note != nil {
		t.Fatalf("after saving: %+v", got)
	}
	stale := decode[apitypes.ErrorBody](t, e.expect(412, "PUT", projectPath, map[string]any{"expectedRevision": 0, "directories": []any{}}))
	if stale.Error.Code != "precondition_failed" {
		t.Fatalf("stale revision: %+v", stale)
	}
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "PUT", projectPath, map[string]any{"expectedRevision": 1, "directories": []map[string]any{
		{"path": code}, {"path": "relative"}, {"path": code},
	}}))
	if bad.Error.Code != "validation_failed" || len(bad.Error.Fields) != 2 || bad.Error.Fields[0].Field != "directories[1].path" || bad.Error.Fields[1].Field != "directories[2].path" {
		t.Fatalf("field errors: %+v", bad)
	}
	e.expect(404, "GET", "/projects/"+uuid.Must(uuid.NewV7()).String()+"/local-directories", nil)

	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Local", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	agentPath := "/agents/" + agent.ID.String() + "/local-directories"
	own := decode[localmode.AgentDirectories](t, e.expect(200, "GET", agentPath, nil))
	if own.Revision != 0 || len(own.Directories) != 0 || own.ProjectID == nil || *own.ProjectID != e.dc.Project.ID || len(own.Inherited) != 3 || own.Inherited[0].Path != code {
		t.Fatalf("Agent reads back: %+v", own)
	}
	own = decode[localmode.AgentDirectories](t, e.expect(200, "PUT", agentPath, map[string]any{"expectedRevision": 0, "directories": []map[string]any{{"path": root, "note": "  parent  "}}}))
	if own.Revision != 1 || len(own.Directories) != 1 || *own.Directories[0].Note != "parent" || own.Directories[0].Status != "ok" || len(own.Inherited) != 3 {
		t.Fatalf("after the Agent saves: %+v", own)
	}
	e.expect(412, "PUT", agentPath, map[string]any{"expectedRevision": 7, "directories": []any{}})
	e.expect(404, "PUT", "/agents/"+uuid.Must(uuid.NewV7()).String()+"/local-directories", map[string]any{"expectedRevision": 0, "directories": []any{}})

	// Platform Agent: projectId is null, nothing is inherited.
	platform, err := e.st.CreateAgent(t.Context(), store.PlatformScope(), "Platform local", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	p := decode[localmode.AgentDirectories](t, e.expect(200, "GET", "/agents/"+platform.ID.String()+"/local-directories", nil))
	if p.ProjectID != nil || len(p.Inherited) != 0 {
		t.Fatalf("platform Agent: %+v", p)
	}
}
