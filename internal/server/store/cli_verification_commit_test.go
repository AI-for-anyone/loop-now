package store_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Only a real TEST PG COMMIT CommandComplete is dropped; transaction bodies
// and persisted rows are not simulated. No secret wire data is logged/stored.
type verificationAckConn struct {
	net.Conn
	arm, observed *atomic.Bool
	drop          atomic.Bool
}

func (c *verificationAckConn) Write(b []byte) (int, error) {
	if len(b) > 5 && b[0] == 'Q' && strings.EqualFold(strings.TrimSpace(string(bytes.TrimRight(b[5:], "\x00"))), "commit") && c.arm.CompareAndSwap(true, false) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}
func (c *verificationAckConn) Read(b []byte) (int, error) {
	if !c.drop.Swap(false) {
		return c.Conn.Read(b)
	}
	for {
		header := make([]byte, 5)
		if _, e := io.ReadFull(c.Conn, header); e != nil {
			return 0, e
		}
		n := int(binary.BigEndian.Uint32(header[1:])) - 4
		if n < 0 || n > 1<<20 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, n)
		if _, e := io.ReadFull(c.Conn, body); e != nil {
			return 0, e
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
func TestCLIVerificationActualCommitAcknowledgementLoss(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	account, version := verificationSeed(t, e, e.project, "gh", "static")
	cfg := e.s.Pool.Config().Copy()
	if cfg.ConnConfig.TLSConfig != nil {
		t.Skip("exclusive local TEST non-TLS acknowledgement fixture required")
	}
	var arm, observed atomic.Bool
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &verificationAckConn{Conn: c, arm: &arm, observed: &observed}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fault := &store.Store{Pool: pool}
	in := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: version}
	arm.Store(true)
	_, created, err := fault.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute))
	if err == nil || created || !observed.Load() {
		t.Fatal("unknown acceptance treated as dispatched success")
	}
	op, err := e.s.GetCLIVerification(ctx, e.project, account, in.OperationID)
	if err != nil || op.Status != "pending" {
		t.Fatal("committed acceptance not recoverable", err)
	}
	if _, created, err = e.s.BeginCLIVerification(ctx, e.project, account, in, verificationMeta(), "console", nil, time.Now().Add(time.Minute)); err != nil || created {
		t.Fatal("unknown acceptance replayed dispatch", err)
	}
	if _, ok, err := e.s.ClaimCLIVerification(ctx, e.project, account, in.OperationID); err != nil || !ok {
		t.Fatal(err)
	}
	tool := "2.100.0"
	if err = e.s.CLIVerificationPhase(ctx, e.project, account, in.OperationID, "probing", &tool); err != nil {
		t.Fatal(err)
	}
	observed.Store(false)
	arm.Store(true)
	_, changed, err := fault.FinishCLIVerification(ctx, e.project, account, in.OperationID, cliverify.Result{Outcome: "passed"})
	if err == nil || changed || !observed.Load() {
		t.Fatal("unacknowledged result claimed confirmed success")
	}
	op, err = e.s.GetCLIVerification(ctx, e.project, account, in.OperationID)
	if err != nil || op.Status != "succeeded" {
		t.Fatal("real committed result was lost", err)
	}
	code := "internal_error"
	if _, changed, err = e.s.FinishCLIVerification(ctx, e.project, account, in.OperationID, cliverify.Result{Outcome: "failed", ErrorCode: &code}); err != nil || changed {
		t.Fatal("late uncertainty overwrote committed result", err)
	}
	var count int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM cli_credential_verifications`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate acceptance or result", err)
	}
	t.Log("schema16 real COMMIT observed then ACK dropped twice; original ID/readback only; no fabricated changed/created success or terminal overwrite")
}
