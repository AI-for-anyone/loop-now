// Package clitest is the contract stub server for loopctl tests: it checks every request loopctl sends against the
// OpenAPI contract and only returns sample responses that were checked against the contract beforehand. A path that
// is not in the contract, a request that does not match the contract, or a sample that does not match all fail the
// test.
// It is used only in tests and does not end up in the loopctl binary.
package clitest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"loop-now/internal/testutil/testenv"
)

// Sample is a sample response. Body is encoded as JSON; when Raw is not nil it is returned as is (for example file
// content).
type Sample struct {
	Status int
	Body   any
	Raw    []byte
	Header http.Header
}

// Recorded is one request the stub server received.
type Recorded struct {
	Op       string // the operation in the contract, such as "GET /agents/{agentId}"
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// Stub is the contract stub server.
type Stub struct {
	t       testing.TB
	Server  *httptest.Server
	Version string // Loop-Now-Version response header; empty omits it (breaks the contract, only for deliberate self-tests)
	base    string
	scheme  string
	doc     *openapi3.T
	router  routers.Router

	mu       sync.Mutex
	samples  map[string][]Sample
	requests []Recorded
	failed   []string
}

func schemaOpts() []openapi3.SchemaValidationOption {
	return []openapi3.SchemaValidationOption{openapi3.EnableJSONSchema2020()}
}

// Load loads and validates a contract file in the api directory.
func Load(t testing.TB, file string) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join(testenv.RepoRoot(), "api", file))
	if err != nil {
		t.Fatalf("加载契约 %s 失败：%v", file, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("契约 %s 本身不合法：%v", file, err)
	}
	return doc
}

// NewExternal starts a stub server for the external port: TLS, base path /api/v1, contract api/openapi.yaml.
func NewExternal(t testing.TB) *Stub { return newStub(t, "openapi.yaml", "/api/v1", true) }

// NewAgent starts a stub server for the Agent API: plain HTTP on loopback, base path /agent/v1, contract
// api/agent-openapi.yaml.
func NewAgent(t testing.TB) *Stub { return newStub(t, "agent-openapi.yaml", "/agent/v1", false) }

func newStub(t testing.TB, file, base string, tlsOn bool) *Stub {
	t.Helper()
	s := &Stub{t: t, Version: "test-version", base: base, scheme: "http", doc: Load(t, file), samples: map[string][]Sample{}}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.Server.Config.ErrorLog = log.New(io.Discard, "", 0) // cases where certificate verification fails produce handshake error logs
	if tlsOn {
		s.scheme = "https"
		s.Server.StartTLS()
	} else {
		s.Server.Start()
	}
	t.Cleanup(s.Server.Close)
	s.doc.Servers = openapi3.Servers{{URL: s.Server.URL + s.base}}
	router, err := legacy.NewRouter(s.doc)
	if err != nil {
		t.Fatalf("创建契约路由失败：%v", err)
	}
	s.router = router
	t.Cleanup(func() {
		if f := s.Failures(); len(f) > 0 {
			t.Errorf("契约桩发现 %d 处问题：\n%s", len(f), strings.Join(f, "\n"))
		}
	})
	return s
}

// Access returns the x-access of an operation in the contract (console or console-and-external); empty when it is
// not declared.
func (s *Stub) Access(op string) string {
	method, path, _ := strings.Cut(op, " ")
	item := s.doc.Paths.Find(path)
	if item == nil || item.GetOperation(method) == nil {
		return ""
	}
	v, _ := item.GetOperation(method).Extensions["x-access"].(string)
	return v
}

// URL returns the server root address (what loopctl's --server takes).
func (s *Stub) URL() string { return s.Server.URL }

// CAFile writes the stub server's certificate to a PEM file and returns its path, for use with --ca-file.
func (s *Stub) CAFile() string {
	p := filepath.Join(s.t.TempDir(), "ca.pem")
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Server.Certificate().Raw})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		s.t.Fatal(err)
	}
	return p
}

