package httpapi_test

import (
	"context"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
)

// waitAgentIdle 同 waitIdle：只看状态接口时，忙闲锁可能还没释放。
func (e *env) waitAgentIdle(a string) {
	e.t.Helper()
	waitIdle(e.t, e, a)
}

// 项目、Manager 与按范围的 Agent 接口（请求与响应都经契约校验）。
func TestProjectsAPI(t *testing.T) {
	e := newEnv(t)
	model := map[string]string{"provider": "anthropic", "modelId": "claude-x"}

	list := decode[apitypes.ProjectList](t, e.expect(200, "GET", "/projects", nil))
	if len(list.Projects) != 1 || !list.Projects[0].IsDefault {
		t.Fatalf("初始只有默认项目：%+v", list)
	}
	p := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "模型接入"}))
	e.expect(409, "POST", "/projects", map[string]any{"name": "模型接入"})
	e.expect(422, "POST", "/projects", map[string]any{"name": "a/b"}, "X-Contract-Invalid-Request", "1")
	pp := "/projects/" + p.ID.String()
	if got := decode[apitypes.Project](t, e.expect(200, "PATCH", pp, map[string]any{"name": "接入"})); got.Name != "接入" {
		t.Fatalf("改名失败：%+v", got)
	}

	// 按范围创建与列出。
	inProj := decode[apitypes.Agent](t, e.expect(201, "POST", pp+"/agents", map[string]any{"name": "对接员", "model": model}))
	onPlat := decode[apitypes.Agent](t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": "巡检员", "model": model}))
	e.expect(409, "POST", "/platform/agents", map[string]any{"name": "巡检员", "model": model})
	if inProj.Scope != "project" || inProj.ProjectID == nil || *inProj.ProjectID != p.ID || onPlat.Scope != "platform" || onPlat.ProjectID != nil {
		t.Fatalf("归属不对：%+v %+v", inProj, onPlat)
	}
	pl := decode[apitypes.AgentList](t, e.expect(200, "GET", "/platform/agents", nil))
	prl := decode[apitypes.AgentList](t, e.expect(200, "GET", pp+"/agents", nil))
	if len(pl.Agents) != 1 || pl.Agents[0].ID != onPlat.ID || len(prl.Agents) != 1 || prl.Agents[0].ID != inProj.ID {
		t.Fatalf("按范围列出不对：%+v %+v", pl, prl)
	}

	// Manager。
	e.expect(422, "PUT", pp+"/manager", map[string]any{"agentId": onPlat.ID})
	if got := decode[apitypes.Project](t, e.expect(200, "PUT", pp+"/manager", map[string]any{"agentId": inProj.ID})); got.ManagerAgentID == nil || *got.ManagerAgentID != inProj.ID {
		t.Fatalf("指定 Manager 失败：%+v", got)
	}
	if got := decode[apitypes.Agent](t, e.expect(200, "GET", "/agents/"+inProj.ID.String(), nil)); !got.IsManager {
		t.Fatal("Agent 应标为 Manager")
	}
	if got := decode[apitypes.Project](t, e.expect(200, "DELETE", pp+"/manager", nil)); got.ManagerAgentID != nil {
		t.Fatalf("取消 Manager 失败：%+v", got)
	}

	// 删除：默认项目不能删；有忙的 Agent 时 409 并列出；空闲后删除。
	def := list.Projects[0]
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "DELETE", "/projects/"+def.ID.String(), nil))
	if len(bad.Error.Fields) != 1 || bad.Error.Fields[0].Field != "projectId" {
		t.Fatalf("删除默认项目应指向 projectId：%+v", bad)
	}
	a := "/agents/" + inProj.ID.String()
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "block"})
	busy := decode[apitypes.ErrorBody](t, e.expect(409, "DELETE", pp, nil))
	if busy.Error.Code != "agent_not_idle" {
		t.Fatalf("有忙的 Agent 时应 409：%+v", busy)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "停", "whenBusy": "interrupt"})
	e.waitAgentIdle(a)
	e.expect(204, "DELETE", pp, nil)
	e.expect(404, "GET", pp, nil)
	e.expect(404, "GET", pp+"/agents", nil)
	e.expect(404, "GET", a, nil)
	if l := decode[apitypes.ProjectList](t, e.expect(200, "GET", "/projects", nil)); len(l.Projects) != 1 {
		t.Fatalf("删除后列表中不应再有该项目：%+v", l)
	}
	decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "接入"})) // 名称可再用

	// 用量、异常与概览的筛选参数。
	q := "?projectId=" + def.ID.String() + "&platform=true"
	e.expect(422, "GET", "/usage"+q, nil)
	e.expect(422, "GET", "/anomalies"+q, nil)
	e.expect(422, "GET", "/usage?projectId=not-a-uuid", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/usage?projectId="+p.ID.String(), nil)
	e.expect(404, "GET", "/anomalies?projectId="+p.ID.String(), nil)
	e.expect(200, "GET", "/usage?platform=true", nil)
	e.expect(200, "GET", "/anomalies?projectId="+def.ID.String(), nil)
}

