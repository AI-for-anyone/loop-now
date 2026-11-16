package app_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/executor"
)

// agentGet calls the Agent-only API with an Agent token and returns the status code and response body.
func agentGet(t *testing.T, base, token, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func dumpedEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d.Env
}

func TestInboxWithFakePi(t *testing.T) {
	w := newWorld(t)
	dump := filepath.Join(t.TempDir(), "dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	setup := func(c *config.Config) {
		c.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
		c.PiIdleTimeout = 500 * time.Millisecond
	}

	// Agent tokens the previous run did not revoke normally (for example, the server was killed) are revoked at
	// startup.
	s := w.start(setup)
	a, ag := s.createAgent("dev assistant")
	stale := uuid.Must(uuid.NewV7())
	if _, err := s.a.Store.Pool.Exec(context.Background(), `INSERT INTO agent_tokens (id, agent_id, instance_id, secret_hash) VALUES ($1, $2, $3, $4)`,
		stale, ag.ID, uuid.Must(uuid.NewV7()), make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	s.stop()
	s = w.start(setup)
	defer func() { s.stop() }()
	var revoked bool
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM agent_tokens WHERE id = $1`, stale).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("startup should revoke Agent tokens of the previous run: %v %v", revoked, err)
	}

	// First talk for one turn to create a session, and wait for Pi to exit on idle timeout.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "hello"})
	s.waitIdle(a)
	sessionBefore, err := s.a.Store.GetAgentSession(context.Background(), ag.ID)
	if err != nil || sessionBefore.SessionFile == "" {
		t.Fatalf("there should be a session already: %+v %v", sessionBefore, err)
	}
	firstToken := dumpedEnv(t, dump)["LOOPCTL_AGENT_TOKEN"]
	agentBase := strings.TrimSuffix(s.url, "/api/v1") + "/agent/v1"
	if env := dumpedEnv(t, dump); env["LOOPCTL_AGENT_URL"] != agentBase {
		t.Fatalf("the injected API URL should point to this server's Agent API: %s, want %s", env["LOOPCTL_AGENT_URL"], agentBase)
	}
	// The first entry on the Agent's PATH is the loopctl of the same version.
	loopctl := filepath.Join(strings.Split(dumpedEnv(t, dump)["PATH"], ":")[0], "loopctl")
	if out, err := exec.Command(loopctl).Output(); err != nil || !strings.Contains(string(out), `"version":"`+buildinfo.Version()+`"`) {
		t.Fatalf("the loopctl the Agent runs should be the server's version: %s %v", out, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.a.Executor.Process(ag.ID).State != executor.ProcessStopped && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// On idle timeout the process is first removed from the process table, and the first step of stopping revokes the
	// token; the two happen a few milliseconds apart.
	code := 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if code, _ = agentGet(t, agentBase, firstToken, "/whoami"); code == 401 {
			break
		}
	}
	if code != 401 {
		t.Fatalf("the old token should be invalid after Pi exits: %d", code)
	}

	// An Inbox message arrives while Pi has exited: start Pi and begin a reminder turn on the original session.
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": "Default project/dev assistant", "text": "Please take a look"})
	s.waitIdle(a)
	reply := s.replies(a)
	last := reply[len(reply)-1]
	if last.Origin != "inbox_reminder" || last.Run.Status != "completed" {
		t.Fatalf("the reminder turn should complete: %+v", last)
	}
	sessionAfter, _ := s.a.Store.GetAgentSession(context.Background(), ag.ID)
	if sessionAfter.SessionFile != sessionBefore.SessionFile || sessionAfter.Epoch != sessionBefore.Epoch {
		t.Fatalf("the reminder should continue the original session: %+v → %+v", sessionBefore, sessionAfter)
	}
	// The new process has a new token and can use it to read its own Inbox.
	tok := dumpedEnv(t, dump)["LOOPCTL_AGENT_TOKEN"]
	if tok == "" || tok == firstToken {
		t.Fatalf("the new process should have a new token")
	}
	code, body := agentGet(t, agentBase, tok, "/whoami")
	if who := decode[apitypes.Whoami](t, body); code != 200 || who.Kind != "agent" || who.Address == nil || *who.Address != "Default org/Default project/dev assistant" {
		t.Fatalf("Agent whoami: %d %s", code, body)
	}
	code, body = agentGet(t, agentBase, tok, "/inbox/messages?unread=true")
	if page := decode[apitypes.InboxMessagePage](t, body); code != 200 || len(page.Messages) != 1 || page.Messages[0].Preview != "Please take a look" {
		t.Fatalf("Agent lists its Inbox: %d %s", code, body)
	}

	// Server restart: tokens issued before the restart are no longer valid.
	s.stop()
	s = w.start(setup)
	if code, _ := agentGet(t, strings.TrimSuffix(s.url, "/api/v1")+"/agent/v1", tok, "/whoami"); code != 401 {
		t.Fatalf("old tokens should be invalid after a server restart: %d", code)
	}
}

func TestStartupRejectsLoopctlMismatch(t *testing.T) {
	w := newWorld(t)
	cfg := w.cfg
	cfg.LoopctlDistDir = t.TempDir() // no build output of the same version as the server
	_, err := app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, "text", cfg.LogLevel),
		app.Options{HomeDirs: []string{"/nonexistent-home"}})
	if err == nil || !strings.Contains(err.Error(), buildinfo.Version()) {
		t.Fatalf("startup should be refused without a loopctl build of the same version, naming the server version: %v", err)
	}
}

// selfSigned generates a self-signed certificate for 127.0.0.1 and returns the certificate and key file paths and a
// certificate pool that trusts it.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loop-now-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func TestExternalPortTLS(t *testing.T) {
	w := newWorld(t)
	certFile, keyFile, pool := selfSigned(t)
	s := w.start(func(c *config.Config) {
		c.ExternalListenAddr, c.ExternalTLSCert, c.ExternalTLSKey = "127.0.0.1:0", certFile, keyFile
	})
	defer s.stop()
	ext := "https://" + s.a.ExternalAddr()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	get := func(path, token string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", ext+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	// No token: 401; plain HTTP cannot connect (TLS only).
	if resp, _ := get("/api/v1/context", ""); resp.StatusCode != 401 || resp.Header.Get("Loop-Now-Version") != buildinfo.Version() {
		t.Fatalf("the external port should return 401 with the version header without a token: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, err := http.Get("http://" + s.a.ExternalAddr() + "/api/v1/context"); err == nil && resp.StatusCode < 400 {
		t.Fatalf("the external port should not accept plain HTTP: %d", resp.StatusCode)
	}

	x := decode[apitypes.ExternalIdentity](t, s.must(201, "POST", "/external-identities", map[string]any{"name": "ci-bot"}))
	issued := decode[apitypes.ExternalTokenIssued](t, s.must(201, "POST", "/external-identities/"+x.ID.String()+"/tokens", nil))
	resp, body := get("/api/v1/whoami", issued.Secret)
	if who := decode[apitypes.Whoami](t, body); resp.StatusCode != 200 || who.Kind != "external" {
		t.Fatalf("whoami on the external port: %d %s", resp.StatusCode, body)
	}
	if resp, _ := get("/api/v1/external-identities", issued.Secret); resp.StatusCode != 403 {
		t.Fatalf("console-only APIs should return 403 on the external port: %d", resp.StatusCode)
	}

	// Download the loopctl of the same version as the server: it matches the SHA-256 in the manifest; downloads also
	// need a token.
	if resp, _ := get("/downloads/loopctl/manifest.json", ""); resp.StatusCode != 401 {
		t.Fatalf("downloads should need a token too: %d", resp.StatusCode)
	}
	resp, body = get("/downloads/loopctl/manifest.json", issued.Secret)
	var m struct {
		Version   string `json:"version"`
		Artifacts []struct {
			OS, Arch, File, SHA256 string
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &m); err != nil || resp.StatusCode != 200 || m.Version != buildinfo.Version() {
		t.Fatalf("download manifest: %d %s", resp.StatusCode, body)
	}
	for _, a := range m.Artifacts {
		if a.OS != "darwin" || a.Arch != "arm64" {
			continue
		}
		resp, bin := get("/downloads/loopctl/"+a.File, issued.Secret)
		sum := sha256.Sum256(bin)
		if resp.StatusCode != 200 || hex.EncodeToString(sum[:]) != a.SHA256 || resp.Header.Get("X-Content-SHA256") != a.SHA256 {
			t.Fatalf("download loopctl for darwin/arm64: %d", resp.StatusCode)
		}
	}
	if resp, _ := get("/downloads/loopctl/../manifest.json", issued.Secret); resp.StatusCode == 200 {
		t.Fatal("a path not in the manifest should not download")
	}
	// The console API does not change when the external port is enabled: still no login needed.
	s.must(200, "GET", "/context", nil)
}

// reminders returns the statuses of the Agent's platform reminder runs, in queue order.
func reminders(t *testing.T, s *server, agentID uuid.UUID) (statuses []string) {
	t.Helper()
	rows, err := s.a.Store.Pool.Query(context.Background(), `SELECT status FROM runs WHERE agent_id = $1 AND origin = 'inbox_reminder' ORDER BY queue_seq`, agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		_ = rows.Scan(&st)
		statuses = append(statuses, st)
	}
	return statuses
}

func TestReminderRecovery(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, ag := s.createAgent("dev assistant")
	s.waitIdle(a)

	// A reminder is queued at restart: after the restart it runs once as usual.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s a very long reply"})
	s.waitRunning(a)
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "in the queue"})
	if got := reminders(t, s, ag.ID); len(got) != 1 || got[0] != "queued" {
		t.Fatalf("there should be one queued reminder turn: %v", got)
	}
	s.stop()
	s = w.start(nil)
	defer func() { s.stop() }()
	s.waitIdle(a)
	if got := reminders(t, s, ag.ID); len(got) != 1 || got[0] != "completed" {
		t.Fatalf("after the restart the queued reminder should run once as usual: %v", got)
	}

	// Reminders queued while busy are dequeued from the database and run only once.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s another very long reply"})
	s.waitRunning(a)
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "before loss 1"})
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "before loss 2"})
	s.must(202, "POST", a+"/messages", map[string]any{"text": "end the long reply", "whenBusy": "interrupt"})
	s.waitIdle(a)
	if got := reminders(t, s, ag.ID); len(got) != 2 || got[1] != "completed" {
		t.Fatalf("queued reminders should be dequeued from the database and run only once: %v", got)
	}
}
