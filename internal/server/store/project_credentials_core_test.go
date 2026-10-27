package store_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

func projectCoreWrite(t *testing.T, e projectMigrationEnv, key string) store.ProjectCredentialWrite {
	t.Helper()
	c, err := modelcredential.APIKey(key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: plain}
}

func TestProjectCredentialCASAndPinnedIdentity(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	a := e.agent(t, "使用者", "openai")
	b := e.agent(t, "共同使用者", "openai")
	write := projectCoreWrite(t, e, "synthetic-A")
	acct, err := e.s.CreateProjectCredential(ctx, e.project, "model", "openai", "同标签", write)
	if err != nil {
		t.Fatal(err)
	}
	original := *acct.View.CurrentVersionID
	// Valid terminal v1 history keeps the old source configuration;
	// current-impact projection must ignore it after that Agent is deleted.
	e.oldTask(t, a, 1, true, true, nil, map[string]string{"OPENAI_API_KEY": "synthetic-A"})
	snapshot, err := e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	rename := "新标签"
	named, err := e.s.MutateProjectCredential(ctx, e.project, acct.View.ID, 1, &rename, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || snapshot.CredentialIdentity != after.CredentialIdentity {
		t.Fatal("rename changed effective identity", err)
	}
	var versionsBefore, versionsAfter int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions`).Scan(&versionsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.MutateProjectCredential(ctx, e.project, acct.View.ID, 1, nil, &write, false); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("stale account CAS accepted", err)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions`).Scan(&versionsAfter); err != nil || versionsAfter != versionsBefore {
		t.Fatal("stale account CAS wrote a version", err)
	}
	other, err := e.s.CreateProjectCredential(ctx, e.project, "model", "anthropic", "不相关", write)
	if err != nil {
		t.Fatal(err)
	}
	after, err = e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || snapshot.CredentialIdentity != after.CredentialIdentity {
		t.Fatal("unrelated account changed identity", err)
	}
	mustExec(t, e.s, `UPDATE model_auth_versions SET generation=generation+1 WHERE id=$1`, original)
	after, err = e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || snapshot.CredentialIdentity != after.CredentialIdentity {
		t.Fatal("generation changed effective identity", err)
	}
	next := projectCoreWrite(t, e, "synthetic-B")
	replaced, err := e.s.MutateProjectCredential(ctx, e.project, acct.View.ID, named.View.Revision, nil, &next, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err = e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || after.ModelAuth.VersionID == original || after.CredentialIdentity == snapshot.CredentialIdentity {
		t.Fatal("replacement identity not observed", err)
	}
	if snapshot.ModelAuth.VersionID != original || !e.s.ValidateModelAuthBinding(ctx, *snapshot.ModelAuth) {
		t.Fatal("old captured binding changed")
	}
	// Two concurrent single-item updates with one expected version admit one commit.
	state, err := e.s.GetCredentialSelections(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var win, stale atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := e.s.PutCredentialSelection(ctx, a, cm.SelectionPut{Purpose: "model", Provider: replaced.View.Provider, AccountID: &replaced.View.ID, ExpectedConfigVersion: state.ConfigVersion})
			if err == nil {
				win.Add(1)
			} else if apperr.Is(err, apperr.CodePreconditionFailed) {
				stale.Add(1)
			} else {
				t.Errorf("unexpected concurrent choice: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if win.Load() != 1 || stale.Load() != 1 {
		t.Fatal("CAS did not serialize choice")
	}
	// A second consumer may use the same immutable grant; cross-project cannot.
	bound := snapshot.ModelAuth.Clone()
	bound.AgentID = b
	if !e.s.ValidateModelAuthBinding(ctx, bound) {
		t.Fatal("shared consumer rejected")
	}
	var org uuid.UUID
	mustQuery := e.s.Pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, e.project)
	if err = mustQuery.Scan(&org); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.CreateProject(ctx, org, "别的归属")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := e.s.CreateAgent(ctx, store.ProjectScope(p.ID), "外部消费者", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	bound.AgentID = outsider.ID
	if e.s.ValidateModelAuthBinding(ctx, bound) {
		t.Fatal("cross-project binding accepted")
	}
	bad := snapshot.ModelAuth.Clone()
	bad.AccountID = &other.View.ID
	if e.s.ValidateModelAuthBinding(ctx, bad) {
		t.Fatal("wrong account/provider accepted")
	}
	// Deleting one source consumer retains shared versions for the live consumer.
	if err = e.s.SoftDeleteAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	bound = snapshot.ModelAuth.Clone()
	bound.AgentID = b
	if !e.s.ValidateModelAuthBinding(ctx, bound) {
		t.Fatal("source Agent delete erased shared grant")
	}
	if _, err = e.s.GetModelAuthVersion(ctx, original); err != nil {
		t.Fatal("shared history GC", err)
	}
	// Deleting the account wipes every version no unfinished task uses; an
	// earlier non-task capture no longer resolves.
	if _, err = e.s.MutateProjectCredential(ctx, e.project, acct.View.ID, replaced.View.Revision, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if e.s.ValidateModelAuthBinding(ctx, bound) {
		t.Fatal("deleted account's unused version still resolves")
	}
	if _, err = e.s.GetModelAuthVersion(ctx, original); err == nil {
		t.Fatal("deleted account's unused version kept its secret")
	}
	impact, err := e.s.ProjectCredentialImpact(ctx, e.project, "model", "openai", &acct.View.ID)
	if err != nil || len(impact.Agents) != 0 {
		t.Fatal("historical task or deleted Agent counted as current consumer", err)
	}
}

func TestProjectCredentialSelectionRejectsWithoutMutation(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	a := e.agent(t, "选择者", "openai")
	w := projectCoreWrite(t, e, "fixture")
	one, err := e.s.CreateProjectCredential(ctx, e.project, "model", "openai", "重名", w)
	if err != nil {
		t.Fatal(err)
	}
	two, err := e.s.CreateProjectCredential(ctx, e.project, "model", "openai", "重名", w)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.s, `UPDATE model_auth_versions SET refresh_state='unavailable' WHERE id=$1`, *two.View.CurrentVersionID)
	state, err := e.s.GetCredentialSelections(ctx, a)
	if err != nil || len(state.Selections[0].Candidates) != 2 || state.Selections[0].Status != "selection_required" {
		t.Fatal("unavailable candidate removed from cardinality", err)
	}
	cli := "gh"
	if _, err = e.s.PutCredentialSelection(ctx, a, cm.SelectionPut{Purpose: "cli", CLI: &cli, AccountID: &one.View.ID, ExpectedConfigVersion: 1}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatal("permission bypass", err)
	}
	provider := "anthropic"
	if _, err = e.s.PutCredentialSelection(ctx, a, cm.SelectionPut{Purpose: "model", Provider: &provider, AccountID: &one.View.ID, ExpectedConfigVersion: 1}); !apperr.Is(err, apperr.CodeCredentialScopeMismatch) {
		t.Fatal("provider mismatch", err)
	}
	agent, err := e.s.GetAgent(ctx, a)
	if err != nil || agent.ConfigVersion != 1 {
		t.Fatal("rejected choice changed config", err)
	}
	snapshot, err := e.s.CaptureExecutionIdentity(ctx, a)
	if !apperr.Is(err, apperr.CodeCredentialSelectionRequired) || snapshot.ModelAuth != nil {
		t.Fatal("ambiguous capture used fallback", err)
	}
	var choices int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_credential_selections WHERE agent_id=$1`, a).Scan(&choices); err != nil || choices != 0 {
		t.Fatal("rejected choice wrote", err)
	}
	t.Log(fmt.Sprintf("candidates=%d rejectedChoiceRows=%d", len(state.Selections[0].Candidates), choices))
}
