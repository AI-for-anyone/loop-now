package cliimport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// expireOnSignal reports context.DeadlineExceeded once due is closed, so a
// test decides when the operation's deadline arrives; cancelling the parent
// still ends it.
type expireOnSignal struct {
	context.Context
	due, done chan struct{}
}

func newExpireOnSignal(parent context.Context, due chan struct{}) expireOnSignal {
	c := expireOnSignal{Context: parent, due: due, done: make(chan struct{})}
	go func() {
		select {
		case <-due:
		case <-parent.Done():
		}
		close(c.done)
	}()
	return c
}

func (c expireOnSignal) Done() <-chan struct{} { return c.done }

func (c expireOnSignal) Err() error {
	select {
	case <-c.done:
	default:
		return nil
	}
	select {
	case <-c.due:
		return context.DeadlineExceeded
	default:
		return c.Context.Err()
	}
}

// signalDeadline makes operations started on s from now on reach their
// deadline when the returned channel is closed. Their DB deadline_at keeps the
// Start value; tests that rely on the DB deadline move it with expireInDB.
func signalDeadline(s *Service) chan struct{} {
	due := make(chan struct{})
	s.withDeadline = func(parent context.Context, _ time.Time) (context.Context, context.CancelFunc) {
		return context.WithCancel(newExpireOnSignal(parent, due))
	}
	return due
}

// expireInDB makes the operation's DB deadline due now.
func expireInDB(t *testing.T, pool *db.DB, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE cli_credential_import_operations SET deadline_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

type serviceAckConn struct {
	net.Conn
	arm      *atomic.Bool
	drop     atomic.Bool
	observed *atomic.Bool
	deny     *atomic.Bool
}

func (c *serviceAckConn) Write(b []byte) (int, error) {
	if c.deny != nil && c.deny.Load() {
		c.Close()
		return 0, errors.New("synthetic TEST connection outage")
	}
	if len(b) > 5 && b[0] == 'Q' && strings.EqualFold(strings.TrimSpace(string(bytes.TrimRight(b[5:], "\x00"))), "commit") && c.arm.CompareAndSwap(true, false) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}

func (c *serviceAckConn) Read(b []byte) (int, error) {
	if !c.drop.Swap(false) {
		return c.Conn.Read(b)
	}
	for {
		head := make([]byte, 5)
		if _, err := io.ReadFull(c.Conn, head); err != nil {
			return 0, err
		}
		n := int(binary.BigEndian.Uint32(head[1:])) - 4
		if n < 0 || n > 1<<20 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return 0, err
		}
		if head[0] == 'C' && string(body) == "COMMIT\x00" {
			c.observed.Store(true)
			c.Close()
			return 0, io.EOF
		}
		if head[0] == 'E' {
			return 0, io.ErrUnexpectedEOF
		}
	}
}
