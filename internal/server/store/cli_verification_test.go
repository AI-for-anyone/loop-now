package store_test

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func verificationMeta() store.VerificationMetadata {
	return store.VerificationMetadata{TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: strings.Repeat("a", 64), AdapterRevision: "fixture", ProbeKind: "github_user"}
}
func TestCLIVerificationOperationCASAndRecovery(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	account, version := verificationSeed(t, e, e.project, "gh", "static")
	in := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: version}
	var wg sync.WaitGroup
	var created atomic.Int32
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, new, e := e.s.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute))
			if e != nil || op.ID != in.OperationID {
				t.Errorf("begin %v", e)
			}
			if new {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("replay dispatched twice")
	}
	wrong := in
	wrong.ExpectedVersionID = uuid.New()
	if _, _, err := e.s.BeginCLIVerification(ctx, e.project, account, wrong, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); !apperr.Is(err, apperr.CodeVerificationOperationConflict) {
		t.Fatal("changed idempotent target", err)
	}
	other := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: version}
	if _, _, err := e.s.BeginCLIVerification(ctx, e.project, account, other, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); !apperr.Is(err, apperr.CodeVerificationInProgress) {
		t.Fatal("active account duplicate", err)
	}
	wrong.OperationID = uuid.New()
	if _, _, err := e.s.BeginCLIVerification(ctx, e.project, account, wrong, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("wrong version didn't fail CAS", err)
	}
	op, claimed, err := e.s.ClaimCLIVerification(ctx, e.project, account, in.OperationID)
	if err != nil || !claimed || op.Status != "running" {
		t.Fatal("claim", err)
	}
	if _, claimed, err = e.s.ClaimCLIVerification(ctx, e.project, account, in.OperationID); err != nil || claimed {
		t.Fatal("claimed twice", err)
	}
	tool := "2.100.0"
	if err = e.s.CLIVerificationPhase(ctx, e.project, account, in.OperationID, "probing", &tool); err != nil {
		t.Fatal(err)
	}
	rename := "new safe label"
	if _, err = e.s.MutateProjectCredential(ctx, e.project, account, 1, &rename, nil, false); err != nil {
		t.Fatal(err)
	}
	op, changed, err := e.s.FinishCLIVerification(ctx, e.project, account, in.OperationID, cliverify.Result{Outcome: "passed"})
	if err != nil || !changed || op.Status != "succeeded" || !op.IsCurrentVersion || op.ToolVersion == nil {
		t.Fatal("finish", err)
	}
	cancelled, err := e.s.CancelCLIVerification(ctx, e.project, account, in.OperationID)
	if err != nil || cancelled.Status != "succeeded" {
		t.Fatal("cancel rewrote committed success", err)
	}
	state, err := e.s.CLIVerificationState(ctx, e.project, account)
	if err != nil || state.Operation == nil || state.Operation.ID != in.OperationID {
		t.Fatal("state lost result", err)
	}
	in.OperationID = uuid.New()
	if _, _, err = e.s.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = e.s.RecoverCLIVerifications(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = e.s.GetCLIVerification(ctx, e.project, account, in.OperationID)
	if err != nil || op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "server_restarted" {
		t.Fatal("recovery replayed or lost failure", err)
	}
	if _, claimed, err = e.s.ClaimCLIVerification(ctx, e.project, account, in.OperationID); err != nil || claimed {
		t.Fatal("recovered operation executed", err)
	}
}
func TestCLIVerificationTargetChangeAndProjectPurge(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	var org uuid.UUID
	if err := e.s.Pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, e.project).Scan(&org); err != nil {
		t.Fatal(err)
	}
	project, err := e.s.CreateProject(ctx, org, "probe-purge")
	if err != nil {
		t.Fatal(err)
	}
	account, version := verificationSeed(t, e, project.ID, "gcloud", "oauth")
	in := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: version}
	meta := verificationMeta()
	meta.ProbeKind = "google_crm_read"
	if _, _, err = e.s.BeginCLIVerification(ctx, project.ID, account, in, meta, "console", nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := e.s.ClaimCLIVerification(ctx, project.ID, account, in.OperationID); err != nil || !claimed {
		t.Fatal(err)
	}
	// Model a persisted management refresh lease without claiming native/Go
	// coordinator integration. That matrix is independent task 2.1.
	mustExec(t, e.s, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_until=clock_timestamp()+interval '1 minute',lease_verification_id=$3 WHERE version_id=$1`, version, uuid.New(), in.OperationID)
	// Force the final project tombstone write to fail: cancellation and purge
	// must roll back with it, retaining both original lease and operation.
	mustExec(t, e.s, `CREATE FUNCTION reject_probe_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END $$`)
	mustExec(t, e.s, `CREATE TRIGGER reject_probe_delete BEFORE UPDATE OF deleted_at ON projects FOR EACH ROW EXECUTE FUNCTION reject_probe_delete()`)
	if _, err = e.s.DeleteProject(ctx, project.ID); err == nil {
		t.Fatal("rollback injection did not fire")
	}
	op, err := e.s.GetCLIVerification(ctx, project.ID, account, in.OperationID)
	if err != nil || op.Status != "running" {
		t.Fatal("rollback cancelled operation", err)
	}
	var owner uuid.UUID
	if err = e.s.Pool.QueryRow(ctx, `SELECT lease_verification_id FROM google_cli_grants WHERE version_id=$1`, version).Scan(&owner); err != nil || owner != in.OperationID {
		t.Fatal("rollback lost original lease", err)
	}
	mustExec(t, e.s, `DROP TRIGGER reject_probe_delete ON projects`)
	if _, err = e.s.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal("project purge blocked by history FK", err)
	}
	var gotProject, gotAccount, gotVersion uuid.UUID
	var live *uuid.UUID
	var status, outcome string
	err = e.s.Pool.QueryRow(ctx, `SELECT project_id,account_id,version_id,live_version_id,status,result_outcome FROM cli_credential_verifications WHERE id=$1`, in.OperationID).Scan(&gotProject, &gotAccount, &gotVersion, &live, &status, &outcome)
	if err != nil || gotProject != project.ID || gotAccount != account || gotVersion != version || live != nil || status != "cancelled" || outcome != "cancelled" {
		t.Fatal("purge lost history or nulled scope", err)
	}
	var versions, grants int
	if err = e.s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions WHERE account_id=$1),(SELECT count(*) FROM google_cli_grants WHERE account_id=$1)`, account).Scan(&versions, &grants); err != nil || versions != 0 || grants != 0 {
		t.Fatal("purge retained grant", err)
	}
	if _, err = e.s.GetCLIVerification(ctx, e.project, account, in.OperationID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatal("cross-project operation exposed", err)
	}
	if _, _, err = e.s.FinishCLIVerification(ctx, project.ID, account, in.OperationID, cliverify.Result{Outcome: "passed"}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatal("deleted project revived", err)
	}
}
func TestCLIVerificationReplacementDoesNotPromoteOldResult(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	account, version := verificationSeed(t, e, e.project, "gh", "static")
	in := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: version}
	if _, _, err := e.s.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	write := store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: []byte(`{"env":{"GH_TOKEN":"new-synthetic"},"files":[]}`)}
	replaced, err := e.s.MutateProjectCredential(ctx, e.project, account, 1, nil, &write, false)
	if err != nil {
		t.Fatal(err)
	}
	op, changed, err := e.s.FinishCLIVerification(ctx, e.project, account, in.OperationID, cliverify.Result{Outcome: "passed"})
	if err != nil || changed || op.Status != "failed" || op.IsCurrentVersion || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "target_changed" {
		t.Fatal("late result promoted", err)
	}
	state, err := e.s.CLIVerificationState(ctx, e.project, account)
	if err != nil || state.Operation != nil || state.CurrentVersionID == nil || *state.CurrentVersionID != *replaced.View.CurrentVersionID {
		t.Fatal("new version inherited old result", err)
	}
	if _, _, err = e.s.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal("terminal idempotent receipt not readable", err)
	}
	// A rejected write is rolled back together with the attempt to terminate a new op.
	next := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: *replaced.View.CurrentVersionID}
	if _, _, err = e.s.BeginCLIVerification(ctx, e.project, account, next, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	write.Secret = nil
	if _, err = e.s.MutateProjectCredential(ctx, e.project, account, replaced.View.Revision, nil, &write, false); err == nil {
		t.Fatal("missing credential accepted")
	}
	op, err = e.s.GetCLIVerification(ctx, e.project, account, next.OperationID)
	if err != nil || op.Status != "pending" {
		t.Fatal("rollback terminated op", err)
	}
}
