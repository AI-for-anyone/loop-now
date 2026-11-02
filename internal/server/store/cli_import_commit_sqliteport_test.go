//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 cli_import_commit_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestCLIImportCommitUnknownActualPG

package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"loop-now/internal/server/store"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCLIImportCommitUnknownActualPG(t *testing.T) {
	e := dbConnectionFixture(t)
	ctx := context.Background()
	config := e.s.Pool.Config().Copy()
	arm, observed := &atomic.Bool{}, &atomic.Bool{}
	config.ConnConfig.Config.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &importCommitConn{Conn: c, arm: arm, observed: observed}, nil
	}
	// Force no TLS on the exclusively local TEST socket; prevents encrypted wire
	// records being misinterpreted by this acknowledgement-only fault harness.
	if config.ConnConfig.TLSConfig != nil {
		t.Skip("COMMIT acknowledgement harness requires local TEST socket/non-TLS")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fault := &store.Store{Pool: pool}
	h := sha256.Sum256([]byte("synthetic-request"))
	candidate := uuid.New()
	instance := uuid.New()
	in := store.CLIImportBegin{ID: uuid.New(), ProjectID: e.project, ScanID: uuid.New(), InstanceID: instance, RequestFingerprint: h[:], ExpectedRevision: 1, ProvenanceKeyID: "key", Deadline: time.Now().Add(time.Minute), Items: []store.CLIImportItem{{CandidateID: candidate, CLI: "gh", Label: "合成"}}}
	if _, _, err = e.s.BeginCLIImport(ctx, in); err != nil {
		t.Fatal(err)
	}
	v := store.CLIImportPreparedItem{CandidateID: candidate, AccountID: uuid.New(), VersionID: uuid.New(), CLI: "gh", Name: "合成", TemplateSource: "builtin", CredentialFormat: "1", AuthMethod: "static", ProvenanceKeyID: "key", OriginMAC: bytes.Repeat([]byte{1}, 32), ContentMAC: bytes.Repeat([]byte{2}, 32)}
	v.Secret = []byte(`{"env":{"GH_TOKEN":"synthetic"},"files":[]}`)
	arm.Store(true)
	_, changed, err := fault.FinalizeCLIImport(ctx, e.project, in.ID, instance, []store.CLIImportPreparedItem{v}, func(context.Context) error { return nil })
	if !store.IsCLIImportCommitUnknown(err) || changed || !observed.Load() {
		t.Fatalf("expected actual commit unknown, changed=%v acknowledged=%v err=%v", changed, observed.Load(), err)
	}
	// A fresh connection sees the actual durable successful operation/one receipt.
	op, err := e.s.GetCLIImport(ctx, e.project, in.ID)
	if err != nil || op.Status != "succeeded" {
		t.Fatal("commit was not durable", err)
	}
	if _, err = e.s.FinishCLIImport(ctx, e.project, in.ID, "failed", "internal_error", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	op, err = e.s.GetCLIImport(ctx, e.project, in.ID)
	if err != nil || op.Status != "succeeded" {
		t.Fatal("unknown outcome overwrote terminal", err)
	}
	if _, created, err := e.s.BeginCLIImport(ctx, in); err != nil || created {
		t.Fatal("commit unknown replayed acceptance", err)
	}
	var count int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM cli_credential_import_receipts WHERE project_id=$1`, e.project).Scan(&count); err != nil || count != 1 {
		t.Fatal("repeated commit", err)
	}
	t.Log("actual PG COMMIT observed; acknowledgement dropped; independent read confirmed; no replay/terminal overwrite")
}
