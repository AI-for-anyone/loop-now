package app_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/app"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/cliverify"
	verifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/testenv"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func verifyProxyCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal("fixture key generation")
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "owned offline fixture CA"}, DNSNames: []string{"accounts.feishu.cn", "open.feishu.cn", "accounts.larksuite.com", "open.larksuite.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if e != nil {
		t.Fatal("fixture certificate generation")
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, e := tls.X509KeyPair(ca, priv)
	clear(priv)
	if e != nil {
		t.Fatal("fixture key pair")
	}
	return cert, ca
}

// verifyConnectFixture terminates CONNECT locally. It never dials, proxies, redirects,
// resolves provider hosts or opens an external socket.
type verifyConnectFixture struct {
	cert       tls.Certificate
	response   func(*http.Request) (int, string)
	allowed    map[string]bool
	unexpected atomic.Bool
	mu         sync.Mutex
	conns      map[net.Conn]bool
	workers    sync.WaitGroup
}

func (p *verifyConnectFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "CONNECT" || !p.allowed[r.Host] {
		p.unexpected.Store(true)
		w.WriteHeader(403)
		return
	}
	conn, buf, e := w.(http.Hijacker).Hijack()
	if e != nil {
		return
	}
	p.workers.Add(1)
	p.mu.Lock()
	p.conns[conn] = true
	p.mu.Unlock()
	defer func() { conn.Close(); p.mu.Lock(); delete(p.conns, conn); p.mu.Unlock(); p.workers.Done() }()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	buf.Flush()
	secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{p.cert}, MinVersion: tls.VersionTLS12})
	if secure.Handshake() != nil {
		return
	}
	reader := bufio.NewReader(secure)
	for {
		req, e := http.ReadRequest(reader)
		if e != nil {
			return
		}
		// Host must match this tunnel. Never permit a second target inside CONNECT.
		if req.Host+":443" != r.Host {
			p.unexpected.Store(true)
			return
		}
		status, body := p.response(req)
		req.Body.Close()
		if status == 0 {
			var b [1]byte
			secure.Read(b[:])
			return
		}
		resp := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
		if resp.Write(secure) != nil {
			return
		}
	}
}
func (p *verifyConnectFixture) close() {
	p.mu.Lock()
	for c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.workers.Wait()
}

type verificationArchiveTransport map[string]string

