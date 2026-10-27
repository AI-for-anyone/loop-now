package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/config"
)

// Receipts written under the removed master-key HMAC stay as history. They
// never match the plain SHA-256 key, never block an import, and the same
// source is imported again as a new account; later imports match the new key.
func TestCLIImportLegacyReceiptRetainedButNeverMatched(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	p := filepath.Join(home, ".config/gh/hosts.yml")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("github.com:\n  user: synthetic\n  oauth_token: SYNTHETIC_LEGACY_RECEIPT_TOKEN\n"), 0600); e != nil {
		t.Fatal(e)
	}
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIImportReaderFactory: func(context.Context) (cliimport.Reader, error) { return source.New(home, catalog) }})
	if e != nil {
		t.Fatal(e)
	}
	go a.Serve()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	defer s.stop()
	ctx := context.Background()
	_, agent := s.createAgent("legacy receipt owner")
	project := *agent.ProjectID
	base := "/projects/" + project.String() + "/cli-credential-import"
	importOnce := func() (cliimport.Candidate, cliimport.Operation) {
		t.Helper()
		scan := decode[cliimport.Scan](t, s.must(201, "POST", base+"/scans", map[string]any{}))
		if len(scan.Candidates) != 1 {
			t.Fatal("expected exact synthetic GH candidate")
		}
		in := cliimport.StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{scan.Candidates[0].CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision}
		s.must(202, "POST", base+"/imports", in)
		for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			op := decode[cliimport.State](t, s.must(200, "GET", base+"/imports/"+in.OperationID.String(), nil)).Operation
			if op.Terminal() {
				if op.Status != "succeeded" || len(op.Items) != 1 || op.Items[0].AccountID == nil {
					t.Fatal("import did not succeed", op.Status, op.ErrorCode)
				}
				return scan.Candidates[0], op
			}
		}
		t.Fatal("import did not settle")
		return cliimport.Candidate{}, cliimport.Operation{}
	}
	receipts := func() map[string]int {
		t.Helper()
		rows, e := a.Store.Pool.Query(ctx, `SELECT provenance_key_id FROM cli_credential_import_receipts WHERE project_id=$1`, project)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var key string
			if e = rows.Scan(&key); e != nil {
				t.Fatal(e)
			}
			out[key]++
		}
		return out
	}
	accounts := func() int {
		t.Helper()
		var n int
		if e := a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM project_credential_accounts WHERE project_id=$1 AND deleted_at IS NULL`, project).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}

	_, first := importOnce()
	if got := receipts(); len(got) != 1 || got["sha256-v1"] != 1 {
		t.Fatal("first receipt not written under sha256-v1", got)
	}
	// Turn that receipt into one written by the removed master-key HMAC: a
	// different key ID and an unrelated origin digest.
	legacyKey := make([]byte, 32)
	legacyOrigin := make([]byte, 32)
	_, _ = rand.Read(legacyKey)
	_, _ = rand.Read(legacyOrigin)
	legacyID := hex.EncodeToString(legacyKey)
	if _, e = a.Store.Pool.Exec(ctx, `UPDATE cli_credential_import_receipts SET provenance_key_id=$2,origin_mac=$3 WHERE project_id=$1`, project, legacyID, legacyOrigin); e != nil {
		t.Fatal(e)
	}

	candidate, second := importOnce()
	if candidate.PreviouslyImported {
		t.Fatal("legacy receipt matched the new provenance key")
	}
	if second.Items[0].Status != "imported" || *second.Items[0].AccountID == *first.Items[0].AccountID || accounts() != 2 {
		t.Fatal("re-import did not create a new account", second.Items[0].Status)
	}
	got := receipts()
	if len(got) != 2 || got[legacyID] != 1 || got["sha256-v1"] != 1 {
		t.Fatal("legacy receipt not retained beside the new one", got)
	}
	var kept []byte
	if e = a.Store.Pool.QueryRow(ctx, `SELECT origin_mac FROM cli_credential_import_receipts WHERE project_id=$1 AND provenance_key_id=$2`, project, legacyID).Scan(&kept); e != nil || !bytes.Equal(kept, legacyOrigin) {
		t.Fatal("legacy receipt changed", e)
	}

	candidate, third := importOnce()
	if !candidate.PreviouslyImported || third.Items[0].Status != "already_imported" || *third.Items[0].AccountID != *second.Items[0].AccountID || accounts() != 2 {
		t.Fatal("same source no longer matched under sha256-v1", third.Items[0].Status)
	}
}
