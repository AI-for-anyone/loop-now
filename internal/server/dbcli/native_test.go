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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func nativeProofPath(t *testing.T, rel string) string {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed native fixture requires darwin-arm64")
	}
	p, e := filepath.Abs(filepath.Join("..", "..", "..", ".local-build", "cli-installation-proof", "unpacked", rel))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p); e != nil {
		t.Skip("reviewed vendor tree unavailable")
	}
	return p
}
func TestNativeMySQLConfigParsingNetworkDenied(t *testing.T) {
	native := nativeProofPath(t, "mysql-8.4.9-macos15-arm64/bin/mysql")
	home := t.TempDir()
	c := Connection{AccountID: uuid.New(), Name: "mysql", VersionID: uuid.New(), Fields: Fields{Host: "offline.example", Port: "3306", User: "native-user", Password: " a:b\\\"# ", Database: "native-db", SSLMode: "DISABLED"}}
	os.WriteFile(filepath.Join(home, ".my.cnf"), []byte("[client]\nhost=host-config-trap.invalid\npassword=host-password-trap\n"), 0600)
	prefix, env, e := nativeConfig("mysql", home, c)
	if e != nil {
		t.Fatal(e)
	}
	profile := filepath.Join(home, "deny.sb")
	os.WriteFile(profile, []byte("(version 1)(allow default)(deny network*)(deny file-read* (subpath \"/opt/homebrew\"))"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-f", profile, native}, append(prefix, "--print-defaults")...)...)
	cmd.Env = env
	b, e := cmd.CombinedOutput()
	defer clear(b)
	if e != nil {
		t.Fatal("native configuration parser failed")
	}
	// Capture synthetic stdout privately; never print parsed credential contents.
	if bytes.Contains(b, []byte("host-config-trap")) || bytes.Contains(b, []byte("host-password-trap")) {
		t.Fatal("native used default HOME configuration")
	}
	for _, expected := range []string{"--host=offline.example", "--user=native-user", "--database=native-db", "--ssl-mode=DISABLED"} {
		if !bytes.Contains(b, []byte(expected)) {
			t.Fatal("native option did not roundtrip")
		}
	}
	// mysql masks passwords in --print-defaults. Its companion uses the same
	// option-file parser; --show is confined to synthetic data in this fixture.
	parser := filepath.Join(filepath.Dir(native), "my_print_defaults")
	args := []string{"-f", profile, parser, prefix[0], "--show", "client"}
	parse := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", args...)
	parse.Env = env
	decoded, e := parse.CombinedOutput()
	defer clear(decoded)
	if e != nil || !bytes.Contains(decoded, []byte("--password="+c.Fields.Password+"\n")) {
		t.Fatal("native option-file password roundtrip failed")
	}

}
func TestNativePostgresServiceAndPassfileLoopbackFixture(t *testing.T) {
	native := nativeProofPath(t, "pgsql/bin/psql")
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	password := " a:b\\\"# "
	type received struct {
		startup, password, query []byte
		err                      error
	}
	result := make(chan received, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			result <- received{err: e}
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		var length [4]byte
		if _, e = io.ReadFull(conn, length[:]); e != nil {
			result <- received{err: e}
			return
		}
		size := binary.BigEndian.Uint32(length[:])
		if size < 8 || size > 65536 {
			result <- received{err: fmt.Errorf("invalid startup")}
			return
		}
		startup := make([]byte, size-4)
		if _, e = io.ReadFull(conn, startup); e != nil {
			result <- received{err: e}
			return
		}
		// Request a cleartext password only for this synthetic loopback fixture.
		if _, e = conn.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 3}); e != nil {
			result <- received{err: e}
			return
		}
		header := make([]byte, 5)
		if _, e = io.ReadFull(conn, header); e != nil {
			result <- received{err: e}
			return
		}
		size = binary.BigEndian.Uint32(header[1:])
		if header[0] != 'p' || size < 5 || size > 65536 {
			result <- received{err: fmt.Errorf("invalid password frame")}
			return
		}
		pass := make([]byte, size-4)
		_, e = io.ReadFull(conn, pass)
		send := func(kind byte, body []byte) error {
			frame := make([]byte, len(body)+5)
			frame[0] = kind
			binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)+4))
			copy(frame[5:], body)
			_, err := conn.Write(frame)
			return err
		}
		if e == nil {
			e = send('R', []byte{0, 0, 0, 0})
		}
		if e == nil {
			e = send('S', []byte("server_version\x0018.3\x00"))
		}
		if e == nil {
			e = send('S', []byte("client_encoding\x00UTF8\x00"))
		}
		if e == nil {
			e = send('Z', []byte{'I'})
		}
		var query []byte
		for e == nil {
			if _, e = io.ReadFull(conn, header); e != nil {
				break
			}
			size = binary.BigEndian.Uint32(header[1:])
			if size < 4 || size > 65536 {
				e = fmt.Errorf("invalid query frame")
				break
			}
			data := make([]byte, size-4)
			if _, e = io.ReadFull(conn, data); e != nil {
				break
			}
			if header[0] == 'X' {
				break
			}
			if header[0] != 'Q' {
				e = fmt.Errorf("unexpected fixture request")
				break
			}
			query = append(query, data...)
			if e = send('C', []byte("SELECT 1\x00")); e == nil {
				e = send('Z', []byte{'I'})
			}
		}
		result <- received{startup: startup, password: pass, query: query, err: e}

	}()
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".psqlrc"), []byte("\\echo host-rc-trap\n"), 0600)
	os.WriteFile(filepath.Join(home, ".pgpass"), []byte("*:*:*:*:host-password-trap\n"), 0600)
	c := Connection{AccountID: uuid.New(), Name: "postgres", VersionID: uuid.New(), Fields: Fields{Host: "127.0.0.1", Port: fmt.Sprint(port), User: "native-user", Password: password, Database: "db:with\\colon", SSLMode: "disable"}}
	_, _, e = nativeConfig("psql", home, c)
	if e != nil {
		t.Fatal(e)
	}
	profile := filepath.Join(home, "loopback.sb")
	text := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%d\"))(deny file-read* (subpath \"/opt/homebrew\"))", port)
	os.WriteFile(profile, []byte(text), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manifest, _ := json.Marshal([]Connection{c})
	if e = privateWrite(filepath.Join(home, ConfigPath("psql")), manifest); e != nil {
		t.Fatal(e)
	}
	wrapper := filepath.Join(home, "psql-sandbox")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec /usr/bin/sandbox-exec -f " + quote(profile) + " " + quote(native) + " \"$@\"\n"
	if e = os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"psql", wrapper, "--loop-connection-id", c.AccountID.String(), "--", "-w", "-c", "SELECT 1"}, Options{Getenv: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, Stdout: &stdout, Stderr: &stderr})
	if code != 0 || strings.Contains(stdout.String(), "host-rc-trap") || strings.Contains(stderr.String(), "host-rc-trap") {
		t.Fatal("production wrapper/local fixture failed or host rc was consumed")
	}
	select {
	case got := <-result:
		if got.err != nil || string(got.password) != password+"\x00" || !bytes.Contains(got.startup, []byte(c.Fields.Database+"\x00")) || !bytes.Contains(got.query, []byte("SELECT 1")) {
			t.Fatal("native service/passfile did not roundtrip")
		}
	case <-ctx.Done():
		t.Fatal("local fixture not cleaned up")
	}
}
