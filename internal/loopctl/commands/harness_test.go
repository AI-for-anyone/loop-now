package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

const (
	tokenSecret = "SECRETsecretSECRETsecret-_42"
	extToken    = "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_" + tokenSecret
	agentToken  = "lna_0192f1a2b3c47d8e9f0a1b2c3d4e5f61_" + tokenSecret
	agentUUID   = "0192f1a2-0000-7000-8000-000000000001"
	testVersion = "test-version"
)

// result 是一次调用的结果。
type result struct {
	Code   int
	Stdout string
	Env    struct {
		OK       bool             `json:"ok"`
		Data     json.RawMessage  `json:"data"`
		Error    *output.Error    `json:"error"`
		Warnings []output.Warning `json:"warnings"`
		Meta     output.Meta      `json:"meta"`
	}
}

// env 是一次测试的运行环境：环境变量、HOME、标准输入与网络拨号记录。
type env struct {
	t     *testing.T
	vars  map[string]string
	home  string
	stdin string
	block bool // 为 true 时任何拨号都失败并被记录

	mu    sync.Mutex
	dials []string
}

func newEnv(t *testing.T) *env {
	return &env{t: t, vars: map[string]string{}, home: t.TempDir()}
}

// external 设置外部身份的环境变量，连接到契约桩。
func (e *env) external(s *clitest.Stub) *env {
	e.vars[conn.EnvURL] = s.URL()
	e.vars[conn.EnvToken] = extToken
	e.vars[conn.EnvCAFile] = s.CAFile()
	return e
}

// agent 设置 Agent 身份的环境变量。
func (e *env) agent(url string) *env {
	e.vars[conn.EnvAgentURL] = url + "/agent/v1"
	e.vars[conn.EnvAgentToken] = agentToken
	return e
}

func (e *env) system(stdout *bytes.Buffer) *cli.System {
	return &cli.System{
		Stdin:     strings.NewReader(e.stdin),
		Stdout:    stdout,
		Getenv:    func(k string) string { return e.vars[k] },
		HomeDir:   func() (string, error) { return e.home, nil },
		Version:   testVersion,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		Sleep:     func(context.Context, time.Duration) error { return nil },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			e.mu.Lock()
			e.dials = append(e.dials, addr)
			e.mu.Unlock()
			if e.block {
				return nil, errors.New("测试禁止网络")
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	var out bytes.Buffer
	var r result
	r.Code = cli.Run(context.Background(), commands.Registry(), args, e.system(&out))
	r.Stdout = out.String()
	if strings.Count(r.Stdout, "\n") != 1 || !strings.HasSuffix(r.Stdout, "\n") {
		e.t.Fatalf("%v：标准输出应恰好是一行 JSON：%q", args, r.Stdout)
	}
	if err := json.Unmarshal([]byte(r.Stdout), &r.Env); err != nil {
		e.t.Fatalf("%v：输出不是 JSON：%v\n%s", args, err, r.Stdout)
	}
	if r.Env.Warnings == nil {
		e.t.Fatalf("%v：warnings 应总是数组：%s", args, r.Stdout)
	}
	if r.Env.OK != (r.Code == 0) {
		e.t.Fatalf("%v：ok 与退出码不一致：%d %s", args, r.Code, r.Stdout)
	}
	return r
}

func (e *env) dialCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.dials)
}

// ok 断言成功并返回 data 解码后的值。
func (r result) ok(t *testing.T) map[string]any {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("应成功，得到退出码 %d：%s", r.Code, r.Stdout)
	}
	var m map[string]any
	json.Unmarshal(r.Env.Data, &m)
	return m
}

// fails 断言以指定错误码与退出码失败。
func (r result) fails(t *testing.T, code string, exit int) {
	t.Helper()
	if r.Code != exit || r.Env.Error == nil || r.Env.Error.Code != code {
		t.Fatalf("应以 %s（退出码 %d）失败，得到 %d：%s", code, exit, r.Code, r.Stdout)
	}
	if output.ExitCode(r.Env.Error.Category) != exit {
		t.Fatalf("类别 %s 与退出码 %d 不对应", r.Env.Error.Category, exit)
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// lastBody 解码桩服务收到的最后一个请求的 JSON 请求体。
func lastBody(t *testing.T, s *clitest.Stub) map[string]any {
	t.Helper()
	reqs := s.Requests()
	if len(reqs) == 0 {
		t.Fatal("桩服务没有收到请求")
	}
	var m map[string]any
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &m); err != nil {
		t.Fatalf("请求体不是 JSON 对象：%s", reqs[len(reqs)-1].Body)
	}
	return m
}

// ops 返回桩服务收到的请求对应的契约操作。
func ops(s *clitest.Stub) []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Op)
	}
	return out
}

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func pemCert(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}
