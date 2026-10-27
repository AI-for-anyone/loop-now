package conn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/loopctl/output"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	path string
}

func newCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "loopctl test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	path := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	return &testCA{cert, key, path}
}

func writeCA(t *testing.T) string { return newCA(t).path }

// tlsServer 启动一个使用该 CA 签发证书的 TLS 服务。
func (ca *testCA) tlsServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

type harness struct {
	c      *Client
	sleeps []time.Duration
}

// agentClient 用 Agent 身份连接明文回环服务。
func agentClient(t *testing.T, url string, env map[string]string) *harness {
	t.Helper()
	m := map[string]string{EnvAgentToken: agentToken, EnvAgentURL: url + "/agent/v1"}
	for k, v := range env {
		m[k] = v
	}
	s, e := Resolve(Options{Getenv: envOf(m)})
	if e != nil {
		t.Fatal(e)
	}
	h := &harness{}
	h.c = NewClient(s, ClientOptions{Version: "v1", GOOS: "linux", GOARCH: "arm64", Sleep: func(_ context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		return nil
	}})
	return h
}

// sequence 依次返回给定的状态码，最后一个重复使用，并记录请求次数。
func sequence(n *int32, statuses ...int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(n, 1)) - 1
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		w.Header().Set(HeaderVersion, "v1")
		w.Header().Set(HeaderRequestID, "req-"+string(rune('a'+i)))
		w.WriteHeader(statuses[i])
		if statuses[i] < 300 {
			io.WriteString(w, `{"ok":1}`)
		}
	}
}

func TestRetryReadRequests(t *testing.T) {
	var n int32
	srv := httptest.NewServer(sequence(&n, 503, 200))
	defer srv.Close()
	h := agentClient(t, srv.URL, nil)
	resp, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"inbox", "messages"}, Retry: true})
	if e != nil || string(resp.Data) != `{"ok":1}` {
		t.Fatalf("%v %v", resp, e)
	}
	_, id, attempts, _ := h.c.Meta()
	if attempts != 2 || atomic.LoadInt32(&n) != 2 || id != "req-b" {
		t.Fatalf("请求次数 %d/%d，请求 ID %s", attempts, atomic.LoadInt32(&n), id)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 500*time.Millisecond {
		t.Fatalf("等待 %v", h.sleeps)
	}
}

func TestRetryGivesUp(t *testing.T) {
	var n int32
	srv := httptest.NewServer(sequence(&n, 503))
	defer srv.Close()
	h := agentClient(t, srv.URL, nil)
	_, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
	if e == nil || e.Category != output.CategoryUnavailable || e.HTTPStatus != 503 {
		t.Fatalf("%v", e)
	}
	if _, _, attempts, _ := h.c.Meta(); attempts != 3 || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("应请求 3 次，得到 %d", atomic.LoadInt32(&n))
	}
	if len(h.sleeps) != 2 || h.sleeps[0] != 500*time.Millisecond || h.sleeps[1] != time.Second {
		t.Fatalf("等待 %v", h.sleeps)
	}
}

