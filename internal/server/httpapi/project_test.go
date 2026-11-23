package httpapi_test

import (
	"context"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
)

// waitAgentIdle is the same as waitIdle: when only looking at the status endpoint, the busy lock may not have been
// released yet.
func (e *env) waitAgentIdle(a string) {
	e.t.Helper()
	waitIdle(e.t, e, a)
}

// Project, Manager and scoped Agent endpoints (requests and responses are validated against the contract).
func TestProjectsAPI(t *testing.T) {
	e := newEnv(t)
	model := map[string]string{"provider": "anthropic", "modelId": "claude-x"}

	list := decode[apitypes.ProjectList](t, e.expect(200, "GET", "/projects", nil))
	if len(list.Projects) != 1 || !list.Projects[0].IsDefault {
		t.Fatalf("initially only the default project: %+v", list)
	}
	p := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "Model integration"}))
	e.expect(409, "POST", "/projects", map[string]any{"name": "Model integration"})
	e.expect(422, "POST", "/projects", map[string]any{"name": "a/b"}, "X-Contract-Invalid-Request", "1")
	pp := "/projects/" + p.ID.String()
	if got := decode[apitypes.Project](t, e.expect(200, "PATCH", pp, map[string]any{"name": "Integration"})); got.Name != "Integration" {
		t.Fatalf("rename: %+v", got)
	}

	// Create and list by scope.
	inProj := decode[apitypes.Agent](t, e.expect(201, "POST", pp+"/agents", map[string]any{"name": "Liaison", "model": model}))
	onPlat := decode[apitypes.Agent](t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": "Inspector", "model": model}))
	e.expect(409, "POST", "/platform/agents", map[string]any{"name": "Inspector", "model": model})
	if inProj.Scope != "project" || inProj.ProjectID == nil || *inProj.ProjectID != p.ID || onPlat.Scope != "platform" || onPlat.ProjectID != nil {
		t.Fatalf("wrong owner: %+v %+v", inProj, onPlat)
	}
	pl := decode[apitypes.AgentList](t, e.expect(200, "GET", "/platform/agents", nil))
	prl := decode[apitypes.AgentList](t, e.expect(200, "GET", pp+"/agents", nil))
	if len(pl.Agents) != 1 || pl.Agents[0].ID != onPlat.ID || len(prl.Agents) != 1 || prl.Agents[0].ID != inProj.ID {
		t.Fatalf("wrong list by scope: %+v %+v", pl, prl)
	}

	// Manager.
	e.expect(422, "PUT", pp+"/manager", map[string]any{"agentId": onPlat.ID})
	if got := decode[apitypes.Project](t, e.expect(200, "PUT", pp+"/manager", map[string]any{"agentId": inProj.ID})); got.ManagerAgentID == nil || *got.ManagerAgentID != inProj.ID {
		t.Fatalf("set Manager: %+v", got)
	}
	if got := decode[apitypes.Agent](t, e.expect(200, "GET", "/agents/"+inProj.ID.String(), nil)); !got.IsManager {
		t.Fatal("the Agent should be marked as Manager")
	}
	if got := decode[apitypes.Project](t, e.expect(200, "DELETE", pp+"/manager", nil)); got.ManagerAgentID != nil {
		t.Fatalf("clear Manager: %+v", got)
	}

	// Delete: the default project cannot be deleted; with busy Agents it is 409 and they are listed; deleted once idle.
	def := list.Projects[0]
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "DELETE", "/projects/"+def.ID.String(), nil))
	if len(bad.Error.Fields) != 1 || bad.Error.Fields[0].Field != "projectId" {
		t.Fatalf("deleting the default project should point at projectId: %+v", bad)
	}
	a := "/agents/" + inProj.ID.String()
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "block"})
	busy := decode[apitypes.ErrorBody](t, e.expect(409, "DELETE", pp, nil))
	if busy.Error.Code != "agent_not_idle" {
		t.Fatalf("with a busy Agent it should be 409: %+v", busy)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "stop", "whenBusy": "interrupt"})
	e.waitAgentIdle(a)
	e.expect(204, "DELETE", pp, nil)
	e.expect(404, "GET", pp, nil)
	e.expect(404, "GET", pp+"/agents", nil)
	e.expect(404, "GET", a, nil)
	if l := decode[apitypes.ProjectList](t, e.expect(200, "GET", "/projects", nil)); len(l.Projects) != 1 {
		t.Fatalf("after deletion the list should no longer contain the project: %+v", l)
	}
	decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "Integration"})) // the name can be reused

	// Filter parameters of usage, anomalies and overview.
	q := "?projectId=" + def.ID.String() + "&platform=true"
	e.expect(422, "GET", "/usage"+q, nil)
	e.expect(422, "GET", "/anomalies"+q, nil)
	e.expect(422, "GET", "/usage?projectId=not-a-uuid", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/usage?projectId="+p.ID.String(), nil)
	e.expect(404, "GET", "/anomalies?projectId="+p.ID.String(), nil)
	e.expect(200, "GET", "/usage?platform=true", nil)
	e.expect(200, "GET", "/anomalies?projectId="+def.ID.String(), nil)
}

