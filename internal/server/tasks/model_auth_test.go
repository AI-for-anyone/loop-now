package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

func TestConvertedV1AndV2TaskCredentialsNeverReadCurrentAccount(t *testing.T) {
	svc, taskID, p := fixture(t, false)
	ctx := context.Background()
	provider, model := "anthropic", "claude-x"
	agent, err := svc.Store.UpdateAgent(ctx, p.ID, store.AgentPatch{ModelProvider: &provider, ModelID: &model})
	if err != nil {
		t.Fatal(err)
	}
	catalog := modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{{ID: provider, AuthMethods: []string{"api_key"}}})
	coordinator := modelauth.NewCoordinator(svc.Store, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(svc.Store, nil, coordinator, catalog, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	keyWrite := func(value string) store.ProjectCredentialWrite {
		c, e := modelcredential.APIKey(value)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := c.Encode()
		if e != nil {
			t.Fatal(e)
		}
		return store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: plain}
	}
	a, err := svc.Store.CreateProjectCredential(ctx, *agent.ProjectID, "model", provider, "frozen-A", keyWrite("frozen-key-A"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.Pool.Exec(ctx, `INSERT INTO project_model_legacy_bindings(version_id,source_agent_id,provider,account_id,project_id)VALUES($1,$2,$3,$4,$5)`, *a.View.CurrentVersionID, p.ID, provider, a.View.ID, *agent.ProjectID); err != nil {
		t.Fatal(err)
	}
	scope, err := taskruntime.TaskScope(p.ID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	input := taskruntime.CaptureInput{Configuration: taskruntime.Configuration{AgentID: p.ID, ConfigVersion: agent.ConfigVersion, Provider: provider, ModelID: model, CLIs: []string{}, Skills: []taskruntime.Skill{}}, Credentials: taskruntime.Credentials{ModelAuth: &modelcredential.Binding{VersionID: *a.View.CurrentVersionID, AgentID: p.ID, Provider: provider}}}
	// Build a converted v2 envelope (sourceVersion 2), rather than a new schema capture.
	v2id := uuid.New()
	v2data, err := json.Marshal(map[string]any{"schemaVersion": 8, "sourceVersion": 2, "snapshotId": v2id, "scope": map[string]any{"agentId": scope.AgentID(), "taskId": scope.TaskID()}, "configuration": input.Configuration, "credentials": map[string]any{"modelAuth": input.Credentials.ModelAuth, "model": nil, "cli": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	// Construct a converted v1 envelope (sourceVersion 1). This fixture
	// deliberately contains all three old auth fields, not a current ref.
	oldEnv := map[string]string{"ANTHROPIC_AUTH_TOKEN": "old-v1-bearer-A", "ANTHROPIC_OAUTH_TOKEN": "old-v1-oauth-A", "ANTHROPIC_API_KEY": "old-v1-key-A"}
	snapshotID := uuid.New()
	v1data, err := json.Marshal(map[string]any{"schemaVersion": 8, "sourceVersion": 1, "snapshotId": snapshotID, "scope": map[string]any{"agentId": scope.AgentID(), "taskId": scope.TaskID()}, "configuration": input.Configuration, "credentials": map[string]any{"model": oldEnv, "cli": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	w := keyWrite("current-key-B")
	b, err := svc.Store.MutateProjectCredential(ctx, *agent.ProjectID, a.View.ID, 1, nil, &w, false)
	if err != nil {
		t.Fatal(err)
	}
	source := &Source{Store: svc.Store, ModelAuth: coordinator}
	for _, test := range []struct {
		name string
		data []byte
		want string
		ref  bool
	}{{"v1", v1data, "old-v1-bearer-A", false}, {"v2", v2data, "frozen-key-A", true}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := svc.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, p.RuntimeID, test.data); err != nil {
				t.Fatal(err)
			}
			captured, err := source.ExecutionSnapshot(ctx, p.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := captured.ModelCredential.AuthFile(provider)
			if err != nil || !strings.Contains(string(encoded), test.want) || strings.Contains(string(encoded), "current-key-B") {
				t.Fatal("old task switched account", err)
			}
			if test.ref && (captured.ModelBinding == nil || captured.ModelBinding.VersionID != *a.View.CurrentVersionID) {
				t.Fatal("v2 grant was rebound")
			}
			if !test.ref && (captured.ModelBinding != nil || len(captured.ModelEnv) != 3 || !captured.ModelCredential.NeedsLegacyAnthropic()) {
				t.Fatal("v1 input was silently rewritten")
			}
		})
	}
	pointer, err := svc.Store.GetProjectCredential(ctx, *agent.ProjectID, a.View.ID)
	if err != nil || *pointer.View.CurrentVersionID != *b.View.CurrentVersionID {
		t.Fatal("task changed current pointer", err)
	}
	// Make A unavailable. It must fail instead of falling back to current B.
	if _, err := svc.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, p.RuntimeID, v2data); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='unavailable' WHERE id=$1`, *a.View.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecutionSnapshot(ctx, p.RuntimeID); err == nil {
		t.Fatal("unavailable frozen grant fell back to B")
	}
}