func TestRetryBackoffCap(t *testing.T) {
	var n int32
	srv := httptest.NewServer(sequence(&n, 502))
	defer srv.Close()
	h := agentClient(t, srv.URL, map[string]string{EnvRetryMax: "3", EnvRetryInitial: "3s", EnvRetryMaxInterval: "5s"})
	h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
	want := []time.Duration{3 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(h.sleeps) != 3 || h.sleeps[0] != want[0] || h.sleeps[1] != want[1] || h.sleeps[2] != want[2] {
		t.Fatalf("等待 %v，应为 %v", h.sleeps, want)
	}
}

func TestNoRetry(t *testing.T) {
	for _, c := range []struct {
		name  string
		env   map[string]string
		req   Request
		codes []int
	}{
		{"写请求不重试", nil, Request{Method: "POST", Path: []string{"inbox", "messages"}}, []int{503, 200}},
		{"重试次数为 0", map[string]string{EnvRetryMax: "0"}, Request{Method: "GET", Path: []string{"x"}, Retry: true}, []int{503, 200}},
		{"其他状态码不重试", nil, Request{Method: "GET", Path: []string{"x"}, Retry: true}, []int{500, 200}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var n int32
			srv := httptest.NewServer(sequence(&n, c.codes...))
			defer srv.Close()
			h := agentClient(t, srv.URL, c.env)
			_, e := h.c.Do(context.Background(), c.req)
			if e == nil || atomic.LoadInt32(&n) != 1 {
				t.Fatalf("应只请求一次并失败：%d %v", atomic.LoadInt32(&n), e)
			}
			if _, _, attempts, _ := h.c.Meta(); attempts != 1 {
				t.Fatalf("attempts %d", attempts)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	var n int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	h := agentClient(t, srv.URL, map[string]string{EnvTimeout: "100ms"})
	start := time.Now()
	_, e := h.c.Do(context.Background(), Request{Method: "POST", Path: []string{"inbox", "messages"}, Body: []byte(`{}`)})
	if e == nil || e.Code != output.CodeTimeout || e.Category != output.CategoryUnavailable {
		t.Fatalf("%v", e)
	}
	if time.Since(start) > 5*time.Second || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("用时 %s，请求 %d 次", time.Since(start), atomic.LoadInt32(&n))
	}
	atomic.StoreInt32(&n, 0)
	h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
	if atomic.LoadInt32(&n) != 3 {
		t.Fatalf("读请求超时后应重试，请求了 %d 次", atomic.LoadInt32(&n))
	}
}

func TestConnectionFailed(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	h := agentClient(t, url, nil)
	_, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
	if e == nil || e.Code != output.CodeConnectionFailed || e.Category != output.CategoryUnavailable {
		t.Fatalf("%v", e)
	}
	if _, _, attempts, responded := h.c.Meta(); attempts != 3 || responded {
		t.Fatalf("attempts %d responded %v", attempts, responded)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	var followed int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&followed, 1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()
	h := agentClient(t, srv.URL, nil)
	_, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
	if e == nil || e.Code != output.CodeUnexpectedRedirect || output.ExitCode(e.Category) != 1 || atomic.LoadInt32(&followed) != 0 {
		t.Fatalf("%v，跟随了 %d 次", e, atomic.LoadInt32(&followed))
	}
}

func TestResponses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		code     string
		category output.Category
	}{
		{"统一错误结构", 409, `{"error":{"code":"agent_not_idle","message":"忙","details":{"a":1},"fields":[{"field":"f","message":"m"}]}}`, "agent_not_idle", output.CategoryConflict},
		{"非 2xx 无法解析", 502, `<html>bad gateway</html>`, output.CodeUnexpectedResponse, output.CategoryUnavailable},
		{"404 无法解析", 404, `not found`, output.CodeUnexpectedResponse, output.CategoryNotFound},
		{"503 带其他 JSON", 503, `{"ok":false,"postgres":{"ok":false},"redis":{"ok":true}}`, output.CodeUnexpectedResponse, output.CategoryUnavailable},
		{"2xx 无法解析", 200, `{"broken"`, output.CodeUnexpectedResponse, output.CategoryInternal},
		{"401", 401, `{"error":{"code":"unauthenticated","message":"令牌无效"}}`, "unauthenticated", output.CategoryUnauthenticated},
		{"403", 403, `{"error":{"code":"forbidden","message":"只限控制台"}}`, "forbidden", output.CategoryForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			}))
			defer srv.Close()
			h := agentClient(t, srv.URL, map[string]string{EnvRetryMax: "0"})
			_, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}, Retry: true})
			if e == nil || e.Code != c.code || e.Category != c.category || e.HTTPStatus != c.status {
				t.Fatalf("%+v", e)
			}
			if c.status == 503 && string(e.Details) != c.body {
				t.Fatalf("JSON 响应体应放进 details：%s", e.Details)
			}
			if c.code == "agent_not_idle" && (string(e.Details) != `{"a":1}` || !strings.Contains(string(e.Fields), `"field":"f"`) || e.Message != "忙") {
				t.Fatalf("没有保留服务端的详情：%+v", e)
			}
		})
	}
	t.Run("204", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		defer srv.Close()
		resp, e := agentClient(t, srv.URL, nil).c.Do(context.Background(), Request{Method: "DELETE", Path: []string{"x"}})
		if e != nil || string(resp.Data) != "null" {
			t.Fatalf("%v %v", resp, e)
		}
	})
	t.Run("超过响应上限", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `"`+strings.Repeat("a", 2000)+`"`)
		}))
		defer srv.Close()
		_, e := agentClient(t, srv.URL, map[string]string{EnvMaxResponse: "1KiB"}).c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}})
		if e == nil || e.Code != output.CodeUnexpectedResponse {
			t.Fatalf("%v", e)
		}
	})
}

