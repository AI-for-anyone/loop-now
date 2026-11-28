package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// forked is plan → fork build {server, web} → join verify → finish, with a capped way back from verify to plan. Each
// branch has its own Agent; plan, verify and finish share a third.
func forked() Definition {
	lead, server, web := uuid.New(), uuid.New(), uuid.New()
	step := func(id string, agent uuid.UUID) Step {
		return Step{ID: id, Title: id, AgentID: agent, InputFields: []Field{}, OutputFields: []Field{}, Inputs: map[string]Source{}}
	}
	return Definition{StartStepID: "plan", Steps: []Step{
		step("plan", lead), step("server", server), step("server_check", server), step("web", web), step("verify", lead), step("finish", lead),
	}, Edges: []Edge{
		{ID: "to_server", From: "plan", To: "server", Label: "Server", Fork: "build"},
		{ID: "to_web", From: "plan", To: "web", Label: "Web", Fork: "build"},
		{ID: "server_done", From: "server", To: "server_check", Label: "Check"},
		{ID: "server_again", From: "server_check", To: "server", Label: "Again", MaxTraversals: ptr(2)},
		{ID: "server_checked", From: "server_check", To: "verify", Label: "Checked"},
		{ID: "web_done", From: "web", To: "verify", Label: "Done"},
		{ID: "rework", From: "verify", To: "plan", Label: "Rework", MaxTraversals: ptr(2)},
		{ID: "passed", From: "verify", To: "finish", Label: "Passed"},
	}, Forks: []Fork{{ID: "build", Join: "verify"}}}
}

func TestForkJoinValidation(t *testing.T) {
	if err := forked().Validate(Limits{}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(*Definition)
		field  string
	}{
		"undeclared fork":        {func(d *Definition) { d.Edges[0].Fork, d.Edges[1].Fork = "other", "other" }, "workflow.edges"},
		"fork without join step": {func(d *Definition) { d.Forks[0].Join = "missing" }, "workflow.forks"},
		"fork with one edge": {func(d *Definition) {
			d.Edges[1].Fork = ""
			d.Edges = append(d.Edges, Edge{ID: "web_only", From: "plan", To: "web", Label: "Web"})
		}, "workflow.forks"},
		"declared fork without edges": {func(d *Definition) { d.Forks = append(d.Forks, Fork{ID: "spare", Join: "verify"}) }, "workflow.forks"},
		"fork edges from two steps": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "extra", From: "verify", To: "web", Label: "Web", Fork: "build"})
		}, "workflow.forks"},
		"fork id used by an edge": {func(d *Definition) { d.Forks[0].ID = "passed"; d.Edges[0].Fork, d.Edges[1].Fork = "passed", "passed" }, "workflow.forks"},
		"duplicate fork":          {func(d *Definition) { d.Forks = append(d.Forks, d.Forks[0]) }, "workflow.forks"},
		"bad fork id":             {func(d *Definition) { d.Forks[0].ID = "1x"; d.Edges[0].Fork, d.Edges[1].Fork = "1x", "1x" }, "workflow.forks"},
		"missing join": {func(d *Definition) {
			// The web branch never reaches verify: it ends the task on its own.
			d.Edges[5].To = "finish"
		}, "workflow.forks"},
		"fork edge straight to the join": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "to_verify", From: "plan", To: "verify", Label: "Verify", Fork: "build"})
		}, "workflow.forks"},
		"branches share a step": {func(d *Definition) {
			d.Edges[5] = Edge{ID: "web_done", From: "web", To: "server_check", Label: "Done"}
		}, "workflow.forks"},
		"edge from one branch into another": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "cross", From: "web", To: "server", Label: "Cross", MaxTraversals: ptr(1)})
		}, "workflow.forks"},
		"edge into a branch from outside": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "back_in", From: "verify", To: "server", Label: "Fix", MaxTraversals: ptr(2)})
		}, "workflow.forks"},
		"loop leaving a branch": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "replan", From: "server_check", To: "plan", Label: "Replan", MaxTraversals: ptr(2)})
		}, "workflow.forks"},
		"edge out of a branch past the join": {func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "skip", From: "web", To: "finish", Label: "Skip"})
		}, "workflow.forks"},
		"nested fork": {func(d *Definition) {
			d.Steps = append(d.Steps, Step{ID: "server_ui", Title: "ui", AgentID: d.Steps[1].AgentID, InputFields: []Field{}, OutputFields: []Field{}, Inputs: map[string]Source{}})
			d.Edges[2] = Edge{ID: "server_done", From: "server", To: "server_check", Label: "Check", Fork: "inner"}
			d.Edges = append(d.Edges, Edge{ID: "to_ui", From: "server", To: "server_ui", Label: "UI", Fork: "inner"},
				Edge{ID: "ui_done", From: "server_ui", To: "server_check", Label: "Done"})
			d.Forks = append(d.Forks, Fork{ID: "inner", Join: "server_check"})
		}, "workflow.forks"},
		"agent in two branches":      {func(d *Definition) { d.Steps[3].AgentID = d.Steps[1].AgentID }, "workflow.steps.agentId"},
		"branch agent also outside":  {func(d *Definition) { d.Steps[5].AgentID = d.Steps[3].AgentID }, "workflow.steps.agentId"},
		"zero parallel branches":     {func(d *Definition) { d.MaxParallelBranches = ptr(0) }, "workflow.maxParallelBranches"},
		"unbounded loop in a branch": {func(d *Definition) { d.Edges[3].MaxTraversals = nil }, "workflow.edges"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := forked()
			c.mutate(&d)
			err := d.Validate(Limits{})
			var e *Error
			if err == nil || !errorAs(err, &e) || e.Field != c.field {
				t.Fatalf("want a %s error, got %v", c.field, err)
			}
			t.Log(err)
		})
	}
}

