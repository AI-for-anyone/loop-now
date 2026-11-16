package e2e_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"loop-now/internal/buildinfo"
	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/output"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
)

// world is the outside state that can be shared across several server starts: data directory, work root,
// certificates and listen addresses.
type world struct {
	t          *testing.T
	cfg        config.Config
	dump       string // FAKEPI_DUMP: the environment written when Pi starts
	caFile     string
	addr       string // loopback listen address, kept across restarts
	extAddr    string // external port address, kept across restarts
	external   bool
	appOptions func(*app.Options) // isolated dependency injection only
}

// newWorld prepares the config the same way the server's app tests do; it skips when this machine does not have Pi
// 1.4.0 (needed to read the model catalog).
func newWorld(t *testing.T, external bool) *world {
	t.Helper()
	real, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.4.0：%v", err)
	}
	dump := filepath.Join(t.TempDir(), "dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w := &world{t: t, dump: dump, external: external, cfg: config.Config{
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second,
		DataDir:    t.TempDir(),
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
		WorkRoot: filepath.Join(t.TempDir(), "work"), DefaultOrgName: "默认组织", DefaultProjectName: "默认项目",
		PiBin: fakepibin.Path(t), PiVersion: "1.4.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second,
		PiStopGrace: 2 * time.Second, AbortTimeout: 3 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, ReconcileInterval: 500 * time.Millisecond,
		SSEKeepalive: time.Second, EventStreamMaxLen: 1000, ShutdownTimeout: 10 * time.Second,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16,
		TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100,
		LoopctlDistDir: loopctlfake.Dir(t), ExecEnvPassthrough: []string{"FAKEPI_DUMP"},
	}}
	if external {
		cert, key, ca := selfSigned(t)
		w.cfg.ExternalListenAddr, w.cfg.ExternalTLSCert, w.cfg.ExternalTLSKey = "127.0.0.1:0", cert, key
		w.caFile = ca
	}
	return w
}

// server is one started instance of the server.
type server struct {
	t   *testing.T
	a   *app.App
	api string // base address of the console API on loopback
	ext string // root address of the external port
}

// start starts the server; on a restart it reuses the previous listen addresses, so loopctl's connection settings
// do not need to change.
func (w *world) start(mutate func(c *config.Config)) *server {
	w.t.Helper()
	cfg := w.cfg
	if mutate != nil {
		mutate(&cfg)
	}
	listen := func(addr string) net.Listener {
		if addr == "" {
			addr = "127.0.0.1:0"
		}
		var ln net.Listener
		var err error
		for i := 0; i < 50; i++ { // a port that was just closed may take a moment before it can be listened on again
			if ln, err = net.Listen("tcp", addr); err == nil {
				return ln
			}
			time.Sleep(100 * time.Millisecond)
		}
		w.t.Fatalf("监听 %s 失败：%v", addr, err)
		return nil
	}
	o := app.Options{Listener: listen(w.addr), HomeDirs: []string{"/nonexistent-home"}}
	if w.external {
		o.ExternalListener = listen(w.extAddr)
	}
	if w.appOptions != nil {
		w.appOptions(&o)
	}
	a, err := app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, "text", cfg.LogLevel), o)
	if err != nil {
		w.t.Fatalf("启动服务端失败：%v", err)
	}
	go func() { _ = a.Serve() }()
	w.addr = a.Addr()
	s := &server{t: w.t, a: a, api: "http://" + a.Addr() + "/api/v1"}
	if w.external {
		w.extAddr = a.ExternalAddr()
		s.ext = "https://" + a.ExternalAddr()
	}
	return s
}

func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.a.Shutdown(ctx); err != nil {
		s.t.Errorf("停止服务端出错：%v", err)
	}
}

// console calls the API on loopback as the console.
func (s *server) console(status int, method, path string, body any) map[string]any {
	s.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.api+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		s.t.Fatalf("控制台 %s %s：期望 %d，实际 %d：%s", method, path, status, resp.StatusCode, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// waitIdle waits until the Agent has no running or queued runs and the busy lock has been released.
func (s *server) waitIdle(agentID string) {
	s.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		st := s.console(200, "GET", "/agents/"+agentID+"/status", nil)
		q := st["queued"].(map[string]any)
		owner, err := s.a.Coord.LockOwner(context.Background(), agentID)
		if st["busy"] == false && q["interrupt"] == float64(0) && q["normal"] == float64(0) && err == nil && owner == "" {
			return
		}
	}
	s.t.Fatal("等待 Agent 空闲超时")
}

// defaultProject returns the ID of the default project.
func (s *server) defaultProject() string {
	return s.console(200, "GET", "/context", nil)["project"].(map[string]any)["id"].(string)
}

// createAgent creates an Agent in the default project through the console API and returns its ID.
func (s *server) createAgent(name string) string {
	s.t.Helper()
	provider, model := s.firstModel()
	ag := s.console(201, "POST", "/projects/"+s.defaultProject()+"/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": provider, "modelId": model}, "prompt": "你是测试助手"})
	return ag["id"].(string)
}

