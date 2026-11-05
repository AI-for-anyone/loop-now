package agentconfig_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/store"
)

type pauseExecutionRead struct {
	once             sync.Once
	entered, release chan struct{}
}

func (p *pauseExecutionRead) hook(ctx context.Context, q string) {
	if strings.Contains(q, "FROM agent_sessions") {
		p.once.Do(func() {
			close(p.entered)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
}

func TestExecutionSnapshotKeepsCapturedConfigAndGrantAcrossSameProviderAccountChange(t *testing.T) {
	e := newEnv(t)
	e.scope = store.PlatformScope()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	a := e.agent(t)
	coordinator := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coordinator, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	e.svc.ModelAuth = auth
	oldMeta, err := auth.PutKey(ctx, a.ID, modelauth.KeyInput{Provider: "anthropic", Key: "synthetic-account-A"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := e.st.GetAgent(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	trace := &pauseExecutionRead{entered: make(chan struct{}), release: make(chan struct{})}
	e.st.Pool.SetQueryHook(trace.hook)
	defer e.st.Pool.SetQueryHook(nil)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(trace.release) }) }
	defer release()
	result := make(chan agentconfig.Snapshot, 1)
	failures := make(chan error, 1)
	go func() { snapshot, err := e.svc.ExecutionSnapshot(ctx, a.ID); result <- snapshot; failures <- err }()
	select {
	case <-trace.entered:
	case <-ctx.Done():
		t.Fatal("execution read not reached")
	}
	// The first snapshot has read its configuration. Commit a new account under
	// the same provider before it resolves the credential; no sleeps or races.
	newMeta, err := auth.PutKey(ctx, a.ID, modelauth.KeyInput{Provider: "anthropic", Key: "synthetic-account-B"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := "new prompt for B"
	if _, err := e.st.UpdateAgent(ctx, a.ID, store.AgentPatch{Prompt: &prompt, ExpectedPromptVersion: func() *int64 { v := int64(1); return &v }()}); err != nil {
		t.Fatal(err)
	}
	release()
	snapshot := <-result
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigVersion != before.ConfigVersion || snapshot.Prompt != before.Prompt || snapshot.ModelBinding == nil || snapshot.ModelBinding.VersionID != *oldMeta.VersionID {
		t.Fatal("old configuration mixed with new authorization")
	}
	encoded, err := snapshot.ModelCredential.AuthFile("anthropic")
	if err != nil || !strings.Contains(string(encoded), "synthetic-account-A") {
		t.Fatal("captured grant resolved to another account", err)
	}
	next, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || next.ModelBinding == nil || next.ModelBinding.VersionID != *newMeta.VersionID || next.Prompt != prompt || next.ConfigVersion <= snapshot.ConfigVersion {
		t.Fatal("next dispatch failed to observe B", err)
	}
	if next.ModelBinding.AgentID != a.ID || next.ModelBinding.VersionID == uuid.Nil {
		t.Fatal("invalid binding")
	}
}
