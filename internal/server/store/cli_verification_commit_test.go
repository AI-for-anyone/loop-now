package store_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync/atomic"
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
