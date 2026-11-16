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
		t.Fatalf("should record a problem containing %q, got %v", substr, f)
	}
}

func TestStubCatchesViolations(t *testing.T) {
	s := NewExternal(t)
	c := client(s)

	t.Run("request that breaks the contract", func(t *testing.T) {
		s.On("POST /platform/agents", Sample{Status: 201, Body: nil})
		s.Failures()
		resp, err := c.Post(s.URL()+"/api/v1/platform/agents", "application/json", bytes.NewReader([]byte(`{"name":"dev"}`)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "the request does not match the contract")
	})
	t.Run("path not in the contract", func(t *testing.T) {
		resp, err := c.Get(s.URL() + "/api/v1/no-such-thing")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "the operation is not in the contract")
	})
	t.Run("sample that breaks the contract", func(t *testing.T) {
		s.On("GET /platform/agents", Sample{Status: 200, Body: map[string]any{"agents": []any{map[string]any{"id": "not-a-uuid"}}}})
		expectFailure(t, s, "Sample GET /platform/agents → 200 does not match the contract")
	})
	t.Run("status code not in the contract", func(t *testing.T) {
		s.On("GET /platform/agents", Sample{Status: 418, Body: map[string]any{}})
		expectFailure(t, s, "the contract has no status code 418")
	})
	t.Run("no sample set", func(t *testing.T) {
		resp, err := c.Get(s.URL() + "/api/v1/skills")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expectFailure(t, s, "no sample response for this operation")
	})
	t.Run("valid request and sample", func(t *testing.T) {
		s.On("GET /cli-templates", Sample{Status: 200, Body: map[string]any{"templates": []any{}}})
		resp, err := c.Get(s.URL() + "/api/v1/cli-templates")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if f := s.Failures(); len(f) != 0 {
			t.Fatalf("should have no problems: %v", f)
		}
		if resp.Header.Get("Loop-Now-Version") == "" || resp.Header.Get("Loop-Now-Request-Id") == "" {
			t.Fatal("the response should carry the version and request ID headers")
		}
	})
}
