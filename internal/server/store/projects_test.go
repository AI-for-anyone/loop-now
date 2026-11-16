package store_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

type projEnv struct {
	t   *testing.T
	s   *store.Store
	ctx context.Context
	dc  store.DefaultContext
}

func newProjEnv(t *testing.T) *projEnv {
	t.Helper()
	s := sqlitetest.NewStore(t)
	ctx := context.Background()
	dc, err := s.EnsureDefaults(ctx, "Default organization", "Default project")
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
		t.Fatalf("want error code %s, got %v", code, err)
	}
}

func TestProjectsCRUDAndNameReuse(t *testing.T) {
	e := newProjEnv(t)
	p := e.project("Model integration")
	if p.IsDefault || p.AgentCount != 0 || p.ManagerAgentID != nil {
		t.Fatalf("wrong fields on the new project: %+v", p)
	}
	_, err := e.s.CreateProject(e.ctx, e.dc.Organization.ID, "Model integration")
	wantErrCode(t, err, apperr.CodeNameConflict)
	_, err = e.s.RenameProject(e.ctx, p.ID, "Default project")
	wantErrCode(t, err, apperr.CodeNameConflict)
	renamed, err := e.s.RenameProject(e.ctx, p.ID, "Integration")
	if err != nil || renamed.Name != "Integration" {
		t.Fatalf("rename failed: %+v %v", renamed, err)
	}
	e.agent(store.ProjectScope(p.ID), "liaison")
	list, err := e.s.ListProjects(e.ctx, e.dc.Organization.ID)
	if err != nil || len(list) != 2 || !list[0].IsDefault || list[1].Name != "Integration" || list[1].AgentCount != 1 {
		t.Fatalf("wrong project list: %+v %v", list, err)
	}
	if _, err := e.s.DeleteProject(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.GetProject(e.ctx, p.ID)
	wantErrCode(t, err, apperr.CodeNotFound)
	_, err = e.s.RenameProject(e.ctx, p.ID, "x")
	wantErrCode(t, err, apperr.CodeNotFound)
	if again := e.project("Integration"); again.ID == p.ID {
		t.Fatal("the name of a deleted project should be reusable, by a new project")
	}
}

func TestDefaultProjectCannotBeDeleted(t *testing.T) {
	e := newProjEnv(t)
	_, err := e.s.DeleteProject(e.ctx, e.dc.Project.ID)
	wantErrCode(t, err, apperr.CodeValidationFailed)
	// The database constraint is the safety net.
	if _, err := e.s.Pool.Exec(e.ctx, `UPDATE projects SET deleted_at = $1 WHERE is_default`, time.Now()); err == nil {
		t.Fatal("the database should refuse to delete the default project")
	}
	// The default project can be renamed.
	if p, err := e.s.RenameProject(e.ctx, e.dc.Project.ID, "Main project"); err != nil || p.Name != "Main project" || !p.IsDefault {
		t.Fatalf("the default project should be renamable: %+v %v", p, err)
	}
}

// ---- Platform Agents and scopes ----

func TestPlatformAgentsAndScopes(t *testing.T) {
	e := newProjEnv(t)
	other := e.project("Other")
	inDefault := e.agent(store.ProjectScope(e.dc.Project.ID), "assistant")
	inOther := e.agent(store.ProjectScope(other.ID), "assistant") // different projects can use the same name
	platform := e.agent(store.PlatformScope(), "assistant")       // the platform and a project can use the same name
	if platform.ProjectID != nil || inDefault.ProjectID == nil || *inDefault.ProjectID != e.dc.Project.ID {
		t.Fatalf("wrong ownership: platform %v, project %v", platform.ProjectID, inDefault.ProjectID)
	}
	_, err := e.s.CreateAgent(e.ctx, store.PlatformScope(), "assistant", "anthropic", "claude-x", "")
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
			t.Fatalf("%s: got %v (%v), want %v", what, got, err, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", what, got, want)
			}
		}
	}
	l, err := e.s.ListAgents(e.ctx, store.PlatformScope())
	check("platform list", l, err, platform.ID)
	l, err = e.s.ListAgents(e.ctx, store.ProjectScope(other.ID))
	check("project list", l, err, inOther.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{})
	check("all", l, err, inDefault.ID, inOther.ID, platform.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{Platform: true})
	check("platform filter", l, err, platform.ID)
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{ProjectID: &other.ID})
	check("project filter", l, err, inOther.ID)
	if _, err := e.s.DeleteProject(e.ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	l, err = e.s.ListAgentsFiltered(e.ctx, store.ScopeFilter{})
	check("all after deleting the project", l, err, inDefault.ID, platform.ID)
	if pid, err := e.s.AgentProjectAny(e.ctx, inOther.ID); err != nil || pid == nil || *pid != other.ID {
		t.Fatalf("a deleted Agent's project should still be readable: %v %v", pid, err)
	}
}

