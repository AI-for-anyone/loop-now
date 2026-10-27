package store_test

import (
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

type projectAuthFixture struct {
	project            store.Project
	agents             []*modelAuthEnv
	pending, completed []cm.Operation
	accounts           []uuid.UUID
}

func seedProjectAuth(t *testing.T, base *modelAuthEnv) projectAuthFixture {
	t.Helper()
	dc, err := base.s.EnsureDefaults(base.ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	p, err := base.s.CreateProject(base.ctx, dc.Organization.ID, "删除认证项目")
	if err != nil {
		t.Fatal(err)
	}
	f := projectAuthFixture{project: p}
	for _, name := range []string{"账户一", "账户二"} {
		a, err := base.s.CreateAgent(base.ctx, store.ProjectScope(p.ID), name, "openai-codex", "fixture", "")
		if err != nil {
			t.Fatal(err)
		}
		e := &modelAuthEnv{t, base.s, base.ctx, a}
		account, err := e.s.CreateProjectCredential(e.ctx, p.ID, "model", "openai-codex", name, store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: e.seal("synthetic-account-A")})
		if err != nil {
			t.Fatal(err)
		}
		revision := int64(1)
		first, _, err := e.s.NewProjectModelAuthOperation(e.ctx, p.ID, cm.LoginStart{Provider: "openai-codex", AccountID: &account.View.ID, ExpectedRevision: &revision}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ended, v, err := e.s.CompleteProjectModelAuthOperation(e.ctx, p.ID, first.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: e.seal("synthetic-account-B")})
		if err != nil || v == nil {
			t.Fatal(err)
		}
		if err = e.s.AddAgentCLI(e.ctx, a.ID, "gh"); err != nil {
			t.Fatal(err)
		}
		if _, err = e.s.CreateProjectCredential(e.ctx, p.ID, "cli", "gh", name, store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: e.seal("synthetic-cli")}); err != nil {
			t.Fatal(err)
		}
		revision = 2
		pending, _, err := e.s.NewProjectModelAuthOperation(e.ctx, p.ID, cm.LoginStart{Provider: "openai-codex", AccountID: &account.View.ID, ExpectedRevision: &revision}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		pending, err = e.s.SetProjectModelAuthURL(e.ctx, p.ID, pending.ID, "https://example.invalid/offline-authorize")
		if err != nil {
			t.Fatal(err)
		}
		f.accounts = append(f.accounts, account.View.ID)
		f.agents = append(f.agents, e)
		f.pending = append(f.pending, pending)
		f.completed = append(f.completed, ended)
	}
	if _, err = base.s.SetProjectManager(base.ctx, p.ID, f.agents[0].agent.ID); err != nil {
		t.Fatal(err)
	}
	return f
}
func assertProjectAuthRetained(t *testing.T, f projectAuthFixture) {
	t.Helper()
	for i, e := range f.agents {
		if projectVersionCount(t, e, f.accounts[i]) != 2 {
			t.Fatal("refused/rolled-back deletion changed versions")
		}
		if r, err := e.s.GetProjectCredential(e.ctx, f.project.ID, f.accounts[i]); err != nil || r.View.CurrentVersionID == nil {
			t.Fatal("pointer changed", err)
		}
		op, err := e.s.GetProjectModelAuthOperation(e.ctx, f.project.ID, f.pending[i].ID)
		if err != nil || op.Status != "awaiting_authorization" || op.AuthorizationURL == nil || op.CompletedAt != nil {
			t.Fatal("operation changed before commit", err)
		}
		var cliVersions int
		if err := e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1`, f.project.ID).Scan(&cliVersions); err != nil || cliVersions != 2 {
			t.Fatal("shared CLI versions changed", err)
		}
		if _, err = e.s.GetAgent(e.ctx, e.agent.ID); err != nil {
			t.Fatal("Agent was soft-deleted", err)
		}
	}
}
func TestProjectAuthPurge(t *testing.T) {
	t.Run("multi-agent-all-account-versions-and-late-complete", func(t *testing.T) {
		base := newModelAuthEnv(t)
		outside := base.key("outside-account-retained")
		f := seedProjectAuth(t, base)
		ids, err := base.s.DeleteProject(base.ctx, f.project.ID)
		if err != nil || len(ids) != 2 {
			t.Fatal(err)
		}
		for i, e := range f.agents {
			if projectVersionCount(t, e, f.accounts[i]) != 0 {
				t.Fatal("project retained account credential")
			}
			var hasPointer bool
			if err := e.s.Pool.QueryRow(e.ctx, `SELECT current_model_version_id IS NOT NULL FROM project_credential_accounts WHERE id=$1`, f.accounts[i]).Scan(&hasPointer); err != nil || hasPointer {
				t.Fatal("project retained pointer", err)
			}
			credentials, err := e.s.ListCredentials(e.ctx, e.agent.ID)
			if err != nil || len(credentials) != 0 {
				t.Fatal("project retained legacy credentials", err)
			}
			op := f.pending[i]
			err = e.s.Pool.QueryRow(e.ctx, `SELECT status,authorization_url,error_code,completed_at FROM project_model_auth_operations WHERE id=$1`, op.ID).Scan(&op.Status, &op.AuthorizationURL, &op.ErrorCode, &op.CompletedAt)
			if err != nil || op.Status != "cancelled" || op.AuthorizationURL != nil || op.ErrorCode != nil || op.CompletedAt == nil {
				t.Fatal("project retained active authorization", err)
			}
			late, v, err := e.s.CompleteProjectModelAuthOperation(e.ctx, f.project.ID, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: e.seal("late")})
			if err != nil || v != nil || late.Status != "cancelled" || projectVersionCount(t, e, f.accounts[i]) != 0 {
				t.Fatal("late completion saved/resurrected deleted account", err)
			}
			ended := f.completed[i]
			err = e.s.Pool.QueryRow(e.ctx, `SELECT status FROM project_model_auth_operations WHERE id=$1`, ended.ID).Scan(&ended.Status)
			if err != nil || ended.Status != "succeeded" {
				t.Fatal("terminal operation history changed", err)
			}
		}
		base.current(outside.ID)
		if base.countVersions() != 1 {
			t.Fatal("outside Agent account purged")
		}
	})
	t.Run("last-project-write-failure-rolls-back-every-purge", func(t *testing.T) {
		base := newModelAuthEnv(t)
		f := seedProjectAuth(t, base)
		for _, q := range []string{`CREATE FUNCTION reject_auth_project_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic project deletion failure'; END IF; RETURN NEW; END $$`, `CREATE TRIGGER reject_auth_project_delete BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION reject_auth_project_delete()`} {
			if _, err := base.s.Pool.Exec(base.ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		ids, err := base.s.DeleteProject(base.ctx, f.project.ID)
		if err == nil || ids != nil {
			t.Fatal("failed transaction returned cleanup IDs")
		}
		assertProjectAuthRetained(t, f)
		p, err := base.s.GetProject(base.ctx, f.project.ID)
		if err != nil || p.ManagerAgentID == nil || *p.ManagerAgentID != f.agents[0].agent.ID {
			t.Fatal("manager/project update partially committed", err)
		}
	})
	t.Run("unfinished-task-refusal-preserves-authorization", func(t *testing.T) {
		base := newModelAuthEnv(t)
		f := seedProjectAuth(t, base)
		if _, err := base.s.Pool.Exec(base.ctx, `INSERT INTO task_records(id,project_id,status,document,version)VALUES($1,$2,'preparing','{"view":{"task":{"status":"preparing","version":1}}}',1)`, uuid.New(), f.project.ID); err != nil {
			t.Fatal(err)
		}
		_, err := base.s.DeleteProject(base.ctx, f.project.ID)
		if !apperr.Is(err, apperr.CodeTaskNotIdle) {
			t.Fatal("unfinished task no longer protects project", err)
		}
		assertProjectAuthRetained(t, f)
	})
	t.Run("busy-agent-refusal-preserves-authorization", func(t *testing.T) {
		base := newModelAuthEnv(t)
		f := seedProjectAuth(t, base)
		if _, _, err := base.s.InsertMessageAndRun(base.ctx, store.NewRun{AgentID: f.agents[0].agent.ID, RunID: uuid.New(), Text: "queued", Start: false, WhenBusy: "queue", InstanceID: uuid.New()}); err != nil {
			t.Fatal(err)
		}
		_, err := base.s.DeleteProject(base.ctx, f.project.ID)
		if !apperr.Is(err, apperr.CodeAgentNotIdle) {
			t.Fatal("busy Agent no longer protects project", err)
		}
		assertProjectAuthRetained(t, f)
	})
}

func projectVersionCount(t *testing.T, e *modelAuthEnv, account uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, account).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
