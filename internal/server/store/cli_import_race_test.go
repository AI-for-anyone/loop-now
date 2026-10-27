package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
)

func TestCLIImportCancelFinalizeBothWinners(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		name := "commit_first"
		if cancelFirst {
			name = "cancel_first"
		}
		t.Run(name, func(t *testing.T) {
			e := dbConnectionFixture(t)
			ctx := context.Background()
			owner := uuid.New()
			candidate := uuid.New()
			h := sha256.Sum256([]byte("request"))
			in := store.CLIImportBegin{ID: uuid.New(), ProjectID: e.project, ScanID: uuid.New(), InstanceID: owner, RequestFingerprint: h[:], ExpectedRevision: 1, ProvenanceKeyID: "key", Deadline: time.Now().Add(time.Minute), Items: []store.CLIImportItem{{CandidateID: candidate, CLI: "gh", Label: "合成来源"}}}
			if _, _, err := e.s.BeginCLIImport(ctx, in); err != nil {
				t.Fatal(err)
			}
			v := store.CLIImportPreparedItem{CandidateID: candidate, AccountID: uuid.New(), VersionID: uuid.New(), CLI: "gh", Name: "测试同名", TemplateSource: "builtin", CredentialFormat: "1", AuthMethod: "static", ProvenanceKeyID: "key", OriginMAC: bytes.Repeat([]byte{1}, 32), ContentMAC: bytes.Repeat([]byte{2}, 32)}
			var err error
			v.Secret = []byte(`{"env":{"GH_TOKEN":"synthetic"},"files":[]}`)
			if cancelFirst {
				if op, err := e.s.CancelCLIImport(ctx, e.project, in.ID); err != nil || op.Status != "cancelled" {
					t.Fatal(err)
				}
				checks := 0
				op, changed, err := e.s.FinalizeCLIImport(ctx, e.project, in.ID, owner, []store.CLIImportPreparedItem{v}, func(context.Context) error { checks++; return nil })
				if err != nil || changed || op.Status != "cancelled" || checks != 0 {
					t.Fatal("late finalize revived cancellation", err)
				}
			} else {
				entered, release, finalDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
				go func() {
					defer close(finalDone)
					first := true
					op, changed, err := e.s.FinalizeCLIImport(ctx, e.project, in.ID, owner, []store.CLIImportPreparedItem{v}, func(context.Context) error {
						if first {
							first = false
							close(entered)
							<-release
						}
						return nil
					})
					if err != nil || !changed || op.Status != "succeeded" {
						t.Error("commit winner failed", err)
					}
				}()
				<-entered
				cancelDone := make(chan struct{})
				go func() {
					defer close(cancelDone)
					op, err := e.s.CancelCLIImport(ctx, e.project, in.ID)
					if err != nil || op.Status != "succeeded" {
						t.Error("cancel lied about committed result", err)
					}
				}()
				close(release)
				<-finalDone
				<-cancelDone
			}
			var accounts, receipts int
			if err = e.s.Pool.QueryRow(ctx, `SELECT(SELECT count(*) FROM project_credential_accounts WHERE project_id=$1),(SELECT count(*) FROM cli_credential_import_receipts WHERE project_id=$1)`, e.project).Scan(&accounts, &receipts); err != nil {
				t.Fatal(err)
			}
			want := 1
			if cancelFirst {
				want = 0
			}
			if accounts != want || receipts != want {
				t.Fatal("winner not atomic")
			}
		})
	}
}
