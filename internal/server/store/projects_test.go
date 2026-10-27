package store_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type projEnv struct {
	t   *testing.T
	s   *store.Store
	ctx context.Context
	dc  store.DefaultContext
}

func newProjEnv(t *testing.T) *projEnv {
	t.Helper()
	s := pgtest.NewStore(t)
	ctx := context.Background()
	dc, err := s.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	return &projEnv{t: t, s: s, ctx: ctx, dc: dc}
}

func (e *projEnv) project(name string) store.Project {
	e.t.Helper()
	p, err := e.s.CreateProject(e.ctx, e.dc.Organization.ID, name)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *projEnv) agent(scope store.Scope, name string) store.Agent {
	e.t.Helper()
	a, err := e.s.CreateAgent(e.ctx, scope, name, "anthropic", "claude-x", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func wantErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("期望错误码 %s，实际 %v", code, err)
	}
}

// ---- 迁移 00003 ----

func TestMigration00003(t *testing.T) {
	schema := pgtest.NewSchema(t)
	ctx := context.Background()
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.MigrateUpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	// 第一版的数据：默认组织与项目、一个 Agent；另一个项目中有同名模板。
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	org, def, other, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	exec(`INSERT INTO organizations (id, name, is_default) VALUES ($1, '默认组织', true)`, org)
	exec(`INSERT INTO projects (id, organization_id, name, is_default) VALUES ($1, $2, '默认项目', true), ($3, $2, '其他', false)`, def, org, other)
	exec(`INSERT INTO agents (id, project_id, name, model_provider, model_id) VALUES ($1, $2, '开发助手', 'anthropic', 'claude-x')`, agent, def)
	exec(`INSERT INTO skill_templates (id, project_id, name, description) VALUES ($1, $2, 'pdf', 'd'), ($3, $4, 'pdf', 'd')`,
		uuid.Must(uuid.NewV7()), def, uuid.Must(uuid.NewV7()), other)

	// 跨项目同名模板：迁移失败并报出名称，数据库停在 00002。
	err = s.MigrateUpTo(ctx, 3)
	if err == nil || !strings.Contains(err.Error(), "pdf") {
		t.Fatalf("跨项目同名模板应让迁移失败并报出名称：%v", err)
	}
	exec(`UPDATE skill_templates SET name = 'pdf-2' WHERE project_id = $1`, other)
	if err := s.MigrateUpTo(ctx, 3); err != nil {
		t.Fatalf("处理冲突后迁移应成功：%v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM agents WHERE id = $1 AND project_id = $2`, agent, def).Scan(&n); err != nil || n != 1 {
		t.Fatalf("已有 Agent 应保留：%d %v", n, err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM skill_templates`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("已有模板应保留：%d %v", n, err)
	}

	// 没有平台 Agent 时可以回退到 00002；模板回填到默认项目。
	if err := s.MigrateDownTo(ctx, 2); err != nil {
		t.Fatalf("回退应成功：%v", err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM skill_templates WHERE project_id = $1`, def).Scan(&n); err != nil || n != 2 {
		t.Fatalf("回退后模板应归默认项目：%d %v", n, err)
	}
	// 有平台 Agent 时回退失败。
	if err := s.MigrateUpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO agents (id, project_id, name, model_provider, model_id) VALUES ($1, NULL, '巡检员', 'anthropic', 'claude-x')`, uuid.Must(uuid.NewV7()))
	if err := s.MigrateDownTo(ctx, 2); err == nil || !strings.Contains(err.Error(), "平台持有") {
		t.Fatalf("有平台 Agent 时回退应失败：%v", err)
	}
}

// ---- 项目 ----

func TestProjectsCRUDAndNameReuse(t *testing.T) {
	e := newProjEnv(t)
	p := e.project("模型接入")
	if p.IsDefault || p.AgentCount != 0 || p.ManagerAgentID != nil {
		t.Fatalf("新项目的字段不对：%+v", p)
	}
	_, err := e.s.CreateProject(e.ctx, e.dc.Organization.ID, "模型接入")
	wantErrCode(t, err, apperr.CodeNameConflict)
	_, err = e.s.RenameProject(e.ctx, p.ID, "默认项目")
	wantErrCode(t, err, apperr.CodeNameConflict)
	renamed, err := e.s.RenameProject(e.ctx, p.ID, "接入")
	if err != nil || renamed.Name != "接入" {
		t.Fatalf("改名失败：%+v %v", renamed, err)
	}
	e.agent(store.ProjectScope(p.ID), "对接员")
	list, err := e.s.ListProjects(e.ctx, e.dc.Organization.ID)
	if err != nil || len(list) != 2 || !list[0].IsDefault || list[1].Name != "接入" || list[1].AgentCount != 1 {
		t.Fatalf("项目列表不对：%+v %v", list, err)
	}
	if _, err := e.s.DeleteProject(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.GetProject(e.ctx, p.ID)
	wantErrCode(t, err, apperr.CodeNotFound)
	_, err = e.s.RenameProject(e.ctx, p.ID, "x")
	wantErrCode(t, err, apperr.CodeNotFound)
	if again := e.project("接入"); again.ID == p.ID {
		t.Fatal("已删除项目的名称应可以再用，并且是新项目")
	}
}

func TestDefaultProjectCannotBeDeleted(t *testing.T) {
	e := newProjEnv(t)
	_, err := e.s.DeleteProject(e.ctx, e.dc.Project.ID)
	wantErrCode(t, err, apperr.CodeValidationFailed)
	// 数据库约束兜底。
	if _, err := e.s.Pool.Exec(e.ctx, `UPDATE projects SET deleted_at = now() WHERE is_default`); err == nil {
		t.Fatal("数据库应拒绝删除默认项目")
	}
	// 默认项目可以改名。
	if p, err := e.s.RenameProject(e.ctx, e.dc.Project.ID, "主项目"); err != nil || p.Name != "主项目" || !p.IsDefault {
		t.Fatalf("默认项目应可以改名：%+v %v", p, err)
	}
}

// ---- 平台 Agent 与范围 ----

func TestPlatformAgentsAndScopes(t *testing.T) {
	e := newProjEnv(t)
	other := e.project("其他")
	inDefault := e.agent(store.ProjectScope(e.dc.Project.ID), "助手")
	inOther := e.agent(store.ProjectScope(other.ID), "助手") // 不同项目可以同名
	platform := e.agent(store.PlatformScope(), "助手")       // 平台与项目可以同名
	if platform.ProjectID != nil || inDefault.ProjectID == nil || *inDefault.ProjectID != e.dc.Project.ID {
		t.Fatalf("归属不对：平台 %v，项目 %v", platform.ProjectID, inDefault.ProjectID)
	}
	_, err := e.s.CreateAgent(e.ctx, store.PlatformScope(), "助手", "anthropic", "claude-x", "")
	wantErrCode(t, err, apperr.CodeNameConflict)
	_, err = e.s.CreateAgent(e.ctx, store.ProjectScope(uuid.Must(uuid.NewV7())), "x", "anthropic", "claude-x", "")
	wantErrCode(t, err, apperr.CodeNotFound)

	ids := func(items []store.AgentListItem) []uuid.UUID {
		var out []uuid.UUID
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	check := func(what string, items []store.AgentListItem, err error, want ...uuid.UUID) {
		t.Helper()
		got := ids(items)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s：得到 %v（%v），期望 %v", what, got, err, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s：得到 %v，期望 %v", what, got, want)
			}
		}
	}
	l, err := e.s.ListAgents(e.ctx, store.PlatformScope())
	check("平台列表", l, err, platform.ID)
	l, err = e.s.ListAgents(e.ctx, store.ProjectScope(other.ID))
	check("项目列表", l, err, inOther.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{})
	check("全部", l, err, inDefault.ID, inOther.ID, platform.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{Platform: true})
	check("筛选平台", l, err, platform.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{ProjectID: &other.ID})
	check("筛选项目", l, err, inOther.ID)
	if _, err := e.s.DeleteProject(e.ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{})
	check("删除项目后的全部", l, err, inDefault.ID, platform.ID)
	if pid, err := e.s.AgentProjectAny(e.ctx, inOther.ID); err != nil || pid == nil || *pid != other.ID {
		t.Fatalf("已删除 Agent 的归属仍应可查：%v %v", pid, err)
	}
}

// ---- Manager ----

func TestProjectManager(t *testing.T) {
	e := newProjEnv(t)
	other := e.project("其他")
	a := e.agent(store.ProjectScope(e.dc.Project.ID), "开发助手")
	b := e.agent(store.ProjectScope(e.dc.Project.ID), "规划助手")
	foreign := e.agent(store.ProjectScope(other.ID), "外人")
	platform := e.agent(store.PlatformScope(), "巡检员")

	p, err := e.s.SetProjectManager(e.ctx, e.dc.Project.ID, a.ID)
	if err != nil || p.ManagerAgentID == nil || *p.ManagerAgentID != a.ID {
		t.Fatalf("指定 Manager 失败：%+v %v", p, err)
	}
	if got, _ := e.s.GetAgent(e.ctx, a.ID); !got.IsManager {
		t.Fatal("Agent 应标为 Manager")
	}
	if p, err = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, b.ID); err != nil || *p.ManagerAgentID != b.ID {
		t.Fatalf("更换 Manager 失败：%+v %v", p, err)
	}
	if got, _ := e.s.GetAgent(e.ctx, a.ID); got.IsManager {
		t.Fatal("原 Manager 不应再标为 Manager")
	}
	for _, x := range []store.Agent{foreign, platform} {
		_, err := e.s.SetProjectManager(e.ctx, e.dc.Project.ID, x.ID)
		wantErrCode(t, err, apperr.CodeValidationFailed)
	}
	// 数据库外键兜底：直接写入其他项目或平台的 Agent 会失败。
	for _, x := range []store.Agent{foreign, platform} {
		if _, err := e.s.Pool.Exec(e.ctx, `UPDATE projects SET manager_agent_id = $1 WHERE id = $2`, x.ID, e.dc.Project.ID); err == nil {
			t.Fatalf("数据库应拒绝把 %s 设为 Manager", x.Name)
		}
	}
	// 删除担任 Manager 的 Agent：同时取消 Manager。
	if err := e.s.SoftDeleteAgent(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ = e.s.GetProject(e.ctx, e.dc.Project.ID); p.ManagerAgentID != nil {
		t.Fatalf("删除 Manager 后项目不应再有 Manager：%v", p.ManagerAgentID)
	}
	if _, err = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, b.ID); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("已删除的 Agent 不能设为 Manager：%v", err)
	}
	if p, err = e.s.ClearProjectManager(e.ctx, e.dc.Project.ID); err != nil || p.ManagerAgentID != nil {
		t.Fatalf("没有 Manager 时取消也应成功：%+v %v", p, err)
	}
}

// ---- 删除项目 ----

func TestDeleteProject(t *testing.T) {
	e := newProjEnv(t)
	p := e.project("临时")
	idle := e.agent(store.ProjectScope(p.ID), "空闲")
	busy := e.agent(store.ProjectScope(p.ID), "排队中")
	if _, err := e.s.SetProjectManager(e.ctx, p.ID, idle.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(e.ctx, `INSERT INTO agent_clis (agent_id, cli_name) VALUES ($1, 'gh')`, idle.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(e.ctx, `INSERT INTO agent_credentials (id, agent_id, purpose, cli_name, template_version, secret, field_names, env_names, file_paths, template_source)
		VALUES ($1, $2, 'cli', 'gh', '1', '\x00', '{}', '{}', '{}', 'builtin')`, uuid.Must(uuid.NewV7()), idle.ID); err != nil {
		t.Fatal(err)
	}
	_, run, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: busy.ID, RunID: uuid.Must(uuid.NewV7()), Text: "hi", Start: false,
		WhenBusy: "queue", InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}

	// 有排队消息：拒绝并列出不空闲的 Agent，项目与 Agent 都不变。
	_, err = e.s.DeleteProject(e.ctx, p.ID)
	ae, _ := apperr.As(err)
	details, _ := ae.Details.(store.ProjectNotIdleDetails)
	if ae == nil || ae.Code != apperr.CodeAgentNotIdle || len(details.Agents) != 1 || details.Agents[0].ID != busy.ID {
		t.Fatalf("应以 agent_not_idle 拒绝并列出排队中的 Agent：%+v", ae)
	}
	if got, err := e.s.GetProject(e.ctx, p.ID); err != nil || got.ManagerAgentID == nil || got.AgentCount != 2 {
		t.Fatalf("被拒绝的删除不应改动项目：%+v %v", got, err)
	}

	// 执行结束后删除：Agent 软删除、凭证密文删除、Manager 取消、名称可再用。
	if _, err := e.s.Pool.Exec(e.ctx, `UPDATE runs SET status = 'completed', ended_at = now() WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := e.s.DeleteProject(e.ctx, p.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("删除失败：%v %v", ids, err)
	}
	var live, creds int
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agents WHERE project_id = $1 AND deleted_at IS NULL`, p.ID).Scan(&live)
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agent_credentials WHERE agent_id = $1`, idle.ID).Scan(&creds)
	var mgr *uuid.UUID
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT manager_agent_id FROM projects WHERE id = $1`, p.ID).Scan(&mgr)
	if live != 0 || creds != 0 || mgr != nil {
		t.Fatalf("删除后应没有未删除的 Agent、凭证密文与 Manager：%d %d %v", live, creds, mgr)
	}
	var msgs int
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM conversation_messages WHERE agent_id = $1`, busy.ID).Scan(&msgs)
	if msgs == 0 {
		t.Fatal("对话历史应保留")
	}
	_, err = e.s.CreateAgent(e.ctx, store.ProjectScope(p.ID), "新来的", "anthropic", "claude-x", "")
	wantErrCode(t, err, apperr.CodeNotFound)
}