// On sets the sample responses for an operation; with several samples they are returned in order and the last one
// is reused. Samples are checked against the contract first.
func (s *Stub) On(op string, samples ...Sample) {
	s.t.Helper()
	for _, smp := range samples {
		if err := s.checkSample(op, smp); err != nil {
			s.record("样例 %s → %d 不符合契约：%v", op, smp.Status, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples[op] = samples
}

// checkSample checks a sample's status code and JSON response body against the contract.
func (s *Stub) checkSample(op string, smp Sample) error {
	method, path, ok := strings.Cut(op, " ")
	if !ok {
		return fmt.Errorf("操作应写作 \"METHOD /path\"")
	}
	item := s.doc.Paths.Find(path)
	if item == nil || item.GetOperation(method) == nil {
		return fmt.Errorf("契约中没有这个操作")
	}
	ref := item.GetOperation(method).Responses.Status(smp.Status)
	if ref == nil || ref.Value == nil {
		return fmt.Errorf("契约中没有状态码 %d", smp.Status)
	}
	if smp.Raw != nil || smp.Body == nil {
		return nil
	}
	mt := ref.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil {
		return fmt.Errorf("契约中这个状态码没有 JSON 响应体")
	}
	b, _ := json.Marshal(smp.Body)
	var v any
	_ = json.Unmarshal(b, &v)
	return mt.Schema.Value.VisitJSON(v, schemaOpts()...)
}

func (s *Stub) record(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, fmt.Sprintf(format, args...))
}

// Failures returns and clears the problems recorded so far (used to test that breaking the contract on purpose
// fails).
func (s *Stub) Failures() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.failed
	s.failed = nil
	return out
}

// Requests returns all requests received.
func (s *Stub) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

// Operations returns all operations in the contract (without the base path), sorted by name.
func (s *Stub) Operations() []string {
	var out []string
	for path, item := range s.doc.Paths.Map() {
		for method := range item.Operations() {
			out = append(out, strings.ToUpper(method)+" "+path)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Stub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := Recorded{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: body}
	// The request URL a server receives has no scheme or host, while the contract router matches servers against
	// the full address.
	r.URL.Scheme, r.URL.Host = s.scheme, r.Host
	route, params, err := s.router.FindRoute(r)
	if err != nil {
		s.record("%s %s：契约中没有这个操作：%v", r.Method, r.URL.Path, err)
		s.append(rec)
		http.Error(w, "not in contract", http.StatusNotFound)
		return
	}
	rec.Op = r.Method + " " + route.Path
	s.append(rec)
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, SchemaValidationOptions: schemaOpts(), MultiError: true}
	in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
	if err := openapi3filter.ValidateRequest(r.Context(), in); err != nil {
		s.record("%s：请求不符合契约：%v", rec.Op, err)
	}
	smp, ok := s.next(rec.Op)
	if !ok {
		s.record("%s：测试没有为这个操作设置样例响应", rec.Op)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	h := w.Header()
	if s.Version != "" {
		h.Set("Loop-Now-Version", s.Version)
	}
	h.Set("Loop-Now-Request-Id", requestID())
	for k, vs := range smp.Header {
		h[k] = vs
	}
	var out []byte
	switch {
	case smp.Raw != nil:
		out = smp.Raw
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", "application/octet-stream")
		}
	case smp.Body != nil:
		out, _ = json.Marshal(smp.Body)
		h.Set("Content-Type", "application/json")
	}
	// Check the whole response (status code, headers and body) against the contract once more.
	opts.IncludeResponseStatus = true
	resp := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: smp.Status, Header: h.Clone(), Body: io.NopCloser(bytes.NewReader(out)), Options: opts}
	if err := openapi3filter.ValidateResponse(r.Context(), resp); err != nil {
		s.record("%s → %d：响应不符合契约：%v", rec.Op, smp.Status, err)
	}
	w.WriteHeader(smp.Status)
	w.Write(out)
}

func (s *Stub) append(r Recorded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
}

func (s *Stub) next(op string) (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.samples[op]
	if len(list) == 0 {
		return Sample{}, false
	}
	smp := list[0]
	if len(list) > 1 {
		s.samples[op] = list[1:]
	}
	return smp, true
}

func requestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
