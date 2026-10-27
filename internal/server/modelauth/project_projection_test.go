package modelauth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
)

func TestProjectCredentialImpactAndCommitNotification(t *testing.T) {
	st, _ := authFixture(t)
	ctx := context.Background()
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "自动使用者", "anthropic", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "已清除历史", "anthropic", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, nil, nil, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, svc)
	p := NewProjectService(svc, templates, 4096)
	var notified []uuid.UUID
	svc.Changed = func(id uuid.UUID) {
		// A notification emitted before COMMIT cannot read through its held project
		// UPDATE lock. A bounded read proves the callback sees committed state.
		read, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		defer stop()
		list, err := p.List(read, dc.Project.ID, "model", "anthropic")
		if err != nil || len(list.Accounts) == 0 {
			t.Errorf("notification before committed account visible: %v", err)
		}
		notified = append(notified, id)
	}
	provider := "anthropic"
	account, err := p.Create(ctx, dc.Project.ID, cm.Create{Purpose: "model", Provider: &provider, Name: "共享A", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-project-A"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(notified) != 2 {
		t.Fatal("project commit did not notify its current Agents")
	}
	for _, id := range notified {
		if id != a.ID && id != b.ID {
			t.Fatal("notified unrelated scope")
		}
	}
	impact, err := p.Impact(ctx, dc.Project.ID, "model", provider, &account.ID)
	if err != nil || len(impact.Agents) != 2 {
		t.Fatal("unique automatic impact", err)
	}
	for _, v := range impact.Agents {
		if v.Mode != "automatic" {
			t.Fatal("wrong current automatic projection")
		}
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO agent_model_legacy_cleared(agent_id,project_id)VALUES($1,$2)`, b.ID, dc.Project.ID); err != nil {
		t.Fatal(err)
	}
	impact, err = p.Impact(ctx, dc.Project.ID, "model", provider, &account.ID)
	if err != nil || len(impact.Agents) != 1 || impact.Agents[0].AgentID != a.ID {
		t.Fatal("legacy_cleared counted as automatic", err)
	}
	state, err := st.GetCredentialSelections(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutCredentialSelection(ctx, b.ID, cm.SelectionPut{Purpose: "model", Provider: &provider, AccountID: &account.ID, ExpectedConfigVersion: state.ConfigVersion}); err != nil {
		t.Fatal(err)
	}
	impact, err = p.Impact(ctx, dc.Project.ID, "model", provider, &account.ID)
	if err != nil || len(impact.Agents) != 2 {
		t.Fatal("explicit impact missing", err)
	}
	if _, err = p.Create(ctx, dc.Project.ID, cm.Create{Purpose: "model", Provider: &provider, Name: "共享B", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-project-B"}}); err != nil {
		t.Fatal(err)
	}
	impact, err = p.Impact(ctx, dc.Project.ID, "model", provider, nil)
	if err != nil || len(impact.Agents) != 1 || impact.Agents[0].AgentID != a.ID || impact.Agents[0].Status != "selection_required" {
		t.Fatal("second-account current impact wrong", err)
	}
	if err = st.AddAgentCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	cli := "gh"
	shared, err := p.Create(ctx, dc.Project.ID, cm.Create{Purpose: "cli", CLI: &cli, Name: "共享gh", Credential: cm.CredentialInput{Type: "cli", Fields: map[string]string{"token": "synthetic-gh"}}})
	if err != nil {
		t.Fatal(err)
	}
	impact, err = p.Impact(ctx, dc.Project.ID, "cli", cli, &shared.ID)
	if err != nil || len(impact.Agents) != 1 || impact.Agents[0].AgentID != a.ID {
		t.Fatal("CLI permission excluded from impact", err)
	}
	if err = st.RemoveAgentCLI(ctx, a.ID, cli); err != nil {
		t.Fatal(err)
	}
	impact, err = p.Impact(ctx, dc.Project.ID, "cli", cli, &shared.ID)
	if err != nil || len(impact.Agents) != 0 {
		t.Fatal("removed CLI permission still in impact", err)
	}
	before := len(notified)
	if _, err = p.Rename(ctx, dc.Project.ID, account.ID, cm.Rename{Name: "stale", ExpectedRevision: 999}); err == nil || len(notified) != before {
		t.Fatal("failed transaction emitted change")
	}
}
