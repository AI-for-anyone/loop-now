package store_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync/atomic"
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