// ---- Manager ----

func TestProjectManager(t *testing.T) {
	e := newProjEnv(t)
	other := e.project("Other")
	a := e.agent(store.ProjectScope(e.dc.Project.ID), "dev assistant")
	b := e.agent(store.ProjectScope(e.dc.Project.ID), "planning assistant")
	foreign := e.agent(store.ProjectScope(other.ID), "outsider")
	platform := e.agent(store.PlatformScope(), "inspector")

	p, err := e.s.SetProjectManager(e.ctx, e.dc.Project.ID, a.ID)
	if err != nil || p.ManagerAgentID == nil || *p.ManagerAgentID != a.ID {
		t.Fatalf("setting the Manager failed: %+v %v", p, err)
	}
	if got, _ := e.s.GetAgent(e.ctx, a.ID); !got.IsManager {
		t.Fatal("the Agent should be marked as Manager")
	}
	if p, err = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, b.ID); err != nil || *p.ManagerAgentID != b.ID {
		t.Fatalf("changing the Manager failed: %+v %v", p, err)
	}
	if got, _ := e.s.GetAgent(e.ctx, a.ID); got.IsManager {
		t.Fatal("the previous Manager should no longer be marked as Manager")
	}
	for _, x := range []store.Agent{foreign, platform} {
		_, err := e.s.SetProjectManager(e.ctx, e.dc.Project.ID, x.ID)
		wantErrCode(t, err, apperr.CodeValidationFailed)
	}
	// The database foreign key is the safety net: writing an Agent from another project or the platform directly fails.
	for _, x := range []store.Agent{foreign, platform} {
		if _, err := e.s.Pool.Exec(e.ctx, `UPDATE projects SET manager_agent_id = $1 WHERE id = $2`, x.ID, e.dc.Project.ID); err == nil {
			t.Fatalf("the database should refuse to make %s the Manager", x.Name)
		}
	}
	// Deleting the Agent that is the Manager: the Manager is cleared too.
	if err := e.s.SoftDeleteAgent(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ = e.s.GetProject(e.ctx, e.dc.Project.ID); p.ManagerAgentID != nil {
		t.Fatalf("the project should have no Manager after the Manager is deleted: %v", p.ManagerAgentID)
	}
	if _, err = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, b.ID); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("a deleted Agent must not become the Manager: %v", err)
	}
	if p, err = e.s.ClearProjectManager(e.ctx, e.dc.Project.ID); err != nil || p.ManagerAgentID != nil {
		t.Fatalf("clearing should also succeed without a Manager: %+v %v", p, err)
	}
}

// ---- Deleting projects ----

func TestDeleteProject(t *testing.T) {
	e := newProjEnv(t)
	p := e.project("Temporary")
	idle := e.agent(store.ProjectScope(p.ID), "idle")
	busy := e.agent(store.ProjectScope(p.ID), "queued")
	if _, err := e.s.SetProjectManager(e.ctx, p.ID, idle.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(e.ctx, `INSERT INTO agent_clis (agent_id, cli_name) VALUES ($1, 'gh')`, idle.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(e.ctx, `INSERT INTO agent_credentials (id, agent_id, purpose, cli_name, template_version, secret, field_names, env_names, file_paths, template_source)
		VALUES ($1, $2, 'cli', 'gh', '1', x'00', '[]', '[]', '[]', 'builtin')`, uuid.Must(uuid.NewV7()), idle.ID); err != nil {
		t.Fatal(err)
	}
	_, run, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: busy.ID, RunID: uuid.Must(uuid.NewV7()), Text: "hi", Start: false,
		WhenBusy: "queue", InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}

	// With queued messages: rejected, listing the Agents that are not idle; neither the project nor the Agents change.
	_, err = e.s.DeleteProject(e.ctx, p.ID)
	ae, _ := apperr.As(err)
	details, _ := ae.Details.(store.ProjectNotIdleDetails)
	if ae == nil || ae.Code != apperr.CodeAgentNotIdle || len(details.Agents) != 1 || details.Agents[0].ID != busy.ID {
		t.Fatalf("should be rejected with agent_not_idle, listing the queued Agent: %+v", ae)
	}
	if got, err := e.s.GetProject(e.ctx, p.ID); err != nil || got.ManagerAgentID == nil || got.AgentCount != 2 {
		t.Fatalf("a rejected delete should not change the project: %+v %v", got, err)
	}

	// Deleting after the run ends: Agents are soft-deleted, encrypted credentials are deleted, the Manager is cleared,
	// and the names can be reused.
	if _, err := e.s.Pool.Exec(e.ctx, `UPDATE runs SET status = 'completed', ended_at = $2 WHERE id = $1`, run.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	ids, err := e.s.DeleteProject(e.ctx, p.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("delete failed: %v %v", ids, err)
	}
	var live, creds int
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agents WHERE project_id = $1 AND deleted_at IS NULL`, p.ID).Scan(&live)
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agent_credentials WHERE agent_id = $1`, idle.ID).Scan(&creds)
	var mgr *uuid.UUID
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT manager_agent_id FROM projects WHERE id = $1`, p.ID).Scan(&mgr)
	if live != 0 || creds != 0 || mgr != nil {
		t.Fatalf("after the delete there should be no live Agents, encrypted credentials or Manager: %d %d %v", live, creds, mgr)
	}
	var msgs int
	_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM conversation_messages WHERE agent_id = $1`, busy.ID).Scan(&msgs)
	if msgs == 0 {
		t.Fatal("the conversation history should be kept")
	}
	_, err = e.s.CreateAgent(e.ctx, store.ProjectScope(p.ID), "newcomer", "anthropic", "claude-x", "")
	wantErrCode(t, err, apperr.CodeNotFound)
}