func (s *server) firstModel() (string, string) {
	provider := s.console(200, "GET", "/model-providers", nil)["providers"].([]any)[0].(map[string]any)["id"].(string)
	model := s.console(200, "GET", "/model-providers/"+provider+"/models", nil)["models"].([]any)[0].(map[string]any)["id"].(string)
	return provider, model
}

// agentEnv reads the environment fakepi wrote when it started and extracts the Agent address and token the server
// injected.
func (w *world) agentEnv() map[string]string {
	w.t.Helper()
	b, err := os.ReadFile(w.dump)
	if err != nil {
		w.t.Fatal(err)
	}
	var d struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		w.t.Fatal(err)
	}
	if d.Env["LOOPCTL_AGENT_TOKEN"] == "" || d.Env["LOOPCTL_AGENT_URL"] == "" {
		w.t.Fatalf("Pi 的环境中没有 Agent 令牌：%v", d.Env)
	}
	return map[string]string{"LOOPCTL_AGENT_URL": d.Env["LOOPCTL_AGENT_URL"], "LOOPCTL_AGENT_TOKEN": d.Env["LOOPCTL_AGENT_TOKEN"]}
}

// loopctl runs loopctl in-process with an environment containing only the given variables.
type loopctl struct {
	t     *testing.T
	vars  map[string]string
	home  string
	stdin string
}

func newLoopctl(t *testing.T, vars map[string]string) *loopctl {
	if vars == nil {
		vars = map[string]string{}
	}
	return &loopctl{t: t, vars: vars, home: t.TempDir()}
}

type result struct {
	Code   int
	Stdout string
	OK     bool             `json:"ok"`
	Data   json.RawMessage  `json:"data"`
	Error  *output.Error    `json:"error"`
	Warn   []output.Warning `json:"warnings"`
	Meta   output.Meta      `json:"meta"`
}

func (l *loopctl) run(args ...string) result {
	l.t.Helper()
	var out bytes.Buffer
	sys := &cli.System{
		Stdin: strings.NewReader(l.stdin), Stdout: &out,
		Getenv:  func(k string) string { return l.vars[k] },
		HomeDir: func() (string, error) { return l.home, nil },
		Version: buildinfo.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
	}
	var r result
	r.Code = cli.Run(context.Background(), commands.Registry(), args, sys)
	r.Stdout = out.String()
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		l.t.Fatalf("%v：输出不是 JSON：%s", args, r.Stdout)
	}
	return r
}

// ok asserts success with no warnings and returns data.
func (r result) ok(t *testing.T) map[string]any {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("应成功，得到退出码 %d：%s", r.Code, r.Stdout)
	}
	if len(r.Warn) != 0 || r.Meta.ServerVersion != buildinfo.Version() {
		t.Fatalf("服务端版本应与 loopctl 一致且没有警告：%s", r.Stdout)
	}
	var m map[string]any
	_ = json.Unmarshal(r.Data, &m)
	return m
}

func (r result) fails(t *testing.T, code string, exit int) {
	t.Helper()
	if r.Code != exit || r.Error == nil || r.Error.Code != code {
		t.Fatalf("应以 %s（退出码 %d）失败，得到 %d：%s", code, exit, r.Code, r.Stdout)
	}
}

// selfSigned generates a self-signed certificate for the external port and returns the paths of the certificate,
// the private key and the CA file (which is the certificate itself).
func selfSigned(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loopctl-e2e"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile, certFile
}

// issueToken creates an external identity through the console API (reusing it if it already exists) and issues a
// token, returning the identity ID, the token ID and the token.
func (s *server) issueToken(identityID, name string, body any) (string, string, string) {
	s.t.Helper()
	if identityID == "" {
		identityID = s.console(201, "POST", "/external-identities", map[string]any{"name": name})["id"].(string)
	}
	issued := s.console(201, "POST", "/external-identities/"+identityID+"/tokens", body)
	return identityID, issued["token"].(map[string]any)["id"].(string), issued["secret"].(string)
}