func errorAs(err error, out **Error) bool {
	e, ok := err.(*Error)
	*out = e
	return ok
}

func TestForkJoinShapesAllowed(t *testing.T) {
	allowed := map[string]func(*Definition){
		"a limit on branches at once": func(d *Definition) { d.MaxParallelBranches = ptr(1) },
		"three branches": func(d *Definition) {
			d.Steps = append(d.Steps, Step{ID: "docs", Title: "docs", AgentID: uuid.New(), InputFields: []Field{}, OutputFields: []Field{}, Inputs: map[string]Source{}})
			d.Edges = append(d.Edges, Edge{ID: "to_docs", From: "plan", To: "docs", Label: "Docs", Fork: "build"}, Edge{ID: "docs_done", From: "docs", To: "verify", Label: "Done"})
		},
		// verify can send both branches back at once: a second fork into the same branches with the same join.
		"a second fork into the same branches": func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "fix_server", From: "verify", To: "server", Label: "Fix", Fork: "fix", MaxTraversals: ptr(2)},
				Edge{ID: "fix_web", From: "verify", To: "web", Label: "Fix", Fork: "fix", MaxTraversals: ptr(2)})
			d.Forks = append(d.Forks, Fork{ID: "fix", Join: "verify"})
		},
		"a trunk edge straight to the join": func(d *Definition) {
			d.Edges = append(d.Edges, Edge{ID: "nothing_to_build", From: "plan", To: "verify", Label: "Nothing to build"})
		},
		"a step before the fork": func(d *Definition) {
			d.Steps = append(d.Steps, Step{ID: "intake", Title: "intake", AgentID: d.Steps[0].AgentID, InputFields: []Field{}, OutputFields: []Field{}, Inputs: map[string]Source{}})
			d.StartStepID = "intake"
			d.Edges = append(d.Edges, Edge{ID: "planned", From: "intake", To: "plan", Label: "Plan"})
		},
	}
	for name, mutate := range allowed {
		t.Run(name, func(t *testing.T) {
			d := forked()
			mutate(&d)
			if err := d.Validate(Limits{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBranchOfSteps(t *testing.T) {
	d := forked()
	for step, want := range map[string]string{"plan": "", "server": "server", "server_check": "server", "web": "web", "verify": "", "finish": ""} {
		if got := d.Branch(step); got != want {
			t.Fatalf("Branch(%s) = %q, want %q", step, got, want)
		}
	}
	f, ok := d.Fork("build")
	if !ok || f.Join != "verify" {
		t.Fatalf("Fork(build) = %+v, %v", f, ok)
	}
	edges := d.ForkEdges("build")
	if len(edges) != 2 || edges[0].To != "server" || edges[1].To != "web" {
		t.Fatalf("fork edges out of order: %+v", edges)
	}
	if d.JoinOf("server") != "verify" || d.JoinOf("web") != "verify" || d.JoinOf("") != "" {
		t.Fatal("JoinOf must give the step where a branch ends")
	}
}

func TestSelectExitTakesAForkOrAnEdge(t *testing.T) {
	d := forked()
	exit, err := d.SelectExit("plan", ptr("build"), nil)
	if err != nil || exit.Fork == nil || exit.Fork.ID != "build" || len(exit.Edges) != 2 || exit.Edge != nil {
		t.Fatalf("fork exit = %+v, %v", exit, err)
	}
	// A fork's edges are taken together: one of them alone is not an exit.
	if _, err := d.SelectExit("plan", ptr("to_server"), nil); err == nil {
		t.Fatal("a single fork edge was accepted as an exit")
	}
	exit, err = d.SelectExit("verify", ptr("passed"), nil)
	if err != nil || exit.Edge == nil || exit.Edge.To != "finish" || exit.Fork != nil {
		t.Fatalf("edge exit = %+v, %v", exit, err)
	}
	if _, err := d.SelectExit("verify", ptr("build"), nil); err == nil {
		t.Fatal("a fork of another step was accepted")
	}
	if _, err := d.SelectExit("plan", nil, nil); err == nil {
		t.Fatal("a step with exits accepted edgeId null")
	}
	exit, err = d.SelectExit("finish", nil, nil)
	if err != nil || exit.Edge != nil || exit.Fork != nil {
		t.Fatalf("end exit = %+v, %v", exit, err)
	}
	// Every fork edge is bounded by its own cap.
	d.Edges[0].MaxTraversals = ptr(1)
	if _, err := d.SelectExit("plan", ptr("build"), map[string]int{"to_server": 1}); err == nil {
		t.Fatal("a fork whose edge reached its cap was accepted")
	}
}

func TestWorkflowWithoutForksIsWrittenAsBefore(t *testing.T) {
	b, _ := json.Marshal(fixture())
	for _, key := range []string{"forks", "fork", "maxParallelBranches"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("a workflow without forks writes %s: %s", key, b)
		}
	}
	d := forked()
	d.MaxParallelBranches = ptr(1)
	b, _ = json.Marshal(d)
	var back Definition
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(back, d) {
		t.Fatalf("round trip: %v", err)
	}
	if !fixture().Sequential() || forked().Sequential() {
		t.Fatal("Sequential must report whether the workflow has no forks")
	}
}