// External identities can create, rename and delete projects and set or clear the Manager, with the same validation as
// the console; every write is audited with that identity as the actor.
func TestProjectGovernanceExternal(t *testing.T) {
	e := newEnv(t)
	who, tok := e.identity("ops-bot")
	ext := e.external(tok)

	p := decode[apitypes.Project](t, ext.expect(201, "POST", "/projects", map[string]any{"name": "External project"}))
	ext.expectError(409, "name_conflict", "POST", "/projects", map[string]any{"name": "External project"})
	pp := "/projects/" + p.ID.String()
	if got := decode[apitypes.Project](t, ext.expect(200, "PATCH", pp, map[string]any{"name": "External project 2"})); got.Name != "External project 2" {
		t.Fatalf("rename: %+v", got)
	}
	a := decode[apitypes.Agent](t, ext.expect(201, "POST", pp+"/agents", map[string]any{"name": "Lead", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	ext.expectError(422, "validation_failed", "PUT", pp+"/manager", map[string]any{"agentId": e.platformAgent("Patrol").ID})
	if got := decode[apitypes.Project](t, ext.expect(200, "PUT", pp+"/manager", map[string]any{"agentId": a.ID})); got.ManagerAgentID == nil || *got.ManagerAgentID != a.ID {
		t.Fatalf("set Manager: %+v", got)
	}
	if got := decode[apitypes.Project](t, ext.expect(200, "DELETE", pp+"/manager", nil)); got.ManagerAgentID != nil {
		t.Fatalf("clear Manager: %+v", got)
	}
	ext.expectError(422, "validation_failed", "DELETE", "/projects/"+e.dc.Project.ID.String(), nil)
	ext.expect(204, "DELETE", pp, nil)
	ext.expectError(404, "not_found", "GET", pp, nil)
	ext.expectError(403, "forbidden", "GET", pp+"/prompt", nil)

	rows, err := e.st.Pool.Query(context.Background(), `SELECT operation FROM audit_log WHERE actor_kind = 'external' AND actor_id = $1 AND status < 300`, who.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatal(err)
		}
		seen[op] = true
	}
	rows.Close()
	for _, op := range []string{"createProject", "updateProject", "setProjectManager", "clearProjectManager", "deleteProject"} {
		if !seen[op] {
			t.Errorf("the audit log is missing the external identity's %s: %v", op, seen)
		}
	}
}

// Subscribing to the overview by scope: only events of Agents in that scope arrive, and the snapshot only covers it.
func TestOverviewScopeFilter(t *testing.T) {
	e := newEnv(t)
	_, inDefault := e.newAgent("In project")
	plat := decode[apitypes.Agent](t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": "Platform", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	pa := "/agents/" + plat.ID.String()

	ch, stop := readSSEPath(t, e, "/overview/events?platform=true", "")
	defer stop()
	snap := next(t, ch)
	e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, snap.event, []byte(snap.data))
	s := decode[apitypes.SseOverviewSnapshot](t, []byte(snap.data))
	if len(s.Agents) != 1 || s.Agents[0].AgentID != plat.ID || s.Agents[0].Scope != "platform" {
		t.Fatalf("the platform snapshot should contain only platform Agents: %+v", s)
	}
	// Message an Agent in the project first, then the platform Agent; only the platform Agent should appear in the
	// event stream.
	e.expect(202, "POST", inDefault+"/messages", map[string]any{"text": "Hello"})
	e.waitAgentIdle(inDefault)
	e.expect(202, "POST", pa+"/messages", map[string]any{"text": "Hello"})
	for done := false; !done; {
		ev := next(t, ch)
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, ev.event, []byte(ev.data))
		st := decode[apitypes.SseAgentStatus](t, []byte(ev.data)).Agent
		if st.AgentID != plat.ID {
			t.Fatalf("a platform subscription should not receive events of Agents in a project: %+v", ev)
		}
		done = !st.Status.Busy && st.LastRun != nil && st.LastRun.Status == "completed"
	}
}
