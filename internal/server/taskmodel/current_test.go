package taskmodel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/workflow"
)

// The single current fields name the current node exactly when there is one; the list always has every current node.
func TestCurrentNodesAndTheSingleFields(t *testing.T) {
	r := Record{View: Detail{Workflow: workflow.Definition{Steps: []workflow.Step{{ID: "plan", Title: "Plan"}, {ID: "server", Title: "Server"}, {ID: "web", Title: "Web"}}}}}
	branch := func(s string) *string { return &s }
	plan := NodeRun{ID: uuid.New(), StepID: "plan", AgentID: uuid.New(), RuntimeID: uuid.New()}
	server := NodeRun{ID: uuid.New(), StepID: "server", AgentID: uuid.New(), RuntimeID: uuid.New(), Branch: branch("server")}
	web := NodeRun{ID: uuid.New(), StepID: "web", AgentID: uuid.New(), RuntimeID: uuid.New(), Branch: branch("web")}
	r.View.Nodes = []NodeRun{plan, server, web}
	r.SetCurrent(plan)
	if r.Current() == nil || r.Current().ID != plan.ID || len(r.Active()) != 1 || *r.View.Task.CurrentStepTitle != "Plan" {
		t.Fatalf("one current node: %+v", r.View.Task)
	}
	// The fork: plan leaves, two branches come.
	r.RemoveCurrent(plan.ID)
	if r.Current() != nil || len(r.Active()) != 0 || r.View.Task.CurrentStepID != nil {
		t.Fatalf("no current node: %+v", r.View.Task)
	}
	r.AddCurrent(server)
	r.AddCurrent(web)
	task := r.View.Task
	if r.Current() != nil || task.CurrentNodeRunID != nil || task.CurrentStepID != nil || task.CurrentStepTitle != nil || task.CurrentAgentID != nil {
		t.Fatalf("several current nodes still fill the single fields: %+v", task)
	}
	if len(task.CurrentNodes) != 2 || task.CurrentNodes[1].StepTitle != "Web" || *task.CurrentNodes[1].Branch != "web" {
		t.Fatalf("current nodes: %+v", task.CurrentNodes)
	}
	if r.ActiveFor(web.RuntimeID).ID != web.ID || r.ActiveFor(plan.RuntimeID) != nil || r.ActiveNode(server.ID) == nil || r.ActiveNode(plan.ID) != nil {
		t.Fatal("ActiveFor/ActiveNode must find a node only while it is current")
	}
	// A branch moves on in its own place; the other arrives and leaves the list.
	next := NodeRun{ID: uuid.New(), StepID: "server", AgentID: server.AgentID, RuntimeID: server.RuntimeID, Branch: branch("server")}
	r.View.Nodes = append(r.View.Nodes, next)
	r.ReplaceCurrent(server.ID, next)
	if r.View.Task.CurrentNodes[0].NodeRunID != next.ID || len(r.View.Task.CurrentNodes) != 2 {
		t.Fatalf("replace: %+v", r.View.Task.CurrentNodes)
	}
	r.RemoveCurrent(web.ID)
	if r.Current() == nil || r.Current().ID != next.ID || *r.View.Task.CurrentStepID != "server" {
		t.Fatalf("one branch left: %+v", r.View.Task)
	}
}

// A view stored before forks has no list: it reads as the one current node, and an empty list is written out as [].
func TestCurrentNodesOfAViewStoredBeforeForks(t *testing.T) {
	node, agent := uuid.New(), uuid.New()
	wf, _ := json.Marshal(twoSteps())
	var d Detail
	raw := `{"task":{"currentNodeRunId":"` + node.String() + `","currentStepId":"verify","currentAgentId":"` + agent.String() + `"},"workflow":` + string(wf) + `,"nodes":[{"id":"` + node.String() + `","stepId":"verify"}]}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Task.CurrentNodes) != 1 || d.Task.CurrentNodes[0] != (CurrentNode{NodeRunID: node, StepID: "verify", StepTitle: "Verify", AgentID: agent}) {
		t.Fatalf("current nodes: %+v", d.Task.CurrentNodes)
	}
	r := Record{View: d}
	if len(r.Active()) != 1 || r.Active()[0].ID != node {
		t.Fatal("Active must give the one current node of an old view")
	}
	var empty Detail
	if err := json.Unmarshal([]byte(`{"task":{"currentNodeRunId":null}}`), &empty); err != nil || empty.Task.CurrentNodes == nil || len(empty.Task.CurrentNodes) != 0 {
		t.Fatalf("no current node: %+v, %v", empty.Task.CurrentNodes, err)
	}
	b, _ := json.Marshal(Summary{})
	if !strings.Contains(string(b), `"currentNodes":[]`) {
		t.Fatalf("an empty list must be written as []: %s", b)
	}
}
