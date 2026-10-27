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

// agentGet 用 Agent 令牌调用 Agent 专用接口，返回状态码与响应体。
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

	// 上一次运行没有正常作废的 Agent 令牌（例如服务端被强制结束），启动时作废。
	s := w.start(setup)
	a, ag := s.createAgent("开发助手")
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
		t.Fatalf("启动时应作废上一次运行的 Agent 令牌：%v %v", revoked, err)
	}

	// 先对话一轮建立会话，等 Pi 空闲超时退出。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "你好"})
	s.waitIdle(a)
	sessionBefore, err := s.a.Store.GetAgentSession(context.Background(), ag.ID)
	if err != nil || sessionBefore.SessionFile == "" {
		t.Fatalf("应已有会话：%+v %v", sessionBefore, err)
	}
	firstToken := dumpedEnv(t, dump)["LOOPCTL_AGENT_TOKEN"]
	agentBase := strings.TrimSuffix(s.url, "/api/v1") + "/agent/v1"
	if env := dumpedEnv(t, dump); env["LOOPCTL_AGENT_URL"] != agentBase {
		t.Fatalf("注入的接口地址应指向本服务端的 Agent 接口：%s，期望 %s", env["LOOPCTL_AGENT_URL"], agentBase)
	}
	// Agent 的 PATH 最前面是同版本的 loopctl。
	loopctl := filepath.Join(strings.Split(dumpedEnv(t, dump)["PATH"], ":")[0], "loopctl")
	if out, err := exec.Command(loopctl).Output(); err != nil || !strings.Contains(string(out), `"version":"`+buildinfo.Version()+`"`) {
		t.Fatalf("Agent 运行的 loopctl 应与服务端同版本：%s %v", out, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.a.Executor.Process(ag.ID).State != executor.ProcessStopped && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// 空闲超时时先把进程移出进程表，停止的第一步作废令牌，两者相差几毫秒。
	code := 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if code, _ = agentGet(t, agentBase, firstToken, "/whoami"); code == 401 {
			break
		}
	}
	if code != 401 {
		t.Fatalf("Pi 退出后旧令牌应失效：%d", code)
	}

	// Pi 已退出时收到 Inbox 消息：启动 Pi，接着原会话开始一轮提醒。
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/开发助手", "text": "请看一下"})
	s.waitIdle(a)
	reply := s.replies(a)
	last := reply[len(reply)-1]
	if last.Origin != "inbox_reminder" || last.Run.Status != "completed" {
		t.Fatalf("提醒那一轮应完成：%+v", last)
	}
	sessionAfter, _ := s.a.Store.GetAgentSession(context.Background(), ag.ID)
	if sessionAfter.SessionFile != sessionBefore.SessionFile || sessionAfter.Epoch != sessionBefore.Epoch {
		t.Fatalf("提醒应接着原会话：%+v → %+v", sessionBefore, sessionAfter)
	}
	// 新进程有新令牌，可以用它读取自己的收件箱。
	tok := dumpedEnv(t, dump)["LOOPCTL_AGENT_TOKEN"]
	if tok == "" || tok == firstToken {
		t.Fatalf("新进程应有新令牌")
	}
	code, body := agentGet(t, agentBase, tok, "/whoami")
	if who := decode[apitypes.Whoami](t, body); code != 200 || who.Kind != "agent" || who.Address == nil || *who.Address != "默认组织/默认项目/开发助手" {
		t.Fatalf("Agent 的 whoami：%d %s", code, body)
	}
	code, body = agentGet(t, agentBase, tok, "/inbox/messages?unread=true")
	if page := decode[apitypes.InboxMessagePage](t, body); code != 200 || len(page.Messages) != 1 || page.Messages[0].Preview != "请看一下" {
		t.Fatalf("Agent 列出收件箱：%d %s", code, body)
	}

	// 服务端重启：重启前签发的令牌都不再有效。
	s.stop()
	s = w.start(setup)
	if code, _ := agentGet(t, strings.TrimSuffix(s.url, "/api/v1")+"/agent/v1", tok, "/whoami"); code != 401 {
		t.Fatalf("服务端重启后旧令牌应失效：%d", code)
	}
}

func TestStartupRejectsLoopctlMismatch(t *testing.T) {
	w := newWorld(t)
	cfg := w.cfg
	cfg.LoopctlDistDir = t.TempDir() // 没有与服务端同版本的产物
	_, err := app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, "text", cfg.LogLevel),
		app.Options{SearchPath: w.schema, HomeDirs: []string{"/nonexistent-home"}})
	if err == nil || !strings.Contains(err.Error(), buildinfo.Version()) {
		t.Fatalf("没有同版本的 loopctl 产物时应拒绝启动，并给出服务端版本：%v", err)
	}
}