// ---- 全局 Skills 库 ----

func TestSkillTemplatesAreGlobal(t *testing.T) {
	e := newProjEnv(t)
	if _, err := e.s.CreateSkillTemplate(e.ctx, content.SkillMeta{Name: "pdf", Description: "d"}, []content.File{{Path: "SKILL.md", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.CreateSkillTemplate(e.ctx, content.SkillMeta{Name: "pdf", Description: "d"}, []content.File{{Path: "SKILL.md", Data: []byte("x")}})
	wantErrCode(t, err, apperr.CodeNameConflict)
	list, err := e.s.ListSkillTemplates(e.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("全局库应只有一个 pdf：%+v %v", list, err)
	}
}

// ---- 并发与边界（5.1）----

// 删除项目与在其中创建 Agent、向其中的 Agent 发消息并发：不会留下“已删除项目中有未删除 Agent”或已删除项目中有进行中的执行。
func TestDeleteProjectConcurrency(t *testing.T) {
	e := newProjEnv(t)
	for round := range 10 {
		p := e.project("并发-" + uuid.NewString()[:8])
		a := e.agent(store.ProjectScope(p.ID), "甲")
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		wg.Go(func() {
			if _, err := e.s.DeleteProject(e.ctx, p.ID); err != nil && !apperr.Is(err, apperr.CodeAgentNotIdle) {
				errs <- err
			}
		})
		for i := range 3 {
			wg.Go(func() {
				_, err := e.s.CreateAgent(e.ctx, store.ProjectScope(p.ID), "乙"+strconv.Itoa(i), "anthropic", "claude-x", "")
				if err != nil && !apperr.Is(err, apperr.CodeNotFound) {
					errs <- err
				}
			})
		}
		wg.Go(func() {
			_, _, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: a.ID, RunID: uuid.Must(uuid.NewV7()), Text: "hi", Start: false,
				WhenBusy: "queue", InstanceID: uuid.Must(uuid.NewV7())})
			if err != nil && !apperr.Is(err, apperr.CodeNotFound) {
				errs <- err
			}
		})
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("第 %d 轮出现意外错误：%v", round, err)
		}
		var deleted bool
		var live, active int
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id = $1`, p.ID).Scan(&deleted)
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agents WHERE project_id = $1 AND deleted_at IS NULL`, p.ID).Scan(&live)
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM runs r JOIN agents a ON a.id = r.agent_id
			WHERE a.project_id = $1 AND r.status IN ('queued', 'starting', 'running', 'aborting')`, p.ID).Scan(&active)
		if deleted && (live != 0 || active != 0) {
			t.Fatalf("第 %d 轮：已删除的项目中仍有 %d 个未删除 Agent、%d 个进行中的执行", round, live, active)
		}
		if !deleted && active == 0 {
			t.Fatalf("第 %d 轮：删除被拒绝时应有排队的执行", round)
		}
	}
}

// 指定 Manager 与删除该 Agent 并发：不会死锁，也不会留下指向已删除 Agent 的 Manager。
func TestSetManagerVsDeleteAgent(t *testing.T) {
	e := newProjEnv(t)
	for round := range 20 {
		a := e.agent(store.ProjectScope(e.dc.Project.ID), "候选-"+uuid.NewString()[:8])
		var wg sync.WaitGroup
		var setErr, delErr error
		wg.Go(func() { _, setErr = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, a.ID) })
		wg.Go(func() { delErr = e.s.SoftDeleteAgent(e.ctx, a.ID) })
		wg.Wait()
		if delErr != nil || (setErr != nil && !apperr.Is(setErr, apperr.CodeValidationFailed)) {
			t.Fatalf("第 %d 轮出现意外错误：set=%v delete=%v", round, setErr, delErr)
		}
		var mgr *uuid.UUID
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT manager_agent_id FROM projects WHERE id = $1`, e.dc.Project.ID).Scan(&mgr)
		if mgr != nil && *mgr == a.ID {
			t.Fatalf("第 %d 轮：Manager 指向了已删除的 Agent", round)
		}
	}
}
