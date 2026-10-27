package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliverify"
	verifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/testutil/testenv"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type verifyWireProof struct {
	Identity, Password, Query, TLSAttempt bool
	Err                                   string
}

func verifyPGWire(ln net.Listener, c dbcli.Connection, scenario string, cert tls.Certificate, entered chan struct{}) (proof verifyWireProof) {
	sock, e := ln.Accept()
	if e != nil {
		proof.Err = "accept"
		return
	}
	defer sock.Close()
	sock.SetDeadline(time.Now().Add(5 * time.Second))
	readStartup := func() ([]byte, error) {
		var h [4]byte
		if _, e := io.ReadFull(sock, h[:]); e != nil {
			return nil, e
		}
		n := binary.BigEndian.Uint32(h[:])
		if n < 8 || n > 65536 {
			return nil, fmt.Errorf("frame")
		}
		b := make([]byte, n-4)
		_, e := io.ReadFull(sock, b)
		return b, e
	}
	startup, e := readStartup()
	if e != nil {
		proof.Err = "startup"
		return
	}
	if scenario == "tls_identity" || scenario == "tls_success" {
		if len(startup) != 4 || binary.BigEndian.Uint32(startup) != 80877103 {
			proof.Err = "SSL request missing"
			return
		}
		proof.TLSAttempt = true
		sock.Write([]byte{'S'})
		secure := tls.Server(sock, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		err := secure.Handshake()
		if scenario == "tls_identity" {
			return
		}
		if err != nil {
			proof.Err = "TLS handshake"
			return
		}
		sock = secure
		startup, e = readStartup()
		if e != nil {
			proof.Err = "TLS startup"
			return
		}
	}
	proof.Identity = bytes.Contains(startup, []byte("user\x00"+c.Fields.User+"\x00")) && bytes.Contains(startup, []byte("database\x00"+c.Fields.Database+"\x00"))
	send := func(k byte, b []byte) error {
		h := make([]byte, 5)
		h[0] = k
		binary.BigEndian.PutUint32(h[1:], uint32(4+len(b)))
		_, e := sock.Write(append(h, b...))
		return e
	}
	recv := func() (byte, []byte, error) {
		var h [5]byte
		if _, e := io.ReadFull(sock, h[:]); e != nil {
			return 0, nil, e
		}
		n := binary.BigEndian.Uint32(h[1:])
		if n < 4 || n > 65536 {
			return 0, nil, fmt.Errorf("frame")
		}
		b := make([]byte, n-4)
		_, e := io.ReadFull(sock, b)
		return h[0], b, e
	}
	if e = send('R', []byte{0, 0, 0, 3}); e != nil {
		proof.Err = "auth challenge"
		return
	}
	kind, pass, e := recv()
	proof.Password = e == nil && kind == 'p' && string(pass) == c.Fields.Password+"\x00"
	clear(pass)
	if e != nil {
		proof.Err = "password"
		return
	}
	if scenario == "authentication" || scenario == "database" {
		state := "28P01"
		if scenario == "database" {
			state = "3D000"
		}
		send('E', []byte("SFATAL\x00C"+state+"\x00Msynthetic rejection\x00\x00"))
		return
	}
	send('R', []byte{0, 0, 0, 0})
	send('S', []byte("server_version\x0018.3\x00"))
	send('S', []byte("client_encoding\x00UTF8\x00"))
	send('Z', []byte{'I'})
	kind, query, e := recv()
	proof.Query = e == nil && kind == 'Q' && string(query) == "SELECT 1\x00"
	if !proof.Query {
		proof.Err = "unexpected query"
		return
	}
	if scenario == "cancel" || scenario == "query_deadline" {
		close(entered)
		var b [1]byte
		sock.Read(b[:])
		return
	}
	// One int4 text-format column, one DataRow, then CommandComplete/Ready.
	rowdesc := append([]byte{0, 1}, []byte("?column?\x00")...)
	rowdesc = append(rowdesc, make([]byte, 6)...)
	rowdesc = append(rowdesc, 0, 0, 0, 23, 0, 4, 255, 255, 255, 255, 0, 0)
	value := byte('1')
	if scenario == "bad_row" {
		value = '2'
	}
	send('T', rowdesc)
	send('D', []byte{0, 1, 0, 0, 0, 1, value})
	send('C', []byte("SELECT 1\x00"))
	send('Z', []byte{'I'})
	recv()
	return
}

func verifyMySQLWire(ln net.Listener, c dbcli.Connection, scenario string, cert tls.Certificate, entered chan struct{}) (proof verifyWireProof) {
	sock, e := ln.Accept()
	if e != nil {
		proof.Err = "accept"
		return
	}
	defer sock.Close()
	sock.SetDeadline(time.Now().Add(5 * time.Second))
	send := func(seq byte, b []byte) error {
		h := []byte{byte(len(b)), byte(len(b) >> 8), byte(len(b) >> 16), seq}
		_, e := sock.Write(append(h, b...))
		return e
	}
	recv := func() ([]byte, error) {
		var h [4]byte
		if _, e := io.ReadFull(sock, h[:]); e != nil {
			return nil, e
		}
		n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
		if n < 1 || n > 65536 {
			return nil, fmt.Errorf("frame")
		}
		b := make([]byte, n)
		_, e := io.ReadFull(sock, b)
		return b, e
	}
	salt := []byte("0123456789abcdefghij")
	caps := uint32(1 | 4 | 8 | 0x200 | 0x2000 | 0x8000 | 0x10000 | 0x20000 | 0x80000 | 0x1000000 | 0x8000000)
	if scenario == "tls_identity" || scenario == "tls_success" {
		caps |= 0x800
	}
	greeting := append([]byte{10}, []byte("8.4.9-fixture\x00")...)
	greeting = append(greeting, 1, 0, 0, 0)
	greeting = append(greeting, salt[:8]...)
	greeting = append(greeting, 0, byte(caps), byte(caps>>8), 45, 2, 0, byte(caps>>16), byte(caps>>24), 21)
	greeting = append(greeting, make([]byte, 10)...)
	greeting = append(greeting, salt[8:]...)
	greeting = append(greeting, 0)
	greeting = append(greeting, []byte("mysql_native_password\x00")...)
	if e = send(0, greeting); e != nil {
		proof.Err = "greeting"
		return
	}
	response, e := recv()
	if e != nil {
		proof.Err = "response"
		return
	}
	authSequence := byte(2)
	if scenario == "tls_identity" || scenario == "tls_success" {
		if len(response) != 32 || binary.LittleEndian.Uint32(response[:4])&0x800 == 0 {
			proof.Err = "SSL request missing"
			return
		}
		proof.TLSAttempt = true
		secure := tls.Server(sock, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		err := secure.Handshake()
		if scenario == "tls_identity" {
			return
		}
		if err != nil {
			proof.Err = "TLS handshake"
			return
		}
		sock = secure
		response, e = recv()
		if e != nil {
			proof.Err = "TLS auth response"
			return
		}
		authSequence = 3
	}
	if len(response) < 34 {
		proof.Err = "short response"
		return
	}
	data := response[32:]
	i := bytes.IndexByte(data, 0)
	if i < 0 {
		proof.Err = "user frame"
		return
	}
	userOK := string(data[:i]) == c.Fields.User
	data = data[i+1:]
	if len(data) < 21 || int(data[0]) != 20 {
		proof.Err = "password frame"
		return
	}
	n := int(data[0])
	data = data[1:]
	one := sha1.Sum([]byte(c.Fields.Password))
	two := sha1.Sum(one[:])
	mix := sha1.Sum(append(append([]byte{}, salt...), two[:]...))
	want := make([]byte, 20)
	for j := range want {
		want[j] = one[j] ^ mix[j]
	}
	proof.Password = bytes.Equal(data[:n], want)
	clear(want)
	data = data[n:]
	i = bytes.IndexByte(data, 0)
	proof.Identity = userOK && i >= 0 && string(data[:i]) == c.Fields.Database
	if scenario == "authentication" || scenario == "database" {
		code := uint16(1045)
		state := "28000"
		if scenario == "database" {
			code = 1049
			state = "42000"
		}
		b := append([]byte{255, byte(code), byte(code >> 8), '#'}, []byte(state)...)
		send(2, append(b, []byte("synthetic rejection")...))
		return
	}
	send(authSequence, []byte{0, 0, 0, 2, 0, 0, 0})
	for {
		packet, e := recv()
		if e != nil {
			if !proof.Query {
				proof.Err = "command"
			}
			return
		}
		if packet[0] == 1 {
			return
		}
		if packet[0] != 3 {
			proof.Err = "unexpected command"
			return
		}
		q := strings.TrimSpace(string(bytes.TrimLeft(packet[1:], "\x00\x01")))
		if strings.TrimSuffix(strings.ToUpper(q), ";") == "SELECT $$" {
			send(1, append([]byte{255, 0x28, 0x04, '#', '4', '2', '0', '0', '0'}, []byte("synthetic syntax probe")...))
			continue
		}
		if strings.HasPrefix(strings.ToUpper(q), "SET ") {
			send(1, []byte{0, 0, 0, 2, 0, 0, 0})
			continue
		}
		// The fixed native client reads this server metadata during startup. It
		// is not the credential-verification SELECT and must not set Query=true.
		metadata := strings.ToUpper(q) == "SELECT @@VERSION_COMMENT LIMIT 1"
		if q != "SELECT 1" && !metadata {
			proof.Err = fmt.Sprintf("unexpected SQL: dollar=%t select1=%t version=%t bytes=%d", strings.Contains(q, "$$"), strings.Contains(strings.ToUpper(q), "SELECT 1"), strings.Contains(strings.ToUpper(q), "VERSION"), len(q))
			return
		}
		if !metadata {
			proof.Query = true
		}
		if (scenario == "cancel" || scenario == "query_deadline") && !metadata {
			close(entered)
			var b [1]byte
			sock.Read(b[:])
			return
		}
		col := []byte{}
		for _, field := range []string{"def", c.Fields.Database, "", "", "1", ""} {
			col = append(col, byte(len(field)))
			col = append(col, field...)
		}
		col = append(col, 12, 45, 0, 1, 0, 0, 0, 3, 0, 0, 0, 0, 0)
		value := byte('1')
		if scenario == "bad_row" {
			value = '2'
		}
		packets := [][]byte{{1}, col, {254, 0, 0, 2, 0}, {1, value}, {254, 0, 0, 2, 0}}
		if binary.LittleEndian.Uint32(response[:4])&0x1000000 != 0 {
			packets = [][]byte{{1}, col, {1, value}, {254, 0, 0, 2, 0, 0, 0}}
		}
		for j, b := range packets {
			if send(byte(j+1), b) != nil {
				proof.Err = "result"
				return
			}
		}
	}
}

func TestCLIVerificationDatabaseGitHubActualNativeApp(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed fixed native platform darwin-arm64")
	}
	repo := testenv.RepoRoot()
	w := newWorld(t)
	w.cfg.NodeBin = "/Users/ljl/.local/node-v24.21.0/bin/node"
	w.cfg.CLIInstallTimeout = 5 * time.Minute
	w.cfg.CLIProbeTimeout = 10 * time.Second
	w.cfg.AuthRecordTimeout = 2 * time.Second
	files := verificationArchiveTransport{
		"https://github.com/cli/cli/releases/download/v2.100.0/gh_2.100.0_macOS_arm64.zip": filepath.Join(repo, ".local-build/cli-verification-native-fixture/gh-official.zip"),
		"https://cdn.mysql.com/archives/mysql-8.4/mysql-8.4.9-macos15-arm64.tar.gz":        filepath.Join(repo, ".local-build/cli-installation-proof/downloads/mysql-8.4.9-macos15-arm64.tar.gz"),
		"https://get.enterprisedb.com/postgresql/postgresql-18.3-1-osx-binaries.zip":       filepath.Join(repo, ".local-build/cli-installation-proof/downloads/postgresql-18.3-1-osx-binaries.zip"),
	}
	for _, path := range files {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("reviewed offline official archive missing")
		}
	}
	pg, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer pg.Close()
	my, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer my.Close()
	// One private CA covers localhost IP for both original TLS identity modes.
	cert, ca := verifyDatabaseCert(t)
	connections := map[string]dbcli.Connection{}
	for _, cli := range []string{"psql", "mysql"} {
		mode := "verify-full"
		port := pg.Addr().(*net.TCPAddr).Port
		if cli == "mysql" {
			mode = "VERIFY_IDENTITY"
			port = my.Addr().(*net.TCPAddr).Port
		}
		connections[cli] = dbcli.Connection{Fields: dbcli.Fields{Host: "127.0.0.1", Port: strconv.Itoa(port), User: "synthetic-user", Password: "synthetic-password", Database: "synthetic-db", SSLMode: mode, CACert: string(ca)}}
	}
	pgProof, myProof := make(chan verifyWireProof, 1), make(chan verifyWireProof, 1)
	go func() { pgProof <- verifyPGWire(pg, connections["psql"], "tls_success", cert, make(chan struct{})) }()
	go func() { myProof <- verifyMySQLWire(my, connections["mysql"], "tls_success", cert, make(chan struct{})) }()
	unixRoot, e := os.MkdirTemp("/private/tmp", "ln-app-gh-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(unixRoot)
	socket := filepath.Join(unixRoot, "api")
	unix, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close()
	var githubCalls, unexpected atomic.Int32
	github := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubCalls.Add(1)
		if r.Method != "GET" || r.Host != "api.github.com" || r.URL.Path != "/user" || r.Header.Get("Authorization") != "token synthetic-native-gh" {
			unexpected.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":123,"login":"fixture-user"}`)
	})}
	go github.Serve(unix)
	defer github.Close()
	factory := func(_ context.Context, spec verifyservice.CommandSpec) (*exec.Cmd, error) {
		profile := filepath.Join(spec.Home, "native.sb")
		allow := ""
		if spec.CLI == "gh" {
			allow = "(remote unix-socket (path-literal " + strconv.Quote(socket) + "))"
			if spec.Phase == "probe" {
				cfg := filepath.Join(spec.Home, ".config/gh")
				if e := os.MkdirAll(cfg, 0700); e != nil {
					return nil, e
				}
				if e := os.WriteFile(filepath.Join(cfg, "config.yml"), []byte("http_unix_socket: "+socket+"\n"), 0600); e != nil {
					return nil, e
				}
			}
		} else {
			allow = "(remote tcp \"localhost:" + connections[spec.CLI].Fields.Port + "\")"
		}
		body := `(version 1)(allow default)(deny network*)(allow network-outbound ` + allow + `)(deny file-read* (subpath "/Users/Shared/loop-now") (subpath "/Users/ljl/.config") (subpath "/Users/ljl/Library/Keychains"))(deny process-exec (literal "/usr/bin/security"))`
		if e := os.WriteFile(profile, []byte(body), 0600); e != nil {
			return nil, e
		}
		return exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile, spec.Executable}, spec.Args...)...), nil
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIHTTPClient: &http.Client{Transport: files}, CLIVerificationCommand: factory})
	if e != nil {
		t.Fatal(e)
	}
	go a.Serve()
	srv := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	defer srv.stop()
	validator := contracttest.New(t, "http://"+a.Addr())
	client := validator.Client()
	call := func(method, path string, value any) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			raw, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(raw)
		}
		req, e := http.NewRequest(method, srv.url+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if value != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode >= 400 {
			t.Fatalf("native App HTTP status=%d", resp.StatusCode)
		}
		return raw
	}
	project := a.API.O.Defaults.Project.ID
	for _, cli := range []string{"gh", "mysql", "psql"} {
		t.Run(cli, func(t *testing.T) {
			fields := map[string]string{"token": "synthetic-native-gh"}
			if cli != "gh" {
				c := connections[cli]
				fields = map[string]string{"host": c.Fields.Host, "port": c.Fields.Port, "user": c.Fields.User, "password": c.Fields.Password, "database": c.Fields.Database, "sslMode": c.Fields.SSLMode, "caCert": c.Fields.CACert}
			}
			raw := call("POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": cli, "name": "native " + cli, "credential": map[string]any{"type": "cli", "fields": fields}})
			var account struct {
				ID uuid.UUID `json:"id"`
			}
			if json.Unmarshal(raw, &account) != nil || account.ID == uuid.Nil {
				t.Fatal("missing account")
			}
			record, e := a.Store.GetProjectCredential(context.Background(), project, account.ID)
			if e != nil {
				t.Fatal(e)
			}
			id := uuid.New()
			base := "/projects/" + project.String() + "/credential-accounts/" + account.ID.String() + "/verification-operations"
			call("POST", base, cliverify.Start{OperationID: id, ExpectedVersionID: *record.View.CurrentVersionID})
			deadline := time.Now().Add(5*time.Minute + 15*time.Second)
			var op cliverify.Operation
			for {
				raw = call("GET", base+"/"+id.String(), nil)
				if json.Unmarshal(raw, &op) != nil {
					t.Fatal("bad operation")
				}
				if op.Status != "pending" && op.Status != "running" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("native operation not settled")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if op.Status != "succeeded" || op.ToolVersion == nil || op.Result == nil || op.Result.Outcome != "passed" {
				code := "none"
				if op.Result != nil && op.Result.ErrorCode != nil {
					code = *op.Result.ErrorCode
				}
				t.Fatalf("native full App failed cli=%s code=%s", cli, code)
			}
		})
	}
	for _, done := range []chan verifyWireProof{pgProof, myProof} {
		select {
		case proof := <-done:
			if !proof.Identity || !proof.Password || !proof.Query || !proof.TLSAttempt || proof.Err != "" {
				t.Fatalf("captured native fixed TLS/query proof missing: identity=%v password=%v query=%v TLS=%v category=%s", proof.Identity, proof.Password, proof.Query, proof.TLSAttempt, proof.Err)
			}
		case <-time.After(time.Second):
			t.Fatal("native wire fixture did not close")
		}
	}
	if githubCalls.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("GitHub fixed target/token not preserved")
	}
	var count int
	if e = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs`).Scan(&count); e != nil || count != 0 {
		t.Fatal("model run created", e)
	}
	if e = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns`).Scan(&count); e != nil || count != 0 {
		t.Fatal("task turn created", e)
	}
	if len(validator.Failures()) != 0 {
		t.Fatal("native App contract violations")
	}
	t.Log("actual App HTTP/installer/native versions/guard/renderer/db-wrapper->native GH Unix and DB verify-full/VERIFY_IDENTITY local wire->persisted success; fixed SELECT1, model/task/provider0")
}

func verifyDatabaseCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal("synthetic key generation failed")
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "wrong.fixture.invalid"}, DNSNames: []string{"wrong.fixture.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	if true {
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal("synthetic cert generation failed")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	c, e := tls.X509KeyPair(certPEM, priv)
	clear(priv)
	if e != nil {
		t.Fatal("synthetic cert invalid")
	}
	return c, certPEM
}
