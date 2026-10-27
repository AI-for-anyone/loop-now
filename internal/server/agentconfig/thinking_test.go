package agentconfig_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/auth"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
)

func TestThinkingNormalNextRoundAndTaskFrozenCapture(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Models = modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "anthropic", AuthMethods: []string{"api_key"}, Models: []modelcatalog.Model{{ID: "claude-x", Reasoning: true, ThinkingLevels: []string{"low", "medium", "high"}, DefaultThinkingLevel: "medium"}}}})
	high := "high"
	a, err := e.svc.CreateAgent(ctx, e.scope, "thinking", "anthropic", "claude-x", "", &high)
	if err != nil {
		t.Fatal(err)
	}
	co := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	ma := modelauth.NewService(e.st, nil, co, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer ma.Close(ctx)
	e.svc.ModelAuth = ma
	project := *a.ProjectID
	provider := "anthropic"
	ps := modelauth.NewProjectService(ma, e.svc.Templates, 4096)
	if _, err = ps.Create(ctx, project, cm.Create{Purpose: "model", Provider: &provider, Name: "synthetic", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-only"}}); err != nil {
		t.Fatal(err)
	}
	before, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || before.ThinkingLevel == nil || *before.ThinkingLevel != "high" {
		t.Fatal("normal capture", err)
	}
	if _, err = e.st.SetProjectManager(ctx, project, a.ID); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(e.st, e.svc, nil, tasks.Options{MaxSteps: 10, MaxEdges: 10, MaxVisits: 10, MaxRequest: 1 << 20}, e.svc.Logger)
	raw, err := svc.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: a.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "thinking frozen", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: a.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	var detail taskmodel.Detail
	if err = json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	instances, err := e.st.TaskInstances(ctx, detail.Task.ID)
	if err != nil || len(instances) != 1 {
		t.Fatal("task capture", err)
	}
	inst := instances[0]
	original := bytes.Clone(inst.Snapshot)
	snap, err := taskruntime.Decode(original)
	if err != nil || snap.Configuration().ThinkingLevel == nil || *snap.Configuration().ThinkingLevel != "high" {
		t.Fatal("task did not freeze actual level", err)
	}
	if _, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{ThinkingLevelSet: true, ThinkingLevel: nil}); err != nil {
		t.Fatal(err)
	}
	next, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || next.ThinkingLevel == nil || *next.ThinkingLevel != "medium" || *before.ThinkingLevel != "high" {
		t.Fatal("normal next round/default reset", err)
	}
	after, err := e.st.TaskInstance(ctx, inst.ID)
	if err != nil || !bytes.Equal(after.Snapshot, original) {
		t.Fatal("rewrote old task snapshot", err)
	}
	// The ordinary task Source consumes the captured concrete level, never
	// the Agent's current choice. This TEST instance has no real session/provider.
	if _, err = e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, inst.ID); err != nil {
		t.Fatal(err)
	}
	source := &tasks.Source{Store: e.st, ModelAuth: co}
	frozen, err := source.ExecutionSnapshot(ctx, inst.ID)
	if err != nil || frozen.ThinkingLevel == nil || *frozen.ThinkingLevel != "high" {
		t.Fatal("task Source read current level", err)
	}
	// A later task under null/default freezes the resolved model default,
	// rather than leaving a nil that could resume the Agent's old high level.
	raw, err = svc.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: a.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "default frozen", Request: map[string]any{}, Workflow: detail.Workflow})
	if err != nil {
		t.Fatal(err)
	}
	var later taskmodel.Detail
	if err = json.Unmarshal(raw, &later); err != nil {
		t.Fatal(err)
	}
	newInstances, err := e.st.TaskInstances(ctx, later.Task.ID)
	if err != nil || len(newInstances) != 1 {
		t.Fatal("default task", err)
	}
	resolved, err := taskruntime.Decode(newInstances[0].Snapshot)
	if err != nil || resolved.Configuration().ThinkingLevel == nil || *resolved.Configuration().ThinkingLevel != "medium" {
		t.Fatal("default task did not freeze concrete medium", err)
	}
}
