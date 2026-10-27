package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/store"
)

// Drop only the actual PostgreSQL COMMIT acknowledgement after reading its
// CommandComplete. Never substitute a fake successful database transaction.
type importCommitConn struct {
	net.Conn
	arm      *atomic.Bool
	drop     atomic.Bool
	observed *atomic.Bool
}

func (c *importCommitConn) Write(b []byte) (int, error) {
	if len(b) > 5 && b[0] == 'Q' && strings.EqualFold(strings.TrimSpace(string(bytes.TrimRight(b[5:], "\x00"))), "commit") && c.arm.CompareAndSwap(true, false) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}
func (c *importCommitConn) Read(b []byte) (int, error) {
	if !c.drop.Swap(false) {
		return c.Conn.Read(b)
	}
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		n := int(binary.BigEndian.Uint32(header[1:])) - 4
		if n < 0 || n > 1<<20 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return 0, err
		}
		if header[0] == 'C' && string(body) == "COMMIT\x00" {
			c.observed.Store(true)
			c.Conn.Close()
			return 0, io.EOF
		}
		if header[0] == 'E' {
			return 0, io.ErrUnexpectedEOF
		}
	}
}
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
