package clitest

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
)

func client(s *Stub) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(s.Server.Certificate())
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func expectFailure(t *testing.T, s *Stub, substr string) {
	t.Helper()
	f := s.Failures()
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), substr) {
		t.Fatalf("应记录包含 %q 的问题，得到 %v", substr, f)
	}
}

func TestStubCatchesViolations(t *testing.T) {
	s := NewExternal(t)
	c := client(s)

	t.Run("不合契约的请求", func(t *testing.T) {
		s.On("POST /platform/agents", Sample{Status: 201, Body: nil})
		s.Failures()
		resp, err := c.Post(s.URL()+"/api/v1/platform/agents", "application/json", bytes.NewReader([]byte(`{"name":"dev"}`)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "请求不符合契约")
	})
	t.Run("契约中没有的路径", func(t *testing.T) {
		resp, err := c.Get(s.URL() + "/api/v1/no-such-thing")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "契约中没有这个操作")
	})
	t.Run("不合契约的样例", func(t *testing.T) {
		s.On("GET /platform/agents", Sample{Status: 200, Body: map[string]any{"agents": []any{map[string]any{"id": "not-a-uuid"}}}})
		expectFailure(t, s, "样例 GET /platform/agents → 200 不符合契约")
	})
	t.Run("契约中没有的状态码", func(t *testing.T) {
		s.On("GET /platform/agents", Sample{Status: 418, Body: map[string]any{}})
		expectFailure(t, s, "契约中没有状态码 418")
	})
	t.Run("没有设置样例", func(t *testing.T) {
		resp, err := c.Get(s.URL() + "/api/v1/skills")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "没有为这个操作设置样例响应")
	})
	t.Run("合规的请求与样例", func(t *testing.T) {
		s.On("GET /cli-templates", Sample{Status: 200, Body: map[string]any{"templates": []any{}}})
		resp, err := c.Get(s.URL() + "/api/v1/cli-templates")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if f := s.Failures(); len(f) != 0 {
			t.Fatalf("不应有问题：%v", f)
		}
		if resp.Header.Get("Loop-Now-Version") == "" || resp.Header.Get("Loop-Now-Request-Id") == "" {
			t.Fatal("响应应带版本与请求 ID 头")
		}
	})
}
