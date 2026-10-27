package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

func TestCLIImportRevisionAndDeletedProjectFences(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "revision_changed"
		if deleted {
			name = "project_deleted"
		}
		t.Run(name, func(t *testing.T) {
			e := dbConnectionFixture(t)
			ctx := context.Background()
			base, err := e.s.GetProject(ctx, e.project)
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.s.CreateProject(ctx, base.OrganizationID, "导入目标")
			if err != nil {
				t.Fatal(err)
			}
			candidate, owner := uuid.New(), uuid.New()
			hash := sha256.Sum256([]byte("fence-request"))
			in := store.CLIImportBegin{ID: uuid.New(), ProjectID: p.ID, ScanID: uuid.New(), InstanceID: owner, RequestFingerprint: hash[:], ExpectedRevision: 1, ProvenanceKeyID: "key", Deadline: time.Now().Add(time.Minute), Items: []store.CLIImportItem{{CandidateID: candidate, CLI: "gh", Label: "合成来源"}}}
			if _, _, err = e.s.BeginCLIImport(ctx, in); err != nil {
				t.Fatal(err)
			}
			v := store.CLIImportPreparedItem{CandidateID: candidate, AccountID: uuid.New(), VersionID: uuid.New(), CLI: "gh", Name: "合成", TemplateSource: "builtin", CredentialFormat: "1", AuthMethod: "static", ProvenanceKeyID: "key", OriginMAC: bytes.Repeat([]byte{1}, 32), ContentMAC: bytes.Repeat([]byte{2}, 32)}
			v.Secret = []byte(`{"env":{"GH_TOKEN":"synthetic"},"files":[]}`)
			if deleted {
				if _, err = e.s.DeleteProject(ctx, p.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err = e.s.CreateProjectCredential(ctx, p.ID, "cli", "gh", "并发管理写", store.ProjectCredentialWrite{TemplateSource: "builtin", AuthMethod: "static", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: []byte(`{"env":{"GH_TOKEN":"different-synthetic"},"files":[]}`)})
				if err != nil {
					t.Fatal(err)
				}
			}
			op, changed, err := e.s.FinalizeCLIImport(ctx, p.ID, in.ID, owner, []store.CLIImportPreparedItem{v}, func(context.Context) error { return nil })
			if deleted {
				if err != nil || changed || op.Status != "failed" || *op.ErrorCode != "project_deleted" {
					t.Fatal("deleted target revived", err)
				}
			} else {
				if !apperr.Is(err, "precondition_failed") || changed {
					t.Fatal("revision fence bypass", err)
				}
				if _, err = e.s.FinishCLIImport(ctx, p.ID, in.ID, "failed", "precondition_failed", uuid.Nil); err != nil {
					t.Fatal(err)
				}
			}
			var a, r int
			if err = e.s.Pool.QueryRow(ctx, `SELECT(SELECT count(*) FROM project_credential_accounts WHERE project_id=$1),(SELECT count(*) FROM cli_credential_import_receipts WHERE project_id=$1)`, p.ID).Scan(&a, &r); err != nil {
				t.Fatal(err)
			}
			want := 1
			if deleted {
				want = 0
			}
			if a != want || r != 0 {
				t.Fatal("late import survived fence")
			}
		})
	}
}
