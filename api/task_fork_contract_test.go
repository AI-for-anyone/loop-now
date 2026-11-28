package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

// A task with a fork, as the server writes it (several current nodes, branch workspaces, the fork in the workflow),
// matches TaskDetail in both contracts; the create input with a fork matches TaskCreateInput.
func TestTaskForkContract(t *testing.T) {
	limit := 1
	lead, server, web := uuid.New(), uuid.New(), uuid.New()
	step := func(id string, agent uuid.UUID) workflow.Step {
		return workflow.Step{ID: id, Title: id, AgentID: agent, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}
	}
	wf := workflow.Definition{StartStepID: "plan", Steps: []workflow.Step{step("plan", lead), step("server", server), step("web", web), step("verify", lead)},
		Edges: []workflow.Edge{{ID: "to_server", From: "plan", To: "server", Label: "Server", Fork: "build"}, {ID: "to_web", From: "plan", To: "web", Label: "Web", Fork: "build"},
			{ID: "server_done", From: "server", To: "verify", Label: "Done"}, {ID: "web_done", From: "web", To: "verify", Label: "Done"}},
		Forks: []workflow.Fork{{ID: "build", Join: "verify"}}, MaxParallelBranches: &limit}
	if err := wf.Validate(workflow.Limits{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	branch := func(s string) *string { return &s }
	node := func(stepID string, agent uuid.UUID, b *string) taskmodel.NodeRun {
		return taskmodel.NodeRun{ID: uuid.New(), TaskID: uuid.New(), StepID: stepID, Visit: 1, Attempt: 1, AgentID: agent, AgentName: stepID, RuntimeID: uuid.New(), Status: "running",
			Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}, Branch: b}
	}
	r := taskmodel.Record{View: taskmodel.Detail{SleepingInstances: []sleepmodel.Record{}, Task: taskmodel.Summary{ID: uuid.New(), ProjectID: uuid.New(), Number: 1, Title: "t", Status: "running",
		CreatedAt: now, UpdatedAt: now, Link: "/tasks/t", Version: 3}, Request: map[string]any{}, Workflow: wf, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}}
	s, w := node("server", server, branch("server")), node("web", web, branch("web"))
	r.View.Nodes = []taskmodel.NodeRun{s, w}
	r.AddCurrent(s)
	r.AddCurrent(w)
	merge, commit := taskmodel.BranchConflict, "abc"
	r.View.Workspace = &taskmodel.Workspace{Mode: taskmodel.WorkspaceModeGit, Folder: "/code/app", Path: "/w/t/app", State: taskmodel.WorkspaceStateActive,
		Git: &taskmodel.WorkspaceGit{Repository: "/code/app", Branch: "task/1-t", BaseBranch: "main", BaseCommit: &commit},
		Branches: []taskmodel.WorkspaceBranch{{Step: "server", Path: "/w/t/app--server", State: taskmodel.WorkspaceStateActive, Merge: &merge, Git: &taskmodel.WorkspaceBranchGit{Branch: "task/1-t--server", BaseCommit: commit}},
			{Step: "web", Path: "/w/t/app--web", State: taskmodel.WorkspaceStateRemoved, Git: &taskmodel.WorkspaceBranchGit{Branch: "task/1-t--web", BaseCommit: commit}}}}
	detail := roundTrip(t, r.View)
	create := roundTrip(t, taskmodel.CreateInput{ActionID: uuid.New(), Title: "t", Request: map[string]any{}, Workflow: wf})
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		doc := contracttest.LoadFile(t, file)
		for name, v := range map[string]any{"TaskDetail": detail, "TaskCreateInput": create} {
			if err := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatalf("%s %s: %v", file, name, err)
			}
		}
		submit := map[string]any{"actionId": uuid.NewString(), "output": map[string]any{}, "edgeId": "build"}
		if err := doc.Components.Schemas["TaskSubmitInput"].Value.VisitJSON(submit, openapi3.EnableJSONSchema2020()); err != nil {
			t.Fatalf("%s TaskSubmitInput with a fork id: %v", file, err)
		}
	}
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
