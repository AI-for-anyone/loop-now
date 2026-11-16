// Package contracttest provides OpenAPI contract validation for tests: it loads and validates api/openapi.yaml,
// validates every request and response through a RoundTripper, validates SSE data by event name, and lists all
// operations in the contract.
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

// Validator validates requests, responses and SSE data.
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

// Load loads and validates the console contract api/openapi.yaml.
func Load(t testing.TB) *openapi3.T { return LoadFile(t, "openapi.yaml") }

// LoadFile loads and validates a contract file in the api directory.
func LoadFile(t testing.TB, file string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(testenv.RepoRoot(), "api", file))
	if err != nil {
		t.Fatalf("Could not load contract %s: %v", file, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("Contract %s is itself invalid: %v", file, err)
	}
	return doc
}

// OperationMeta holds the operationId, x-access and x-plugin-scope of one operation in the contract.
type OperationMeta struct {
	OperationID string
	Access      string // x-access; empty when absent
	PluginScope string // x-plugin-scope; empty when plugin tokens cannot call it
}

// LoadOperations returns all operations in a contract file, keyed like "GET /agents/{agentId}".
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

// New creates a validator for the console contract; baseURL is the address of the test server (without /api/v1).
func New(t testing.TB, baseURL string) *Validator {
	return NewFor(t, baseURL, "openapi.yaml", "/api/v1")
}

// NewAgent creates a validator for the Agent-only API contract (api/agent-openapi.yaml, base path /agent/v1).
func NewAgent(t testing.TB, baseURL string) *Validator {
	return NewFor(t, baseURL, "agent-openapi.yaml", "/agent/v1")
}

// NewFor creates a validator for the given contract file and base path.
func NewFor(t testing.TB, baseURL, file, base string) *Validator {
	t.Helper()
	doc := LoadFile(t, file)
	doc.Servers = openapi3.Servers{{URL: baseURL + base}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("Could not create the contract router: %v", err)
	}
	v := &Validator{t: t, doc: doc, router: router}
	t.Cleanup(func() {
		if len(v.failed) > 0 {
			t.Errorf("Contract validation found %d problems:\n%s", len(v.failed), strings.Join(v.failed, "\n"))
		}
	})
	return v
}

func (v *Validator) record(format string, args ...any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.failed = append(v.failed, fmt.Sprintf(format, args...))
}

// Failures returns the recorded validation failures (for testing that a deliberate contract violation fails validation).
func (v *Validator) Failures() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.failed
	v.failed = nil
	return out
}

// Client returns an HTTP client whose traffic is validated against the contract.
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
		v.record("%s %s: operation not found in the contract: %v", req.Method, req.URL.Path, err)
		return rt.base.RoundTrip(req)
	}
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, SchemaValidationOptions: schemaOpts(), MultiError: true}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: opts}
	// Request validation only serves to find mistakes in the tests themselves; cases that deliberately send invalid
	// requests would be reported here, so only problems with valid requests are recorded.
	if err := openapi3filter.ValidateRequest(req.Context(), in); err != nil && req.Header.Get("X-Contract-Invalid-Request") == "" {
		v.record("%s %s: request does not match the contract: %v", req.Method, req.URL.Path, err)
	}
	// A request without a body keeps having no body: replacing it with an empty reader would make the transport send it
	// with an unknown length.
	if req.Body != nil {
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp, nil // event streams are validated separately by ValidateSSE
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	opts.IncludeResponseStatus = true
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(body)), Options: opts}
	if err := openapi3filter.ValidateResponse(req.Context(), out); err != nil {
		v.record("%s %s → %d: response does not match the contract: %v\nResponse: %s", req.Method, req.URL.Path, resp.StatusCode, err, truncate(body))
	}
	return resp, nil
}

func truncate(b []byte) string {
	if len(b) > 500 {
		return string(b[:500]) + "…"
	}
	return string(b)
}

// AgentEventsPath, OverviewEventsPath and InboxEventsPath are the contract's three event stream paths (without /api/v1).
const (
	AgentEventsPath    = "/agents/{agentId}/events"
	OverviewEventsPath = "/overview/events"
	InboxEventsPath    = "/inbox/events"
)

// SSEEventSchemas returns the mapping from event name to schema name in x-sse-events of the Agent event stream.
func (v *Validator) SSEEventSchemas() map[string]string { return v.StreamEventSchemas(AgentEventsPath) }

// StreamEventSchemas returns the mapping from event name to schema name in x-sse-events of the given event stream.
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

// ValidateSSE validates the data of one SSE event of the Agent event stream by its event name.
func (v *Validator) ValidateSSE(event string, data []byte) {
	v.ValidateStreamSSE(AgentEventsPath, event, data)
}

// ValidateStreamSSE validates the data of one SSE event of the given event stream by its event name.
func (v *Validator) ValidateStreamSSE(path, event string, data []byte) {
	name, ok := v.StreamEventSchemas(path)[event]
	if !ok {
		v.record("SSE event %q is not listed in x-sse-events of %s", event, path)
		return
	}
	ref := v.doc.Components.Schemas[name]
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		v.record("SSE event %s: data is not JSON: %v", event, err)
		return
	}
	if err := ref.Value.VisitJSON(value, schemaOpts()...); err != nil {
		v.record("SSE event %s does not match %s: %v\nData: %s", event, name, err, truncate(data))
	}
}

// Operations returns all operations in the contract, like "GET /agents/{agentId}" (without /api/v1).
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
