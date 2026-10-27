package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

func TestProjectCredentialDeletionPurgesWholeOwnerAtomically(t *testing.T) {
	for _, withAgent := range []bool{false, true} {
		t.Run(fmt.Sprint(withAgent), func(t *testing.T) {
			e := projectMigrationFixture(t)
			ctx := context.Background()
			if err := e.s.Migrate(ctx, quietLogger()); err != nil {
				t.Fatal(err)
			}
			var org uuid.UUID
			if err := e.s.Pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, e.project).Scan(&org); err != nil {
				t.Fatal(err)
			}
			p, err := e.s.CreateProject(ctx, org, "待删除")
			if err != nil {
				t.Fatal(err)
			}
			var agent uuid.UUID
			if withAgent {
				a, err := e.s.CreateAgent(ctx, store.ProjectScope(p.ID), "归属Agent", "openai", "fixture", "")
				if err != nil {
					t.Fatal(err)
				}
				agent = a.ID
			}
			c, _ := modelcredential.APIKey("synthetic-delete-key")
			plain, _ := c.Encode()
			defer clear(plain)
			write := store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: plain}
			create := func(project uuid.UUID, purpose, slot, name string) store.ProjectCredentialRecord {
				w := write
				if purpose == "cli" {
					w.AuthMethod = "static"
					w.TemplateSource = "builtin"
					w.Meta = store.CredentialMeta{TemplateVersion: "1"}
				}
				r, err := e.s.CreateProjectCredential(ctx, project, purpose, slot, name, w)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			model := create(p.ID, "model", "openai", "当前与历史")
			old := *model.View.CurrentVersionID
			if _, err = e.s.MutateProjectCredential(ctx, p.ID, model.View.ID, 1, nil, &write, false); err != nil {
				t.Fatal(err)
			}
			// Accounts deleted before account deletion wiped their versions still
			// hold secrets; the project purge must cover them too.
			removed := create(p.ID, "model", "openai", "已删除模型")
			mustExec(t, e.s, `UPDATE project_credential_accounts SET deleted_at=now() WHERE id=$1`, removed.View.ID)
			cli := create(p.ID, "cli", "gh", "已删除CLI")
			cw := write
			cw.AuthMethod = "static"
			cw.TemplateSource = "builtin"
			cw.Meta = store.CredentialMeta{TemplateVersion: "1"}
			cli, err = e.s.MutateProjectCredential(ctx, p.ID, cli.View.ID, 1, nil, &cw, false)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, e.s, `UPDATE project_credential_accounts SET deleted_at=now() WHERE id=$1`, cli.View.ID)
			other := create(e.project, "model", "openai", "别的项目")
			otherCLI := create(e.project, "cli", "gh", "别的项目CLI")
			platform, err := e.s.CreateAgent(ctx, store.PlatformScope(), "私有平台", "openai", "fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			private := e.version(t, platform.ID, "openai", 1)
			name := "待取消登录"
			op, _, err := e.s.NewProjectModelAuthOperation(ctx, p.ID, cm.LoginStart{Provider: "openai-codex", Name: &name}, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.s.SetProjectModelAuthURL(ctx, p.ID, op.ID, "https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"); err != nil {
				t.Fatal(err)
			}
			// An already claimed original generation must not recreate deleted secrets.
			attempt := uuid.New()
			if ok, err := e.s.ClaimModelAuthRefresh(ctx, old, 1, attempt); err != nil || !ok {
				t.Fatal("refresh claim", err)
			}
			if withAgent {
				// Shadowed legacy credentials are a separate project-owned inventory.
				mustExec(t, e.s, `INSERT INTO project_credential_legacy_rows(id,project_id,source_agent_id,secret,field_names,env_names,file_paths,updated_at)VALUES($1,$2,$3,$4,'{}','{}','{}',now())`, uuid.New(), p.ID, agent, plain)
			}
			counts := func() []int {
				out := make([]int, 7)
				queries := []string{
					`SELECT count(*) FROM model_auth_versions v JOIN project_credential_accounts a ON a.id=v.project_account_id WHERE a.project_id=$1 AND octet_length(v.secret)>0`,
					`SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1 AND octet_length(v.secret)>0`,
					`SELECT count(*) FROM project_credential_legacy_rows WHERE project_id=$1 AND octet_length(secret)>0`,
					`SELECT count(*) FROM project_credential_accounts WHERE project_id=$1 AND (current_model_version_id IS NOT NULL OR current_cli_version_id IS NOT NULL)`,
					`SELECT count(*) FROM project_model_auth_operations WHERE project_id=$1 AND (status IN('pending','awaiting_authorization') OR authorization_url IS NOT NULL)`,
					`SELECT count(*) FROM model_auth_versions v JOIN project_credential_accounts a ON a.id=v.project_account_id WHERE a.project_id=$1`,
					`SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1`,
				}
				for i, q := range queries {
					if err := e.s.Pool.QueryRow(ctx, q, p.ID).Scan(&out[i]); err != nil {
						t.Fatal(err)
					}
				}
				return out
			}
			before := counts()
			if before[0] != 3 || before[1] != 2 || before[4] != 1 {
				t.Fatal("incomplete deletion fixture", before)
			}
			// Failure at the last project write rolls back all prior purges and URL changes.
			mustExec(t, e.s, `CREATE FUNCTION reject_project_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'injected delete rollback'; END IF; RETURN NEW; END $$`)
			mustExec(t, e.s, `CREATE TRIGGER reject_project_delete BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION reject_project_delete()`)
			if _, err = e.s.DeleteProject(ctx, p.ID); err == nil {
				t.Fatal("injected failure committed")
			}
			if after := counts(); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatal("rollback lost owner secrets/operations", before, after)
			}
			if _, err = e.s.GetProject(ctx, p.ID); err != nil {
				t.Fatal("rollback deleted owner", err)
			}
			mustExec(t, e.s, `DROP TRIGGER reject_project_delete ON projects`)
			ids, err := e.s.DeleteProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != map[bool]int{false: 0, true: 1}[withAgent] {
				t.Fatal("wrong Agent deletion scope")
			}
			for _, n := range counts() {
				if n != 0 {
					t.Fatal("project deletion retained secrets/pointers/active URL", counts())
				}
			}
			for _, id := range []uuid.UUID{*other.View.CurrentVersionID, private} {
				if _, err = e.s.GetModelAuthVersion(ctx, id); err != nil {
					t.Fatal("deleted another owner", err)
				}
			}
			if _, _, err = e.s.GetProjectCLIVersion(ctx, otherCLI.View.ID, *otherCLI.View.CurrentVersionID); err != nil {
				t.Fatal("deleted other CLI owner", err)
			}
			if ok, err := e.s.CommitModelAuthRefresh(ctx, old, attempt, write.Secret); err != nil || ok {
				t.Fatal("late refresh revived deleted secret", err)
			}
			if err = e.s.FailModelAuthRefresh(ctx, old, attempt, "refresh_uncertain"); err != nil {
				t.Fatal(err)
			}
			terminal, saved, err := e.s.CompleteProjectModelAuthOperation(ctx, p.ID, op.ID, write)
			if err != nil || saved != nil || terminal.Status != "cancelled" || terminal.AuthorizationURL != nil || counts()[5] != 0 {
				t.Fatal("late login saved/revived deleted project", err)
			}
			var tombstones int
			if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM project_credential_accounts WHERE project_id=$1 AND deleted_at IS NOT NULL`, p.ID).Scan(&tombstones); err != nil || tombstones != 3 {
				t.Fatal("lost account history", err, tombstones)
			}
		})
	}
}
