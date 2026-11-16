package agentconfig_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/agentconfig"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/store"
)

type pausePromptCapture struct {
	once             sync.Once
	entered, release chan struct{}
}

// hook pauses before the capture reads the Agent row: by then the project's prompt has been read, and the write
// transaction the capture runs in has not finished.
func (p *pausePromptCapture) hook(ctx context.Context, q string) {
	if strings.Contains(q, "agents.prompt_version") && strings.Contains(q, "FROM agents WHERE id=$1") {
		p.once.Do(func() {
			close(p.entered)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
}

func TestPromptCaptureProjectLockAndAccountCoherence(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := e.agent(t)
	project := *a.ProjectID
	if _, err := e.st.PutProjectPrompt(ctx, project, "project-A", 1); err != nil {
		t.Fatal(err)
	}
	coord := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coord, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	e.svc.ModelAuth = auth
	projectAuth := modelauth.NewProjectService(auth, e.svc.Templates, 4096)
	provider := "anthropic"
	account, err := projectAuth.Create(ctx, project, cm.Create{Purpose: "model", Provider: &provider, Name: "capture-A", Credential: cm.CredentialInput{Type: "api_key", Key: "fixture-A"}})
	if err != nil {
		t.Fatal(err)
	}
	trace := &pausePromptCapture{entered: make(chan struct{}), release: make(chan struct{})}
	e.st.Pool.SetQueryHook(trace.hook)
	defer e.st.Pool.SetQueryHook(nil)
	var once sync.Once
	release := func() { once.Do(func() { close(trace.release) }) }
	defer release()
	result := make(chan agentconfig.Snapshot, 1)
	failure := make(chan error, 1)
	go func() { s, err := e.svc.ExecutionSnapshot(ctx, a.ID); result <- s; failure <- err }()
	select {
	case <-trace.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Project read, Agent not yet read, the capture's write transaction still open: a real project UPDATE has to wait
	// for it to finish.
	written := make(chan error, 1)
	go func() {
		_, err := e.st.Pool.Exec(ctx, `UPDATE projects SET prompt='project-B',prompt_version=prompt_version+1 WHERE id=$1`, project)
		written <- err
	}()
	select {
	case err := <-written:
		t.Fatal("project write did not wait for the capture", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	snap := <-result
	if err = <-failure; err != nil {
		t.Fatal(err)
	}
	if snap.PromptLayers.ProjectPrompt != "project-A" || snap.PromptLayers.ProjectVersion != 2 || snap.PromptLayers.AgentVersion != 1 || snap.ModelBinding == nil || snap.ModelBinding.VersionID != *account.CurrentVersionID {
		t.Fatal("capture mixed blocked project update")
	}
	if err = <-written; err != nil {
		t.Fatal(err)
	}
	text := "agent-B"
	v := int64(1)
	if _, err = e.st.UpdateAgent(ctx, a.ID, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: &v}); err != nil {
		t.Fatal(err)
	}
	b, err := projectAuth.Replace(ctx, project, account.ID, cm.Replace{ExpectedRevision: account.Revision, Credential: cm.CredentialInput{Type: "api_key", Key: "fixture-B"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || next.PromptLayers.ProjectPrompt != "project-B" || next.PromptLayers.ProjectVersion != 3 || next.ModelBinding.VersionID != *b.CurrentVersionID || next.PromptLayers.AgentPrompt != text {
		t.Fatal("next capture missed project edit", err)
	}
}
