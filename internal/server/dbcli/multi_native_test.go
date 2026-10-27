package dbcli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativePGFixture(t *testing.T, c *Connection) <-chan error {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	c.Fields.Port = fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		var lenbuf [4]byte
		if _, e = io.ReadFull(conn, lenbuf[:]); e != nil {
			done <- e
			return
		}
		n := binary.BigEndian.Uint32(lenbuf[:])
		if n < 8 || n > 65536 {
			done <- fmt.Errorf("bad startup")
			return
		}
		startup := make([]byte, n-4)
		if _, e = io.ReadFull(conn, startup); e != nil {
			done <- e
			return
		}
		if !bytes.Contains(startup, []byte(c.Fields.Database+"\x00")) || !bytes.Contains(startup, []byte(c.Fields.User+"\x00")) {
			done <- fmt.Errorf("connection identity mismatch")
			return
		}
		send := func(kind byte, body []byte) error {
			packet := make([]byte, len(body)+5)
			packet[0] = kind
			binary.BigEndian.PutUint32(packet[1:], uint32(len(body)+4))
			copy(packet[5:], body)
			_, e := conn.Write(packet)
			return e
		}
		receive := func() (byte, []byte, error) {
			var header [5]byte
			if _, e := io.ReadFull(conn, header[:]); e != nil {
				return 0, nil, e
			}
			size := binary.BigEndian.Uint32(header[1:])
			if size < 4 || size > 65536 {
				return 0, nil, fmt.Errorf("bad native frame")
			}
			body := make([]byte, size-4)
			_, e := io.ReadFull(conn, body)
			return header[0], body, e
		}
		if e = send('R', []byte{0, 0, 0, 3}); e != nil {
			done <- e
			return
		}
		kind, pass, e := receive()
		if e != nil || kind != 'p' || string(pass) != c.Fields.Password+"\x00" {
			done <- fmt.Errorf("captured password mismatch")
			return
		}
		clear(pass)
		for _, v := range []struct {
			k byte
			b []byte
		}{{'R', []byte{0, 0, 0, 0}}, {'S', []byte("server_version\x0018.3\x00")}, {'S', []byte("client_encoding\x00UTF8\x00")}, {'Z', []byte{'I'}}} {
			if e = send(v.k, v.b); e != nil {
				done <- e
				return
			}
		}
		kind, query, e := receive()
		if e != nil || kind != 'Q' || !bytes.Contains(query, []byte("SELECT 1")) {
			done <- fmt.Errorf("native query missing")
			return
		}
		if e = send('C', []byte("SELECT 1\x00")); e == nil {
			e = send('Z', []byte{'I'})
		}
		if e != nil {
			done <- e
			return
		}
		kind, _, e = receive()
		if e == nil && kind != 'X' {
			e = fmt.Errorf("unexpected extra native request")
		}
		if e == io.EOF {
			e = nil
		}
		done <- e
	}()
	return done
}
func TestNativePostgresTwoCapturedConnections(t *testing.T) {
	native := nativeProofPath(t, "pgsql/bin/psql")
	home := t.TempDir()
	connections := []Connection{}
	for _, name := range []string{"A", "B"} {
		connections = append(connections, Connection{AccountID: uuid.New(), Name: name, VersionID: uuid.New(), Fields: Fields{Host: "127.0.0.1", User: "user-" + name, Password: "synthetic-" + name + ":\\", Database: "db-" + name, SSLMode: "disable"}})
	}
	results := []<-chan error{}
	for n := range connections {
		results = append(results, nativePGFixture(t, &connections[n]))
	}
	raw, _ := json.Marshal(connections)
	if e := privateWrite(filepath.Join(home, ConfigPath("psql")), raw); e != nil {
		t.Fatal(e)
	}
	clear(raw)
	profile := filepath.Join(home, "offline.sb")
	text := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\") (remote tcp \"localhost:%s\"))(deny file-read* (subpath \"/opt/homebrew\"))", connections[0].Fields.Port, connections[1].Fields.Port)
	os.WriteFile(profile, []byte(text), 0600)
	wrapper := filepath.Join(home, "native-sandbox")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	os.WriteFile(wrapper, []byte("#!/bin/sh\nexec /usr/bin/sandbox-exec -f "+quote(profile)+" "+quote(native)+" \"$@\"\n"), 0700)
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var out, errout bytes.Buffer
	if code := Run(ctx, []string{"psql", wrapper, "-w", "-c", "SELECT 1"}, Options{Getenv: getenv, Stdout: &out, Stderr: &errout}); code == 0 {
		t.Fatal("multiple captured connections guessed a default")
	}
	for _, c := range connections {
		out.Reset()
		errout.Reset()
		if code := Run(ctx, []string{"psql", wrapper, "--loop-connection-id", c.AccountID.String(), "--", "-w", "-c", "SELECT 1"}, Options{Getenv: getenv, Stdout: &out, Stderr: &errout}); code != 0 {
			t.Fatal("actual native captured connection failed")
		}
	}
	for _, result := range results {
		select {
		case e := <-result:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("native fixture not closed")
		}
	}
}