func (t verificationArchiveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	path, ok := t[r.URL.String()]
	if !ok || r.Method != "GET" {
		return nil, fmt.Errorf("unregistered offline tool archive")
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	info, e := file.Stat()
	if e != nil {
		file.Close()
		return nil, e
	}
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: file, ContentLength: info.Size(), Request: r}, nil
}
func TestCLIVerificationLarkGoogleActualNativeApp(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed native tool platform darwin-arm64")
	}
	repo := testenv.RepoRoot()
	w := newWorld(t)
	w.cfg.NodeBin = "/Users/ljl/.local/node-v24.21.0/bin/node"
	w.cfg.CLIInstallTimeout = 5 * time.Minute
	w.cfg.CLIProbeTimeout = 10 * time.Second
	w.cfg.AuthRecordTimeout = 2 * time.Second
	files := verificationArchiveTransport{
		"https://github.com/larksuite/cli/releases/download/v1.0.94/lark-cli-1.0.94-darwin-arm64.tar.gz": filepath.Join(repo, ".local-build/cli-verification-native-fixture/lark-official.tar.gz"),
		"https://storage.googleapis.com/cloud-sdk-release/google-cloud-cli-568.0.0-darwin-arm.tar.gz":    filepath.Join(repo, ".local-build/cli-installation-proof/downloads/google-cloud-cli-568.0.0-darwin-arm.tar.gz"),
		"https://dl.google.com/dl/cloudsdk/channels/rapid/python-3.13.7-macos11.tar.gz":                  filepath.Join(repo, ".local-build/cli-installation-proof/downloads/python-3.13.7-macos11.tar.gz"),
	}
	for _, path := range files {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("reviewed official offline archive absent")
		}
	}
	var tokens, bots, crm, saTokens, unexpected atomic.Int32
	cert, ca := verifyProxyCert(t)
	px := &verifyConnectFixture{cert: cert, allowed: map[string]bool{"accounts.feishu.cn:443": true, "open.feishu.cn:443": true}, conns: map[net.Conn]bool{}}
	px.response = func(r *http.Request) (int, string) {
		if r.Host == "accounts.feishu.cn" && r.Method == "POST" && r.URL.Path == "/oauth/v3/token" {
			tokens.Add(1)
			if r.ParseForm() != nil || r.Form.Get("client_id") != "synthetic-app" || r.Form.Get("client_secret") != "synthetic-secret" {
				unexpected.Add(1)
			}
			return 200, `{"code":0,"access_token":"synthetic-tat","expires_in":3600}`
		}
		if r.Host == "open.feishu.cn" && r.Method == "GET" && r.URL.Path == "/open-apis/bot/v3/info" {
			bots.Add(1)
			if r.Header.Get("Authorization") != "Bearer synthetic-tat" {
				unexpected.Add(1)
			}
			return 200, `{"code":0,"bot":{"open_id":"synthetic-bot","app_name":"synthetic-application"}}`
		}
		unexpected.Add(1)
		return 403, `{"code":10003}`
	}
	proxy := httptest.NewServer(px)
	defer func() { proxy.Close(); px.close() }()
	_, proxyPort, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/token" {
			saTokens.Add(1)
			if r.ParseForm() != nil || r.Form.Get("assertion") == "" {
				unexpected.Add(1)
			}
			_, _ = io.WriteString(w, `{"access_token":"synthetic-google-access","expires_in":3600,"token_type":"Bearer"}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/projects" {
			crm.Add(1)
			if r.Header.Get("Authorization") != "Bearer synthetic-google-access" {
				unexpected.Add(1)
			}
			_, _ = io.WriteString(w, `{"projects":[]}`)
			return
		}
		unexpected.Add(1)
		w.WriteHeader(403)
	}))
	defer google.Close()
	_, googlePort, _ := net.SplitHostPort(google.Listener.Addr().String())
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	factory := func(_ context.Context, spec verifyservice.CommandSpec) (*exec.Cmd, error) {
		// Only explicit Go fixtures adapt official transport. Original production
		// Spec fixed argv/credential paths remain unchanged and are never logged.
		port := proxyPort
		if spec.CLI == "gcloud" {
			port = googlePort
		}
		bridge := ""
		env := map[string]string{}
		for _, value := range spec.Environment {
			k, v, ok := strings.Cut(value, "=")
			if ok {
				env[k] = v
			}
			if k == "LOOP_NOW_GCLOUD_BRIDGE_SOCKET" {
				bridge = v
			}
		}
		unix := ""
		if bridge != "" {
			unix = " (remote unix-socket (path " + strconv.Quote(bridge) + "))"
		}
		profile := filepath.Join(spec.Home, "native.sb")
		body := fmt.Sprintf(`(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp "localhost:%s")%s)(deny file-read* (subpath "/Users/ljl/.config") (subpath "/Users/ljl/Library/Keychains"))(deny process-exec (literal "/usr/bin/security"))`, port, unix)
		if e := os.WriteFile(profile, []byte(body), 0600); e != nil {
			return nil, e
		}
		var cmd *exec.Cmd
		if spec.CLI == "lark-cli" {
			cmd = exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile, spec.Executable}, spec.Args...)...)
			if spec.Phase == "probe" {
				caFile := filepath.Join(spec.Home, "fixture-ca.pem")
				if e := os.WriteFile(caFile, ca, 0600); e != nil {
					return nil, e
				}
				cmd.Env = append(append([]string{}, spec.Environment...), "LARKSUITE_CLI_PROXY_ENABLE=true", "LARKSUITE_CLI_PROXY_ADDRESS="+proxy.URL, "LARKSUITE_CLI_CA_PATH="+caFile)
			}
			return cmd, nil
		}
		if spec.CLI != "gcloud" {
			return nil, fmt.Errorf("unexpected native tool")
		}
		args := append([]string{}, spec.Args...)
		if spec.Phase == "probe" {
			if len(spec.Args) != 4 || spec.Args[0] != "-S" || spec.Args[2] != "--sdk-root" {
				return nil, fmt.Errorf("unexpected fixed Google entry")
			}
			code := "import sys,os,json; from pathlib import Path; sys.path.insert(0," + strconv.Quote(filepath.Dir(spec.Args[1])) + "); import google_verify as v; v.CRM_ORIGIN=" + strconv.Quote(google.URL) + "; v.TOKEN_URL=" + strconv.Quote(google.URL+"/token") + "; os.environ['CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDRESOURCEMANAGER']=" + strconv.Quote(google.URL+"/") + "; os.environ['CLOUDSDK_AUTH_TOKEN_HOST']=" + strconv.Quote(google.URL+"/token") + "; sys.argv=['verify','--sdk-root'," + strconv.Quote(spec.Args[3]) + "]; raise SystemExit(v.main())"
			args = []string{"-S", "-c", code}
		}
		shell := "export DYLD_FRAMEWORK_PATH=" + quote(env["DYLD_FRAMEWORK_PATH"]) + " DYLD_LIBRARY_PATH=" + quote(env["DYLD_LIBRARY_PATH"]) + " PYTHONHOME=" + quote(env["PYTHONHOME"]) + "; exec " + quote(spec.Executable)
		for _, arg := range args {
			shell += " " + quote(arg)
		}
		cmd = exec.Command("/usr/bin/sandbox-exec", "-f", profile, "/bin/sh", "-c", shell)
		cmd.Env = append([]string{}, spec.Environment...)
		return cmd, nil
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var diagnostics bytes.Buffer
	defer func() {
		for _, line := range strings.Split(diagnostics.String(), "\n") {
			if strings.Contains(line, "CLI验证工具版本校验失败") {
				t.Log(line)
			}
		}
	}()
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(&diagnostics, "json", w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIHTTPClient: &http.Client{Transport: files}, CLIVerificationCommand: factory})
	if e != nil {
		t.Fatal("isolated App build failed", e)
	}
	go func() { _ = a.Serve() }()
	srv := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	defer srv.stop()
	validator := contracttest.New(t, "http://"+a.Addr())
	client := validator.Client()
	call := func(method, path string, value any) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			b, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(b)
		}
		req, e := http.NewRequest(method, srv.url+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if value != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode >= 400 {
			t.Fatalf("formal native App request failed status=%d", response.StatusCode)
		}
		return raw
	}
	project := a.API.O.Defaults.Project.ID
	create := func(cli, name string, fields map[string]string) uuid.UUID {
		t.Helper()
		raw := call("POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": cli, "name": name, "credential": map[string]any{"type": "cli", "fields": fields}})
		var account struct {
			ID uuid.UUID `json:"id"`
		}
		if json.Unmarshal(raw, &account) != nil || account.ID == uuid.Nil {
			t.Fatal("create returned no account")
		}
		return account.ID
	}
	lark := create("lark-cli", "native lark", map[string]string{"appId": "synthetic-app", "appSecret": "synthetic-secret", "brand": "feishu"})
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	sa, e := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "fixture-id", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "client_email": "fixture@synthetic-project.iam.gserviceaccount.com", "client_id": "fixture-client", "token_uri": "https://oauth2.googleapis.com/token"})
	clear(der)
	if e != nil {
		t.Fatal(e)
	}
	serviceAccount := create("gcloud", "native SA", map[string]string{"serviceAccountJson": string(sa), "defaultProject": "must-not-select-business"})
	clear(sa)
	oauth := clicredential.Google{Type: "authorized_user", Account: "fixture@example.invalid", ClientID: "fixture-client", ClientSecret: "fixture-secret", RefreshToken: "never-consumed", AccessToken: "synthetic-google-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}
	plain, e := oauth.Encode()
	if e != nil {
		t.Fatal(e)
	}
	record, e := a.Store.CreateProjectCredential(context.Background(), project, "cli", "gcloud", "native OAuth", store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &oauth.Account, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
	clear(plain)
	if e != nil {
		t.Fatal(e)
	}
	ranLark, ranGoogle, ranSA := int32(0), int32(0), int32(0)
	for _, test := range []struct {
		name    string
		account uuid.UUID
	}{{"lark", lark}, {"google_sa", serviceAccount}, {"google_oauth", record.View.ID}} {
		t.Run(test.name, func(t *testing.T) {
			account := test.account
			if test.name == "lark" {
				ranLark++
			} else {
				ranGoogle++
				if test.name == "google_sa" {
					ranSA++
				}
			}
			r, e := a.Store.GetProjectCredential(context.Background(), project, account)
			if e != nil {
				t.Fatal(e)
			}
			id := uuid.New()
			base := "/projects/" + project.String() + "/credential-accounts/" + account.String() + "/verification-operations"
			raw := call("POST", base, cliverify.Start{OperationID: id, ExpectedVersionID: *r.View.CurrentVersionID})
			var op cliverify.Operation
			if json.Unmarshal(raw, &op) != nil || op.ID != id {
				t.Fatal("bad accepted receipt")
			}
			deadline := time.Now().Add(5*time.Minute + 15*time.Second)
			for {
				raw = call("GET", base+"/"+id.String(), nil)
				if json.Unmarshal(raw, &op) != nil {
					t.Fatal("bad operation")
				}
				if op.Status != "pending" && op.Status != "running" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("native App did not settle")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if op.Status != "succeeded" || op.ToolVersion == nil || op.Result == nil || op.Result.Outcome != "passed" {
				code := "none"
				if op.Result != nil && op.Result.ErrorCode != nil {
					code = *op.Result.ErrorCode
				}
				t.Fatalf("native whole App failed: cli=%s code=%s", op.CLI, code)
			}
			if _, e := a.Store.GetProjectCredential(context.Background(), project, account); e != nil {
				t.Fatal(e)
			}
		})
	}
	if tokens.Load() != ranLark || bots.Load() != ranLark || crm.Load() != ranGoogle || saTokens.Load() != 2*ranSA || unexpected.Load() != 0 || px.unexpected.Load() {
		t.Fatalf("native controlled fixture counts: larkToken=%d bot=%d CRM=%d SAToken=%d unexpected=%d", tokens.Load(), bots.Load(), crm.Load(), saTokens.Load(), unexpected.Load())
	}
	var count int
	if e = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs`).Scan(&count); e != nil || count != 0 {
		t.Fatal("verification created model run", e)
	}
	if e = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns`).Scan(&count); e != nil || count != 0 {
		t.Fatal("verification created task turn", e)
	}
	if len(validator.Failures()) != 0 {
		t.Fatal("formal native App contract violation")
	}
	t.Logf("actual TEST App/schema16 HTTP->installer->native version->guard/renderer->local protocol->PG success; LarkCases=%d GoogleCases=%d SACases=%d model/task0 provider0", ranLark, ranGoogle, ranSA)
}
