package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/taskmodel"
)

// The Manager creates a task with a fork through the Agent API, and the console reads it, both within the contract; a
// workflow that puts one Agent in two branches is refused with a field error.
func TestTaskHTTPForkCreate(t *testing.T) {
	e, manager, d := taskEnv(t)
	lead := d.Nodes[0].AgentID
	agents := []uuid.UUID{}
	for _, name := range []string{"Server", "Web"} {
		a := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": name, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
		agents = append(agents, a.ID)
	}
	step := func(id string, agent uuid.UUID) map[string]any {
		return map[string]any{"id": id, "title": id, "description": "Do " + id, "agentId": agent, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}
	}
	create := func(server, web uuid.UUID) map[string]any {
		return map[string]any{"actionId": uuid.New(), "title": "Forked", "request": map[string]any{}, "workflow": map[string]any{
			"startStepId": "plan", "maxParallelBranches": 2,
			"steps": []any{step("plan", lead), step("server", server), step("web", web), step("verify", lead)},
			"edges": []any{
				map[string]any{"id": "to_server", "from": "plan", "to": "server", "label": "Server", "fork": "build"},
				map[string]any{"id": "to_web", "from": "plan", "to": "web", "label": "Web", "fork": "build"},
				map[string]any{"id": "server_done", "from": "server", "to": "verify", "label": "Done"},
				map[string]any{"id": "web_done", "from": "web", "to": "verify", "label": "Done"},
			},
			"forks": []any{map[string]any{"id": "build", "join": "verify"}},
		}}
	}
	created := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", create(agents[0], agents[1])))
	if len(created.Workflow.Forks) != 1 || created.Workflow.Forks[0].Join != "verify" || created.Workflow.Edges[0].Fork != "build" || *created.Workflow.MaxParallelBranches != 2 {
		t.Fatalf("the fork is not kept with the workflow: %+v", created.Workflow)
	}
	if len(created.Task.CurrentNodes) != 1 || created.Task.CurrentNodes[0].StepID != "plan" || created.Task.CurrentNodes[0].Branch != nil {
		t.Fatalf("current nodes %+v", created.Task.CurrentNodes)
	}
	raw := e.console().expect(200, "GET", "/tasks/"+created.Task.ID.String(), nil)
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if nodes := view["task"].(map[string]any)["currentNodes"].([]any); len(nodes) != 1 {
		t.Fatalf("console view current nodes: %v", nodes)
	}
	// The same Agent in both branches.
	out := manager.expect(422, "POST", "/tasks", create(agents[0], agents[0]))
	var body apitypes.ErrorBody
	if err := json.Unmarshal(out, &body); err != nil || body.Error.Code != "validation_failed" || len(body.Error.Fields) != 1 || body.Error.Fields[0].Field != "workflow.steps.agentId" {
		t.Fatalf("refusal: %s", out)
	}
}
