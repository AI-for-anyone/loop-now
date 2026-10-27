// Package clitest 是 loopctl 测试用的契约桩服务：按 OpenAPI 契约校验 loopctl 发出的每个请求，
// 只返回事先按契约校验过的样例响应。契约中不存在的路径、不合契约的请求或样例都会让测试失败。
// 只在测试中使用，不进入 loopctl 二进制。
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

// Sample 是一个样例响应。Body 编码为 JSON；Raw 不为空时原样返回（例如文件内容）。
type Sample struct {
	Status int
	Body   any
	Raw    []byte
	Header http.Header
}

// Recorded 是桩服务收到的一个请求。
type Recorded struct {
	Op       string // 契约中的操作，形如 "GET /agents/{agentId}"
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// Stub 是契约桩服务。
type Stub struct {
	t       testing.TB
	Server  *httptest.Server
	Version string // 响应头 Loop-Now-Version 的值；为空时不带这个头（不符合契约，只用于故意违约的自测）
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

// Load 加载并校验 api 目录下的契约文件。
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

// NewExternal 启动对外端口的桩服务：TLS，基址 /api/v1，契约为 api/openapi.yaml。
func NewExternal(t testing.TB) *Stub { return newStub(t, "openapi.yaml", "/api/v1", true) }

// NewAgent 启动 Agent 接口的桩服务：回环上的明文 HTTP，基址 /agent/v1，契约为 api/agent-openapi.yaml。
func NewAgent(t testing.TB) *Stub { return newStub(t, "agent-openapi.yaml", "/agent/v1", false) }

func newStub(t testing.TB, file, base string, tlsOn bool) *Stub {
	t.Helper()
	s := &Stub{t: t, Version: "test-version", base: base, scheme: "http", doc: Load(t, file), samples: map[string][]Sample{}}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.Server.Config.ErrorLog = log.New(io.Discard, "", 0) // 证书校验失败的用例会产生握手错误日志
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

// Access 返回契约中操作的 x-access（console 或 console-and-external）；没有声明时为空。
func (s *Stub) Access(op string) string {
	method, path, _ := strings.Cut(op, " ")
	item := s.doc.Paths.Find(path)
	if item == nil || item.GetOperation(method) == nil {
		return ""
	}
	v, _ := item.GetOperation(method).Extensions["x-access"].(string)
	return v
}

// URL 返回服务端根地址（对应 loopctl 的 --server）。
func (s *Stub) URL() string { return s.Server.URL }

// CAFile 把桩服务的证书写成 PEM 文件并返回路径，供 --ca-file 使用。
func (s *Stub) CAFile() string {
	p := filepath.Join(s.t.TempDir(), "ca.pem")
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Server.Certificate().Raw})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		s.t.Fatal(err)
	}
	return p
}

// On 为一个操作设置样例响应；有多个样例时按顺序返回，最后一个重复使用。样例先按契约校验。
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

// checkSample 按契约校验样例的状态码与 JSON 响应体。
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

// Failures 取出并清空已记录的问题（用于测试“故意违约时会失败”）。
func (s *Stub) Failures() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.failed
	s.failed = nil
	return out
}

// Requests 返回收到的全部请求。
func (s *Stub) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

// Operations 返回契约中的全部操作（不含基址），按名称排序。
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
	// 服务端收到的请求 URL 不含协议与主机，契约路由按完整地址匹配 servers。
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
	// 整个响应（状态码、响应头与响应体）再按契约校验一次。
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
