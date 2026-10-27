package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

func TestCLIImportAtomicMixedAndHistory(t *testing.T) {
	e := dbConnectionFixture(t)
	ctx := context.Background()
	instance := uuid.New()
	revision := func() int64 {
		t.Helper()
		_, r, err := e.s.ListProjectCredentials(ctx, e.project, "cli", "")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	makeItem := func(origin, token string) store.CLIImportPreparedItem {
		t.Helper()
		v := store.CLIImportPreparedItem{CandidateID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), CLI: "gh", Name: "同名账号", TemplateSource: "builtin", AuthMethod: "static", ProvenanceKeyID: "test-hmac-v1"}
		tm, _ := e.templates.Get("gh")
		payload, names, err := tm.Render(map[string]string{"token": token})
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := json.Marshal(payload)
		v.CredentialFormat = tm.CredentialFormat()
		v.Meta = store.CredentialMeta{TemplateVersion: v.CredentialFormat, FieldNames: names, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}
		h := sha256.Sum256([]byte(origin))
		v.OriginMAC = h[:]
		h2 := sha256.Sum256(plain)
		v.ContentMAC = h2[:]
		v.Secret = plain
		return v
	}
	begin := func(items []store.CLIImportPreparedItem) store.CLIImportBegin {
		t.Helper()
		h := sha256.Sum256([]byte(uuid.NewString()))
		in := store.CLIImportBegin{ID: uuid.New(), ProjectID: e.project, ScanID: uuid.New(), InstanceID: instance, RequestFingerprint: h[:], ExpectedRevision: revision(), ProvenanceKeyID: "test-hmac-v1", Deadline: time.Now().Add(time.Minute)}
		for _, v := range items {
			in.Items = append(in.Items, store.CLIImportItem{CandidateID: v.CandidateID, CLI: v.CLI, Label: "安全合成来源"})
		}
		if _, created, err := e.s.BeginCLIImport(ctx, in); err != nil || !created {
			t.Fatal("begin", err)
		}
		return in
	}
	finalize := func(in store.CLIImportBegin, items []store.CLIImportPreparedItem) (store.CLIImportOperation, bool, error) {
		return e.s.FinalizeCLIImport(ctx, e.project, in.ID, instance, items, func(context.Context) error { return nil })
	}
	a, b := makeItem("originA", "syntheticA"), makeItem("originB", "syntheticB")
	first := begin([]store.CLIImportPreparedItem{a, b})
	before := revision()
	op, changed, err := finalize(first, []store.CLIImportPreparedItem{a, b})
	if err != nil || !changed || op.Status != "succeeded" || revision() != before+1 {
		t.Fatal("atomic first", err)
	}
	for _, v := range []store.CLIImportPreparedItem{a, b} {
		row, _, err := e.s.GetProjectCLIVersion(ctx, v.AccountID, v.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(row.Secret, v.Secret) {
			t.Fatal("imported credential not stored verbatim")
		}
	}
	replay, created, err := e.s.BeginCLIImport(ctx, first)
	if err != nil || created || replay.Status != "succeeded" {
		t.Fatal("same op replay", err)
	}
	altered := first
	altered.RequestFingerprint = bytes.Repeat([]byte{9}, 32)
	if _, _, err = e.s.BeginCLIImport(ctx, altered); !apperr.Is(err, "import_operation_conflict") {
		t.Fatal("different request accepted", err)
	}
	// Cross-scan same-origin results retain original IDs, not preallocated new IDs.
	againA := makeItem("originA", "syntheticA")
	oldOnly := begin([]store.CLIImportPreparedItem{againA})
	before = revision()
	op, changed, err = finalize(oldOnly, []store.CLIImportPreparedItem{againA})
	if err != nil || changed || revision() != before || op.Items[0].Status != "already_imported" || *op.Items[0].AccountID != a.AccountID {
		t.Fatal("pure old bump/recreate", err)
	}
	c := makeItem("originC", "syntheticC")
	mix := begin([]store.CLIImportPreparedItem{againA, c})
	before = revision()
	op, changed, err = finalize(mix, []store.CLIImportPreparedItem{againA, c})
	if err != nil || !changed || revision() != before+1 || op.Items[0].Status != "already_imported" {
		t.Fatal("mixed batch", err)
	}
	// Force second SQL version insertion failure after the first account write.
	d, f := makeItem("originD", "syntheticD"), makeItem("originF", "syntheticF")
	f.VersionID = d.VersionID
	bad := begin([]store.CLIImportPreparedItem{d, f})
	before = revision()
	_, changed, err = finalize(bad, []store.CLIImportPreparedItem{d, f})
	if err == nil || changed || revision() != before {
		t.Fatal("failed batch committed")
	}
	var count int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM project_credential_accounts WHERE project_id=$1`, e.project).Scan(&count); err != nil || count != 3 {
		t.Fatal("partial account", count, err)
	}
	if _, err = e.s.FinishCLIImport(ctx, e.project, bad.ID, "failed", "internal_error", f.CandidateID); err != nil {
		t.Fatal(err)
	}
	// Receipt survives replacement/deletion; cannot resurrect an imported source.
	replacement := store.ProjectCredentialWrite{TemplateSource: "builtin", AuthMethod: "static", Meta: a.Meta, Secret: []byte(`{"env":{"GH_TOKEN":"replacement-synthetic"},"files":[]}`)}
	if _, err = e.s.MutateProjectCredential(ctx, e.project, a.AccountID, 1, nil, &replacement, false); err != nil {
		t.Fatal(err)
	}
	replaced := begin([]store.CLIImportPreparedItem{againA})
	if _, _, err = finalize(replaced, []store.CLIImportPreparedItem{againA}); !apperr.Is(err, "import_source_conflict") {
		t.Fatal("replacement allowed old source overwrite", err)
	}
	if _, err = e.s.CancelCLIImport(ctx, e.project, replaced.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.MutateProjectCredential(ctx, e.project, a.AccountID, 2, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	conflict := begin([]store.CLIImportPreparedItem{againA})
	if _, _, err = finalize(conflict, []store.CLIImportPreparedItem{againA}); !apperr.Is(err, "import_source_conflict") {
		t.Fatal("deleted receipt revived", err)
	}
	if _, err = e.s.CancelCLIImport(ctx, e.project, conflict.ID); err != nil {
		t.Fatal(err)
	}
	key := begin([]store.CLIImportPreparedItem{d})
	if _, err = e.s.CancelCLIImport(ctx, e.project, key.ID); err != nil {
		t.Fatal(err)
	}
	// Receipts under an earlier provenance key stay as history but never match:
	// the same source under the current key is imported again as a new account.
	againB := makeItem("originB", "syntheticB")
	againB.ProvenanceKeyID = "different-key"
	key.ID = uuid.New()
	key.ProvenanceKeyID = "different-key"
	key.ExpectedRevision = revision()
	key.Items = []store.CLIImportItem{{CandidateID: againB.CandidateID, CLI: againB.CLI, Label: "安全合成来源"}}
	if _, created, err := e.s.BeginCLIImport(ctx, key); err != nil || !created {
		t.Fatal("old provenance receipts blocked a new key", err)
	}
	op, changed, err = finalize(key, []store.CLIImportPreparedItem{againB})
	if err != nil || !changed || op.Items[0].Status != "imported" || *op.Items[0].AccountID == b.AccountID || *op.Items[0].AccountID != againB.AccountID {
		t.Fatal("old receipt matched under a different key", err)
	}
	var receipts int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM cli_credential_import_receipts WHERE project_id=$1 AND origin_mac=$2`, e.project, againB.OriginMAC).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("old receipt not retained beside the new one", receipts, err)
	}
	if err = e.s.MigrateDownTo(ctx, 16); err == nil {
		t.Fatal("Down erased history")
	}
}

