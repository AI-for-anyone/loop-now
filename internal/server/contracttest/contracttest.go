// Package contracttest 为测试提供 OpenAPI 契约校验：加载并校验 api/openapi.yaml，
// 用 RoundTripper 校验每个请求与响应，按事件名校验 SSE 数据，并列出契约中的全部操作。
package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Validator 校验请求、响应与 SSE 数据。
type Validator struct {
	t      testing.TB
	doc    *openapi3.T
	router routers.Router
	mu     sync.Mutex
	failed []string
}

func schemaOpts() []openapi3.SchemaValidationOption {
	return []openapi3.SchemaValidationOption{openapi3.EnableJSONSchema2020()}
}

// Load 加载并校验控制台契约 api/openapi.yaml。
func Load(t testing.TB) *openapi3.T { return LoadFile(t, "openapi.yaml") }

// LoadFile 加载并校验 api 目录下的契约文件。
func LoadFile(t testing.TB, file string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(testenv.RepoRoot(), "api", file))
	if err != nil {
		t.Fatalf("加载契约 %s 失败：%v", file, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("契约 %s 本身不合法：%v", file, err)
	}
	return doc
}

// OperationMeta 是契约中一个操作的 operationId、x-access 与 x-plugin-scope。
type OperationMeta struct {
	OperationID string
	Access      string // x-access；没有时为空
	PluginScope string // x-plugin-scope；插件令牌不能调用时为空
}

// LoadOperations 返回契约文件中的全部操作，键形如 "GET /agents/{agentId}"。
func LoadOperations(t testing.TB, file string) map[string]OperationMeta {
	t.Helper()
	doc := LoadFile(t, file)
	out := map[string]OperationMeta{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			access, _ := op.Extensions["x-access"].(string)
			scope, _ := op.Extensions["x-plugin-scope"].(string)
			out[strings.ToUpper(method)+" "+path] = OperationMeta{OperationID: op.OperationID, Access: access, PluginScope: scope}
		}
	}
	return out
}

// New 创建控制台契约的校验器；baseURL 是测试服务的地址（不含 /api/v1）。
func New(t testing.TB, baseURL string) *Validator {
	return NewFor(t, baseURL, "openapi.yaml", "/api/v1")
}

// NewAgent 创建 Agent 专用接口契约（api/agent-openapi.yaml，基址 /agent/v1）的校验器。
func NewAgent(t testing.TB, baseURL string) *Validator {
	return NewFor(t, baseURL, "agent-openapi.yaml", "/agent/v1")
}

// NewFor 创建指定契约文件与基址的校验器。
func NewFor(t testing.TB, baseURL, file, base string) *Validator {
	t.Helper()
	doc := LoadFile(t, file)
	doc.Servers = openapi3.Servers{{URL: baseURL + base}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("创建契约路由失败：%v", err)
	}
	v := &Validator{t: t, doc: doc, router: router}
	t.Cleanup(func() {
		if len(v.failed) > 0 {
			t.Errorf("契约校验失败 %d 处：\n%s", len(v.failed), strings.Join(v.failed, "\n"))
		}
	})
	return v
}

func (v *Validator) record(format string, args ...any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.failed = append(v.failed, fmt.Sprintf(format, args...))
}

// Failures 返回已记录的校验失败（用于测试“故意违约时校验会失败”）。
func (v *Validator) Failures() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.failed
	v.failed = nil
	return out
}

// Client 返回经契约校验的 HTTP 客户端。
func (v *Validator) Client() *http.Client {
	return &http.Client{Transport: &roundTripper{v: v, base: http.DefaultTransport}}
}

type roundTripper struct {
	v    *Validator
	base http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	v := rt.v
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	route, params, err := v.router.FindRoute(req)
	if err != nil {
		v.record("%s %s：契约中找不到该操作：%v", req.Method, req.URL.Path, err)
		return rt.base.RoundTrip(req)
	}
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, SchemaValidationOptions: schemaOpts(), MultiError: true}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: opts}
	// 请求校验只用于发现测试本身的错误；故意发送非法请求的用例会在这里报出，所以只记录合法请求的问题。
	if err := openapi3filter.ValidateRequest(req.Context(), in); err != nil && req.Header.Get("X-Contract-Invalid-Request") == "" {
		v.record("%s %s：请求不符合契约：%v", req.Method, req.URL.Path, err)
	}
	// 没有请求体的请求保持没有请求体：换成空读取器会让传输层按长度未知发送。
	if req.Body != nil {
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp, nil // 事件流由 ValidateSSE 单独校验
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	opts.IncludeResponseStatus = true
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(body)), Options: opts}
	if err := openapi3filter.ValidateResponse(req.Context(), out); err != nil {
		v.record("%s %s → %d：响应不符合契约：%v\n响应：%s", req.Method, req.URL.Path, resp.StatusCode, err, truncate(body))
	}
	return resp, nil
}

func truncate(b []byte) string {
	if len(b) > 500 {
		return string(b[:500]) + "…"
	}
	return string(b)
}

// AgentEventsPath、OverviewEventsPath 与 InboxEventsPath 是契约中三条事件流的路径（不含 /api/v1）。
const (
	AgentEventsPath    = "/agents/{agentId}/events"
	OverviewEventsPath = "/overview/events"
	InboxEventsPath    = "/inbox/events"
)

// SSEEventSchemas 返回 Agent 事件流 x-sse-events 中事件名到 schema 名的映射。
func (v *Validator) SSEEventSchemas() map[string]string { return v.StreamEventSchemas(AgentEventsPath) }

// StreamEventSchemas 返回指定事件流 x-sse-events 中事件名到 schema 名的映射。
func (v *Validator) StreamEventSchemas(path string) map[string]string {
	op := v.doc.Paths.Find(path).Get
	raw, _ := json.Marshal(op.Extensions["x-sse-events"])
	var m map[string]struct {
		Ref string `json:"$ref"`
	}
	_ = json.Unmarshal(raw, &m)
	out := map[string]string{}
	for k, r := range m {
		out[k] = strings.TrimPrefix(r.Ref, "#/components/schemas/")
	}
	return out
}

// ValidateSSE 按事件名校验 Agent 事件流中一条 SSE 事件的数据。
func (v *Validator) ValidateSSE(event string, data []byte) {
	v.ValidateStreamSSE(AgentEventsPath, event, data)
}

// ValidateStreamSSE 按事件名校验指定事件流中一条 SSE 事件的数据。
func (v *Validator) ValidateStreamSSE(path, event string, data []byte) {
	name, ok := v.StreamEventSchemas(path)[event]
	if !ok {
		v.record("SSE 事件 %q 不在 %s 的 x-sse-events 中", event, path)
		return
	}
	ref := v.doc.Components.Schemas[name]
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		v.record("SSE 事件 %s 的数据不是 JSON：%v", event, err)
		return
	}
	if err := ref.Value.VisitJSON(value, schemaOpts()...); err != nil {
		v.record("SSE 事件 %s 不符合 %s：%v\n数据：%s", event, name, err, truncate(data))
	}
}

// Operations 返回契约中的全部操作，形如 "GET /agents/{agentId}"（不含 /api/v1）。
func (v *Validator) Operations() []string {
	var out []string
	for path, item := range v.doc.Paths.Map() {
		for method := range item.Operations() {
			out = append(out, strings.ToUpper(method)+" "+path)
		}
	}
	sort.Strings(out)
	return out
}