// ---- Global Skills library ----

func TestSkillTemplatesAreGlobal(t *testing.T) {
	e := newProjEnv(t)
	if _, err := e.s.CreateSkillTemplate(e.ctx, content.SkillMeta{Name: "pdf", Description: "d"}, []content.File{{Path: "SKILL.md", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.CreateSkillTemplate(e.ctx, content.SkillMeta{Name: "pdf", Description: "d"}, []content.File{{Path: "SKILL.md", Data: []byte("x")}})
	wantErrCode(t, err, apperr.CodeNameConflict)
	list, err := e.s.ListSkillTemplates(e.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("the global library should have a single pdf: %+v %v", list, err)
	}
}

// ---- Concurrency and edge cases (5.1) ----

// Deleting a project concurrently with creating an Agent in it and sending messages to its Agents: never leaves
// "a non-deleted Agent in a deleted project" or a run in progress in a deleted project.
func TestDeleteProjectConcurrency(t *testing.T) {
	e := newProjEnv(t)
	for round := range 10 {
		p := e.project("concurrent-" + uuid.NewString()[:8])
		a := e.agent(store.ProjectScope(p.ID), "alpha")
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		wg.Go(func() {
			if _, err := e.s.DeleteProject(e.ctx, p.ID); err != nil && !apperr.Is(err, apperr.CodeAgentNotIdle) {
				errs <- err
			}
		})
		for i := range 3 {
			wg.Go(func() {
				_, err := e.s.CreateAgent(e.ctx, store.ProjectScope(p.ID), "beta"+strconv.Itoa(i), "anthropic", "claude-x", "")
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
			t.Fatalf("round %d: unexpected error: %v", round, err)
		}
		var deleted bool
		var live, active int
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id = $1`, p.ID).Scan(&deleted)
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM agents WHERE project_id = $1 AND deleted_at IS NULL`, p.ID).Scan(&live)
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM runs r JOIN agents a ON a.id = r.agent_id
			WHERE a.project_id = $1 AND r.status IN ('queued', 'starting', 'running', 'aborting')`, p.ID).Scan(&active)
		if deleted && (live != 0 || active != 0) {
			t.Fatalf("round %d: the deleted project still has %d live Agents and %d active runs", round, live, active)
		}
		if !deleted && active == 0 {
			t.Fatalf("round %d: a rejected delete should leave a queued run", round)
		}
	}
}

// Setting the Manager concurrently with deleting that Agent: no deadlock, and no Manager left pointing to a deleted
// Agent.
func TestSetManagerVsDeleteAgent(t *testing.T) {
	e := newProjEnv(t)
	for round := range 20 {
		a := e.agent(store.ProjectScope(e.dc.Project.ID), "candidate-"+uuid.NewString()[:8])
		var wg sync.WaitGroup
		var setErr, delErr error
		wg.Go(func() { _, setErr = e.s.SetProjectManager(e.ctx, e.dc.Project.ID, a.ID) })
		wg.Go(func() { delErr = e.s.SoftDeleteAgent(e.ctx, a.ID) })
		wg.Wait()
		if delErr != nil || (setErr != nil && !apperr.Is(setErr, apperr.CodeValidationFailed)) {
			t.Fatalf("round %d: unexpected error: set=%v delete=%v", round, setErr, delErr)
		}
		var mgr *uuid.UUID
		_ = e.s.Pool.QueryRow(e.ctx, `SELECT manager_agent_id FROM projects WHERE id = $1`, e.dc.Project.ID).Scan(&mgr)
		if mgr != nil && *mgr == a.ID {
			t.Fatalf("round %d: the Manager points to a deleted Agent", round)
		}
	}
}