func TestCLIImportCancelCommitRecovery(t *testing.T) {
	e := dbConnectionFixtureAt(t, 25)
	ctx := context.Background()
	base, err := e.s.GetProject(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.s.CreateProject(ctx, base.OrganizationID, "导入恢复项目")
	if err != nil {
		t.Fatal(err)
	}
	e.project = p.ID
	instance := uuid.New()
	// Empty 017 down/up is lawful, history later blocks it.
	if err := e.s.MigrateDownTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateUpTo(ctx, 17); err != nil {
		t.Fatal(err)
	}
	// Current Store code requires the latest schema.
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	makeBegin := func() store.CLIImportBegin {
		t.Helper()
		h := sha256.Sum256([]byte(uuid.NewString()))
		in := store.CLIImportBegin{ID: uuid.New(), ProjectID: e.project, ScanID: uuid.New(), InstanceID: instance, RequestFingerprint: h[:], ExpectedRevision: 1, ProvenanceKeyID: "key1", Deadline: time.Now().Add(time.Minute), Items: []store.CLIImportItem{{CandidateID: uuid.New(), CLI: "gh", Label: "合成"}}}
		if _, _, err := e.s.BeginCLIImport(ctx, in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	in := makeBegin()
	var wg sync.WaitGroup
	wg.Add(2)
	for _, code := range []string{"cancelled", "worker_restarted"} {
		go func(code string) {
			defer wg.Done()
			status := "failed"
			if code == "cancelled" {
				status = code
			}
			if _, err := e.s.FinishCLIImport(ctx, e.project, in.ID, status, code, uuid.Nil); err != nil {
				t.Error(err)
			}
		}(code)
	}
	wg.Wait()
	op, err := e.s.GetCLIImport(ctx, e.project, in.ID)
	if err != nil || !op.Terminal() {
		t.Fatal(err)
	}
	if _, err = e.s.CancelCLIImport(ctx, e.project, in.ID); err != nil {
		t.Fatal(err)
	}
	pending := makeBegin()
	if err = e.s.RecoverCLIImports(ctx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	op, err = e.s.GetCLIImport(ctx, e.project, pending.ID)
	if err != nil || op.Status != "failed" || *op.ErrorCode != "worker_restarted" {
		t.Fatal("restart failed", err)
	}
	late := makeBegin()
	_, changed, err := e.s.FinalizeCLIImport(ctx, e.project, late.ID, instance, nil, func(context.Context) error { return errors.New("source changed") })
	if err == nil || changed {
		t.Fatal("invalid late write")
	}
	if _, err = e.s.CancelCLIImport(ctx, e.project, late.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.DeleteProject(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.FinishCLIImport(ctx, e.project, late.ID, "failed", "internal_error", uuid.Nil); err != nil {
		t.Fatal("deleted project history cleanup", err)
	}
	if err = e.s.MigrateDownTo(ctx, 16); err == nil {
		t.Fatal("project deletion bypassed Down history guard")
	}
}
