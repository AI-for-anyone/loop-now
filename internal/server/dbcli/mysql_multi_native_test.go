package dbcli

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func nativeMySQLFixture(t *testing.T, c *Connection) <-chan error {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	c.Fields.Port = fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	done := make(chan error, 1)
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		send := func(seq byte, data []byte) error {
			header := []byte{byte(len(data)), byte(len(data) >> 8), byte(len(data) >> 16), seq}
			_, e := conn.Write(append(header, data...))
			return e
		}
		recv := func() ([]byte, error) {
			var h [4]byte
			if _, e := io.ReadFull(conn, h[:]); e != nil {
				return nil, e
			}
			n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
			if n < 1 || n > 65536 {
				return nil, fmt.Errorf("invalid mysql frame")
			}
			b := make([]byte, n)
			_, e := io.ReadFull(conn, b)
			return b, e
		}
		salt := []byte("0123456789abcdefghij")
		caps := uint32(1 | 4 | 8 | 0x200 | 0x2000 | 0x8000 | 0x10000 | 0x20000 | 0x80000 | 0x1000000 | 0x8000000)
		greeting := append([]byte{10}, []byte("8.4.9-fixture\x00")...)
		greeting = append(greeting, 1, 0, 0, 0)
		greeting = append(greeting, salt[:8]...)
		greeting = append(greeting, 0, byte(caps), byte(caps>>8), 45, 2, 0, byte(caps>>16), byte(caps>>24), 21)
		greeting = append(greeting, make([]byte, 10)...)
		greeting = append(greeting, salt[8:]...)
		greeting = append(greeting, 0)
		greeting = append(greeting, []byte("mysql_native_password\x00")...)
		if e = send(0, greeting); e != nil {
			done <- e
			return
		}
		response, e := recv()
		if e != nil || len(response) < 34 {
			done <- fmt.Errorf("native mysql handshake missing")
			return
		}
		data := response[32:]
		cut := bytes.IndexByte(data, 0)
		if cut < 0 || string(data[:cut]) != c.Fields.User {
			done <- fmt.Errorf("native mysql user mismatch")
			return
		}
		data = data[cut+1:]
		if len(data) < 1 {
			done <- fmt.Errorf("native auth missing")
			return
		}
		n := int(data[0])
		data = data[1:]
		if n != 20 || len(data) < n {
			done <- fmt.Errorf("native auth size mismatch")
			return
		}
		one := sha1.Sum([]byte(c.Fields.Password))
		two := sha1.Sum(one[:])
		mix := sha1.Sum(append(append([]byte{}, salt...), two[:]...))
		want := make([]byte, 20)
		for i := range want {
			want[i] = one[i] ^ mix[i]
		}
		if !bytes.Equal(data[:n], want) {
			done <- fmt.Errorf("captured mysql password mismatch")
			return
		}
		clear(want)
		data = data[n:]
		cut = bytes.IndexByte(data, 0)
		if cut < 0 || string(data[:cut]) != c.Fields.Database {
			done <- fmt.Errorf("captured mysql database mismatch")
			return
		}
		if e = send(2, []byte{0, 0, 0, 2, 0, 0, 0}); e != nil {
			done <- e
			return
		}
		queried := false
		queryCount := 0
		for {
			packet, e := recv()
			if e != nil {
				done <- e
				return
			}
			if packet[0] == 1 {
				if !queried {
					done <- fmt.Errorf("native query never executed (queries=%d clientFlags=%d)", queryCount, binary.LittleEndian.Uint32(response[:4]))
				} else {
					done <- nil
				}
				return
			}
			if packet[0] != 3 {
				done <- fmt.Errorf("unexpected native mysql command")
				return
			}
			queryCount++
			if strings.TrimSpace(strings.ToUpper(string(bytes.TrimLeft(packet[1:], "\x00\x01")))) == "SELECT $$" {
				// MySQL 8.4's client probes unfinished dollar quoting. An actual server
				// returns a syntax error, not a result set the probe will never consume.
				if e = send(1, append([]byte{255, 0x28, 0x04, '#', '4', '2', '0', '0', '0'}, []byte("synthetic syntax probe")...)); e != nil {
					done <- e
					return
				}
				continue
			}

			if strings.Contains(strings.ToUpper(string(packet[1:])), "SET ") {
				if e = send(1, []byte{0, 0, 0, 2, 0, 0, 0}); e != nil {
					done <- e
					return
				}
				continue
			}
			if strings.Contains(strings.ToUpper(string(packet[1:])), "SELECT 1") {
				queried = true
			}
			col := []byte{}
			for _, field := range []string{"def", c.Fields.Database, "", "", "1", ""} {
				col = append(col, byte(len(field)))
				col = append(col, field...)
			}
			col = append(col, 12, 45, 0)
			size := make([]byte, 4)
			binary.LittleEndian.PutUint32(size, 1)
			col = append(col, size...)
			col = append(col, 3, 0, 0, 0, 0, 0)
			packets := []struct {
				seq byte
				b   []byte
			}{{1, []byte{1}}, {2, col}, {3, []byte{254, 0, 0, 2, 0}}, {4, []byte{1, '1'}}, {5, []byte{254, 0, 0, 2, 0}}}
			if binary.LittleEndian.Uint32(response[:4])&0x1000000 != 0 {
				packets = []struct {
					seq byte
					b   []byte
				}{{1, []byte{1}}, {2, col}, {3, []byte{1, '1'}}, {4, []byte{254, 0, 0, 2, 0, 0, 0}}}
			}
			for _, v := range packets {
				if e = send(v.seq, v.b); e != nil {
					done <- e
					return
				}
			}
		}
	}()
	return done
}
func TestNativeMySQLTwoCapturedConnections(t *testing.T) {
	native := nativeProofPath(t, "mysql-8.4.9-macos15-arm64/bin/mysql")
	home := t.TempDir()
	connections := []Connection{}
	for _, name := range []string{"A", "B"} {
		connections = append(connections, Connection{AccountID: uuid.New(), Name: name, VersionID: uuid.New(), Fields: Fields{Host: "127.0.0.1", User: "user-" + name, Password: "synthetic-" + name + ":\\\" ", Database: "db-" + name, SSLMode: "DISABLED"}})
	}
	results := []<-chan error{}
	for n := range connections {
		results = append(results, nativeMySQLFixture(t, &connections[n]))
	}
	raw, _ := json.Marshal(connections)
	if e := privateWrite(filepath.Join(home, ConfigPath("mysql")), raw); e != nil {
		t.Fatal(e)
	}
	clear(raw)
	profile := filepath.Join(home, "offline.sb")
	text := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\") (remote tcp \"localhost:%s\"))(deny file-read* (subpath \"/opt/homebrew\"))", connections[0].Fields.Port, connections[1].Fields.Port)
	os.WriteFile(profile, []byte(text), 0600)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	wrapper := filepath.Join(home, "native-sandbox")
	os.WriteFile(wrapper, []byte("#!/bin/sh\nexec /usr/bin/sandbox-exec -f "+quote(profile)+" "+quote(native)+" \"$@\"\n"), 0700)
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, c := range connections {
		var out, errout bytes.Buffer
		code := Run(ctx, []string{"mysql", wrapper, "--loop-connection-id", c.AccountID.String(), "--batch", "--raw", "--skip-column-names", "--execute", "SELECT 1"}, Options{Getenv: getenv, Stdout: &out, Stderr: &errout})
		if code != 0 || strings.TrimSpace(out.String()) != "1" {
			category := "unclassified"
			if code := regexp.MustCompile(`ERROR [0-9]+`).FindString(errout.String() + out.String()); code != "" {
				category = code
			}
			for _, known := range []string{"ERROR 2026", "ERROR 2059", "ERROR 2012", "ERROR 2013", "ERROR 2000", "ERROR 1045", "Unknown option", "unknown option", "plugin", "Malformed", "character set", "charset", "packet", "Commands out"} {
				if strings.Contains(errout.String(), known) {
					category = known
					break
				}
			}
			select {
			case e := <-results[0]:
				t.Log("fixture fixed phase", e)
			default:
			}
			t.Fatal("actual native mysql captured connection failed", code, category)
		}
	}
	for _, result := range results {
		select {
		case e := <-result:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("native mysql fixture not closed")
		}
	}
}