// selfSigned 生成 127.0.0.1 的自签证书，返回证书与私钥文件路径以及信任它的证书池。
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

	// 没有令牌：401；纯 HTTP 连接不上（只有 TLS）。
	if resp, _ := get("/api/v1/context", ""); resp.StatusCode != 401 || resp.Header.Get("Loop-Now-Version") != buildinfo.Version() {
		t.Fatalf("对外端口未带令牌应返回 401 并带版本头：%d %v", resp.StatusCode, resp.Header)
	}
	if resp, err := http.Get("http://" + s.a.ExternalAddr() + "/api/v1/context"); err == nil && resp.StatusCode < 400 {
		t.Fatalf("对外端口不应接受明文 HTTP：%d", resp.StatusCode)
	}

	x := decode[apitypes.ExternalIdentity](t, s.must(201, "POST", "/external-identities", map[string]any{"name": "ci-bot"}))
	issued := decode[apitypes.ExternalTokenIssued](t, s.must(201, "POST", "/external-identities/"+x.ID.String()+"/tokens", nil))
	resp, body := get("/api/v1/whoami", issued.Secret)
	if who := decode[apitypes.Whoami](t, body); resp.StatusCode != 200 || who.Kind != "external" {
		t.Fatalf("对外端口的 whoami：%d %s", resp.StatusCode, body)
	}
	if resp, _ := get("/api/v1/external-identities", issued.Secret); resp.StatusCode != 403 {
		t.Fatalf("只限控制台的接口在对外端口上应返回 403：%d", resp.StatusCode)
	}

	// 下载与服务端同版本的 loopctl：与清单中的 SHA-256 一致；下载同样需要令牌。
	if resp, _ := get("/downloads/loopctl/manifest.json", ""); resp.StatusCode != 401 {
		t.Fatalf("下载也需要令牌：%d", resp.StatusCode)
	}
	resp, body = get("/downloads/loopctl/manifest.json", issued.Secret)
	var m struct {
		Version   string `json:"version"`
		Artifacts []struct {
			OS, Arch, File, SHA256 string
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &m); err != nil || resp.StatusCode != 200 || m.Version != buildinfo.Version() {
		t.Fatalf("下载清单：%d %s", resp.StatusCode, body)
	}
	for _, a := range m.Artifacts {
		if a.OS != "darwin" || a.Arch != "arm64" {
			continue
		}
		resp, bin := get("/downloads/loopctl/"+a.File, issued.Secret)
		sum := sha256.Sum256(bin)
		if resp.StatusCode != 200 || hex.EncodeToString(sum[:]) != a.SHA256 || resp.Header.Get("X-Content-SHA256") != a.SHA256 {
			t.Fatalf("下载 darwin/arm64 的 loopctl：%d", resp.StatusCode)
		}
	}
	if resp, _ := get("/downloads/loopctl/../manifest.json", issued.Secret); resp.StatusCode == 200 {
		t.Fatal("清单中没有的路径不应下载成功")
	}
	// 控制台接口不因开启对外端口而改变：仍免登录。
	s.must(200, "GET", "/context", nil)
}

// reminders 返回 Agent 的平台提醒执行：状态与回复。
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
	a, ag := s.createAgent("开发助手")
	s.waitIdle(a)

	// 重启时有排队中的提醒：重启后照常执行一次。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s 很长的回复"})
	s.waitRunning(a)
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "排队中"})
	if got := reminders(t, s, ag.ID); len(got) != 1 || got[0] != "queued" {
		t.Fatalf("应有一轮排队中的提醒：%v", got)
	}
	s.stop()
	s = w.start(nil)
	defer func() { s.stop() }()
	s.waitIdle(a)
	if got := reminders(t, s, ag.ID); len(got) != 1 || got[0] != "completed" {
		t.Fatalf("重启后排队中的提醒应照常执行一次：%v", got)
	}

	// Redis 数据丢失：提醒按数据库补回，且不重复执行。
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s 又一个很长的回复"})
	s.waitRunning(a)
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "丢失前一"})
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": ag.ID.String(), "text": "丢失前二"})
	if err := s.a.Redis.Del(context.Background(), s.a.Coord.Keys().QueueNormal(ag.ID.String())).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // 对账间隔为 500ms
	s.must(202, "POST", a+"/messages", map[string]any{"text": "结束长回复", "whenBusy": "interrupt"})
	s.waitIdle(a)
	if got := reminders(t, s, ag.ID); len(got) != 2 || got[1] != "completed" {
		t.Fatalf("Redis 数据丢失后提醒应按数据库补回并只执行一次：%v", got)
	}
}