// 外部身份可以新建、改名、删除项目并指定或取消 Manager，校验与控制台相同；每个写操作都记入审计，操作者是该身份。
func TestProjectGovernanceExternal(t *testing.T) {
	e := newEnv(t)
	who, tok := e.identity("ops-bot")
	ext := e.external(tok)

	p := decode[apitypes.Project](t, ext.expect(201, "POST", "/projects", map[string]any{"name": "外部项目"}))
	ext.expectError(409, "name_conflict", "POST", "/projects", map[string]any{"name": "外部项目"})
	pp := "/projects/" + p.ID.String()
	if got := decode[apitypes.Project](t, ext.expect(200, "PATCH", pp, map[string]any{"name": "外部项目2"})); got.Name != "外部项目2" {
		t.Fatalf("改名失败：%+v", got)
	}
	a := decode[apitypes.Agent](t, ext.expect(201, "POST", pp+"/agents", map[string]any{"name": "经理", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	ext.expectError(422, "validation_failed", "PUT", pp+"/manager", map[string]any{"agentId": e.platformAgent("巡检").ID})
	if got := decode[apitypes.Project](t, ext.expect(200, "PUT", pp+"/manager", map[string]any{"agentId": a.ID})); got.ManagerAgentID == nil || *got.ManagerAgentID != a.ID {
		t.Fatalf("指定 Manager 失败：%+v", got)
	}
	if got := decode[apitypes.Project](t, ext.expect(200, "DELETE", pp+"/manager", nil)); got.ManagerAgentID != nil {
		t.Fatalf("取消 Manager 失败：%+v", got)
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
			t.Errorf("审计记录中缺少外部身份的 %s：%v", op, seen)
		}
	}
}

// 按范围订阅概览：只收到该范围 Agent 的事件，快照也只含该范围。
func TestOverviewScopeFilter(t *testing.T) {
	e := newEnv(t)
	_, inDefault := e.newAgent("项目内")
	plat := decode[apitypes.Agent](t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": "平台", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	pa := "/agents/" + plat.ID.String()

	ch, stop := readSSEPath(t, e, "/overview/events?platform=true", "")
	defer stop()
	snap := next(t, ch)
	e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, snap.event, []byte(snap.data))
	s := decode[apitypes.SseOverviewSnapshot](t, []byte(snap.data))
	if len(s.Agents) != 1 || s.Agents[0].AgentID != plat.ID || s.Agents[0].Scope != "platform" {
		t.Fatalf("平台快照应只含平台 Agent：%+v", s)
	}
	// 先给项目内 Agent 发消息，再给平台 Agent 发；事件流中只应出现平台 Agent。
	e.expect(202, "POST", inDefault+"/messages", map[string]any{"text": "你好"})
	e.waitAgentIdle(inDefault)
	e.expect(202, "POST", pa+"/messages", map[string]any{"text": "你好"})
	for done := false; !done; {
		ev := next(t, ch)
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, ev.event, []byte(ev.data))
		st := decode[apitypes.SseAgentStatus](t, []byte(ev.data)).Agent
		if st.AgentID != plat.ID {
			t.Fatalf("按平台订阅不应收到项目内 Agent 的事件：%+v", ev)
		}
		done = !st.Status.Busy && st.LastRun != nil && st.LastRun.Status == "completed"
	}
}