func TestRequestHeadersAndPath(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	h := agentClient(t, srv.URL, nil)
	h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"agents", "a/b#c 中"}, Query: map[string][]string{"address": {"默认项目/dev#任务"}}})
	if got.Header.Get("Authorization") != "Bearer "+agentToken || got.Header.Get("User-Agent") != "loopctl/v1 (linux/arm64)" || got.Header.Get("Accept") != "application/json" {
		t.Fatalf("请求头 %v", got.Header)
	}
	if got.URL.EscapedPath() != "/agent/v1/agents/a%2Fb%23c%20%E4%B8%AD" || got.URL.Query().Get("address") != "默认项目/dev#任务" {
		t.Fatalf("路径 %s，查询 %s", got.URL.EscapedPath(), got.URL.RawQuery)
	}
	if strings.Contains(got.URL.String(), "secret") || strings.Contains(got.URL.String(), "AGENT") {
		t.Fatal("URL 中出现令牌")
	}
}

func TestReadInterruptedIsRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, "partial")
			return // 连接在响应体中途关闭
		}
		io.WriteString(w, "complete")
	}))
	defer srv.Close()
	var got string
	h := agentClient(t, srv.URL, nil)
	_, e := h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"f"}, Retry: true, Consume: func(resp *http.Response) error {
		b, err := io.ReadAll(resp.Body)
		got = string(b)
		return err
	}})
	if e != nil || got != "complete" || atomic.LoadInt32(&n) != 2 {
		t.Fatalf("%v %q %d", e, got, atomic.LoadInt32(&n))
	}
}

func TestVersionWarnings(t *testing.T) {
	for _, c := range []struct {
		name, header, code string
	}{
		{"一致", "v1", ""},
		{"不一致", "v2", output.WarnVersionMismatch},
		{"没有版本头", "", output.WarnServerVersionUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.header != "" {
					w.Header().Set(HeaderVersion, c.header)
				}
				io.WriteString(w, `{}`)
			}))
			defer srv.Close()
			h := agentClient(t, srv.URL, nil)
			h.c.Do(context.Background(), Request{Method: "GET", Path: []string{"x"}})
			ws := h.c.Warnings()
			if c.code == "" {
				if len(ws) != 0 {
					t.Fatalf("%v", ws)
				}
				return
			}
			if len(ws) != 1 || ws[0].Code != c.code {
				t.Fatalf("%v", ws)
			}
			if d, ok := ws[0].Details.(map[string]any); c.code == output.WarnVersionMismatch && (!ok || d["serverVersion"] != "v2" || d["clientVersion"] != "v1" || d["downloadPath"] != nil) {
				t.Fatalf("Agent 身份的详情不对：%v", ws[0].Details)
			}
		})
	}
}

func TestTLS(t *testing.T) {
	ca, other := newCA(t), newCA(t)
	srv := ca.tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderVersion, "v2")
		io.WriteString(w, `{"ok":true}`)
	}))
	resolve := func(caFile string) *Client {
		env := map[string]string{EnvToken: extToken, EnvURL: srv.URL + "/"}
		if caFile != "" {
			env[EnvCAFile] = caFile
		}
		s, e := Resolve(Options{ConfigPath: filepath.Join(t.TempDir(), "none"), Getenv: envOf(env)})
		if e != nil {
			t.Fatal(e)
		}
		return NewClient(s, ClientOptions{Version: "v1", GOOS: "darwin", GOARCH: "arm64"})
	}
	t.Run("指定签发它的 CA", func(t *testing.T) {
		c := resolve(ca.path)
		resp, e := c.Do(context.Background(), Request{Method: "GET", Path: []string{"whoami"}})
		if e != nil || string(resp.Data) != `{"ok":true}` {
			t.Fatalf("%v", e)
		}
		ws := c.Warnings()
		if len(ws) != 1 || ws[0].Details.(map[string]any)["downloadPath"] != "/downloads/loopctl/loopctl-darwin-arm64" {
			t.Fatalf("外部身份应给出下载路径：%v", ws)
		}
		if tr := c.hc.Transport.(*http.Transport); tr.TLSClientConfig.RootCAs == nil || tr.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("指定 CA 文件时应只信任该 CA，且不跳过校验")
		}
	})
	for name, caFile := range map[string]string{"指定另一个 CA": other.path, "没有指定 CA（系统根证书）": ""} {
		t.Run(name, func(t *testing.T) {
			_, e := resolve(caFile).Do(context.Background(), Request{Method: "GET", Path: []string{"whoami"}, Retry: true})
			if e == nil || e.Code != output.CodeTLSVerifyFailed || e.Category != output.CategoryConfig {
				t.Fatalf("%v", e)
			}
		})
	}
}
