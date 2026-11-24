package httpapi_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
)

type acpCommand struct {
	ID       uuid.UUID `json:"id"`
	Runtime  string    `json:"runtime"`
	Name     string    `json:"name"`
	Command  string    `json:"command"`
	Args     []string  `json:"args"`
	EnvNames []string  `json:"envNames"`
}

type runtimeDefinition struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Providers    []string   `json:"providers"`
	AcpCommandID *uuid.UUID `json:"acpCommandId"`
}

// A local ACP command is added, changed and removed through the console; while it exists it is a runtime with one
// model provider of its id (the program's default model), and Agents can use it. Its environment values are never
// returned; a change merges into them. It cannot be removed while an Agent runs on it.
func TestACPCommands(t *testing.T) {
	e := newEnv(t)
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	created := decode[acpCommand](t, e.expect(201, "POST", "/acp-commands", map[string]any{"name": " My Agent ", "command": "my-agent",
		"args": []string{"--profile", "acp"}, "env": map[string]string{"MY_TOKEN": "secret", "MODE": "x"}}))
	if created.Name != "My Agent" || created.Runtime != "acp-"+created.ID.String() || !slices.Equal(created.Args, []string{"--profile", "acp"}) ||
		!slices.Equal(created.EnvNames, []string{"MODE", "MY_TOKEN"}) {
		t.Fatalf("created: %+v", created)
	}
	body := e.expect(200, "GET", "/acp-commands", nil)
	if strings.Contains(string(body), "secret") {
		t.Fatalf("environment values are never returned: %s", body)
	}
	defs := decode[struct {
		Runtimes []runtimeDefinition `json:"runtimes"`
	}](t, e.expect(200, "GET", "/runtime-definitions", nil))
	last := defs.Runtimes[len(defs.Runtimes)-1]
	if last.ID != created.Runtime || last.Name != "My Agent" || last.AcpCommandID == nil || *last.AcpCommandID != created.ID ||
		!slices.Equal(last.Providers, []string{created.Runtime}) || defs.Runtimes[0].AcpCommandID != nil {
		t.Fatalf("its runtime: %+v", last)
	}
	models := decode[apitypes.ModelList](t, e.expect(200, "GET", "/model-providers/"+created.Runtime+"/models", nil))
	if len(models.Models) != 1 || models.Models[0].ID != "default" || !slices.Equal(models.Models[0].ThinkingLevels, []string{"off"}) {
		t.Fatalf("its provider: %+v", models)
	}

	changed := decode[acpCommand](t, e.expect(200, "PATCH", "/acp-commands/"+created.ID.String(), map[string]any{"name": "Renamed",
		"env": map[string]any{"MODE": nil, "EXTRA": "1"}}))
	if changed.Name != "Renamed" || changed.Command != "my-agent" || !slices.Equal(changed.EnvNames, []string{"EXTRA", "MY_TOKEN"}) {
		t.Fatalf("changed: %+v", changed)
	}
	if rt, ok := agentruntime.ByID(created.Runtime); !ok || rt.Name != "Renamed" || rt.Command.Env["MY_TOKEN"] != "secret" || rt.Command.Env["EXTRA"] != "1" {
		t.Fatalf("the runtime follows: %+v", rt)
	}

	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "uses it", "model": map[string]string{"provider": created.Runtime, "modelId": "default"}}))
	if agent.Runtime != created.Runtime {
		t.Fatalf("an Agent runs on it: %+v", agent)
	}
	var eb struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Agents []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"agents"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(e.expect(409, "DELETE", "/acp-commands/"+created.ID.String(), nil), &eb); err != nil || eb.Error.Code != "runtime_in_use" ||
		len(eb.Error.Details.Agents) != 1 || eb.Error.Details.Agents[0].ID != agent.ID.String() || eb.Error.Details.Agents[0].Name != "uses it" {
		t.Fatalf("in use: %+v %v", eb, err)
	}
	e.expect(200, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	e.expect(204, "DELETE", "/acp-commands/"+created.ID.String(), nil)
	if _, ok := agentruntime.ByID(created.Runtime); ok {
		t.Fatal("its runtime is gone")
	}
	e.expect(404, "GET", "/model-providers/"+created.Runtime+"/models", nil)
	e.expect(404, "DELETE", "/acp-commands/"+created.ID.String(), nil)
}

// What a command may be: a program name or an absolute path, and environment names the platform does not set itself.
func TestACPCommandValidation(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	for _, body := range []map[string]any{
		{"name": " ", "command": "x"},
		{"name": "a", "command": "bin/agent"},
		{"name": "a", "command": "x", "env": map[string]string{"1BAD": "v"}},
		{"name": "a", "command": "x", "env": map[string]string{"HOME": "/tmp"}},
	} {
		e.expectError(422, "validation_failed", "POST", "/acp-commands", body)
	}
	c := decode[acpCommand](t, e.expect(201, "POST", "/acp-commands", map[string]any{"name": "a", "command": "/usr/local/bin/agent"}))
	e.expect(422, "PATCH", "/acp-commands/"+c.ID.String(), map[string]any{}, "X-Contract-Invalid-Request", "1")
	e.expectError(422, "validation_failed", "PATCH", "/acp-commands/"+c.ID.String(), map[string]any{"env": map[string]any{"PATH": "/x"}})
}
