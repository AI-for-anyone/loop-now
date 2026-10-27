package agentconfig_test

import (
	"context"
	"errors"
	"testing"

	"loop-now/internal/server/agentconfig"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
)

func TestProjectCredentialExecutionFailureAndTemplateSource(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	coordinator := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coordinator, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	project := modelauth.NewProjectService(auth, e.svc.Templates, 4096)
	e.svc.ModelAuth = auth
	provider := "anthropic"
	first, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "model", Provider: &provider, Name: "A", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-A"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "model", Provider: &provider, Name: "B", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-B"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.ExecutionSnapshot(ctx, a.ID); !errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		t.Fatal("ambiguous account escaped as internal failure", err)
	}
	if _, err = project.Delete(ctx, *a.ProjectID, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.ExecutionSnapshot(ctx, a.ID); err != nil {
		t.Fatal("unique account not restored", err)
	}
	cli := "gh"
	if _, err = e.svc.AddCLI(ctx, a.ID, cli); err != nil {
		t.Fatal(err)
	}
	account, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "cli", CLI: &cli, Name: "gh", Credential: cm.CredentialInput{Type: "cli", Fields: map[string]string{"token": "synthetic-gh"}}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.CLIPayloads[cli].Env["GH_TOKEN"] != "synthetic-gh" {
		t.Fatal("shared CLI not injected", err)
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE project_cli_versions SET template_source='plugin:foreign' WHERE id=$1`, *account.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.ExecutionSnapshot(ctx, a.ID); !errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		t.Fatal("foreign CLI source injected", err)
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE project_cli_versions SET template_source='builtin' WHERE id=$1`, *account.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='unavailable' WHERE id=$1`, *first.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.ExecutionSnapshot(ctx, a.ID); !errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		t.Fatal("unavailable grant escaped as internal failure", err)
	}
}
