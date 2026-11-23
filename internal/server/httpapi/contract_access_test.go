package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/contracttest"
)

// TestSameRequestsOnExternalPort sends the same request for every operation in the contract to both the console and the
// external port (path parameters are random IDs, the body is an empty object): externally open operations get the same
// status code on both, console-only operations return 403 on the external port; responses on both sides are validated
// against the contract.
func TestSameRequestsOnExternalPort(t *testing.T) {
	e := newEnv(t)
	_, tok := e.identity("ci-bot")
	ext := e.external(tok)
	meta := contracttest.LoadOperations(t, "openapi.yaml")
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	param := regexp.MustCompile(`\{[^}]+\}`)
	sse := map[string]bool{"GET /agents/{agentId}/events": true, "GET /overview/events": true, "GET /inbox/events": true}
	for _, k := range keys {
		method, tmpl, _ := strings.Cut(k, " ")
		path := param.ReplaceAllStringFunc(tmpl, func(p string) string {
			if p == "{cli}" {
				return "gh"
			}
			return uuid.Must(uuid.NewV7()).String()
		})
		if tmpl == "/model-providers/{providerId}/models" {
			path = "/model-providers/nonexistent/models"
		}
		if strings.HasSuffix(tmpl, "/files") || strings.HasSuffix(tmpl, "/files/content") {
			path += "?path=a.md"
		}
		if tmpl == "/agents" {
			path += "?address=nonexistent"
		}
		var body any
		if method != http.MethodGet && method != http.MethodDelete {
			body = map[string]any{}
		}
		extCode, extBody, _ := ext.do(method, path, body, "X-Contract-Invalid-Request", "1")
		if meta[k].Access == "console" {
			if extCode != http.StatusForbidden || !strings.Contains(string(extBody), `"forbidden"`) {
				t.Errorf("%s is console only, the external port should return 403: %d %s", k, extCode, extBody)
			}
			continue
		}
		if sse[k] {
			continue
		}
		conCode, conBody, _ := e.console().do(method, path, body, "X-Contract-Invalid-Request", "1")
		if extCode != conCode {
			t.Errorf("%s should have the same status code on the external port and the console: %d / %d\nexternal: %s\nconsole: %s", k, extCode, conCode, extBody, conBody)
		}
	}
}

// TestAgentContractMatchesConsole compares each same-named structure in agent-openapi.yaml and openapi.yaml, and the
// operationId of same-named endpoints.
func TestAgentContractMatchesConsole(t *testing.T) {
	agent, console := contracttest.LoadFile(t, "agent-openapi.yaml"), contracttest.LoadFile(t, "openapi.yaml")
	for name, s := range agent.Components.Schemas {
		c, ok := console.Components.Schemas[name]
		if !ok {
			t.Errorf("schema %s in the Agent contract is not in the console contract", name)
			continue
		}
		a, _ := json.Marshal(s.Value)
		b, _ := json.Marshal(c.Value)
		if !bytes.Equal(a, b) {
			t.Errorf("schema %s differs between the two contracts:\nAgent: %s\nconsole: %s", name, a, b)
		}
	}
	am, cm := contracttest.LoadOperations(t, "agent-openapi.yaml"), contracttest.LoadOperations(t, "openapi.yaml")
	for k, m := range am {
		method, path, _ := strings.Cut(k, " ")
		op := agent.Paths.Find(path).GetOperation(method)
		if op.Extensions["x-agent-only"] == true {
			// The two namespaces may have the same relative path for different
			// operations; no Agent-only operation ID may appear in console.
			for _, c := range cm {
				if c.OperationID == m.OperationID {
					t.Errorf("an Agent-only operation should not appear in the console contract: %s", m.OperationID)
				}
			}
			continue
		}
		if c, ok := cm[k]; !ok || c.OperationID != m.OperationID {
			t.Errorf("Agent endpoint %s should match the console endpoint of the same name: %+v / %+v", k, m, c)
		}
	}
}

// TestAgentContractCatchesViolation validates a non-conforming server with the Agent contract and expects problems to
// be found.
func TestAgentContractCatchesViolation(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind": "robot"}`))
	}))
	defer broken.Close()
	v := contracttest.NewAgent(t, broken.URL)
	resp, err := v.Client().Get(broken.URL + "/agent/v1/whoami")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(v.Failures()) == 0 {
		t.Fatal("a response that violates the Agent contract should be caught")
	}
}
