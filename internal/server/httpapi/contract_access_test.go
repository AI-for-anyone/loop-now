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

// TestSameRequestsOnExternalPort 对契约中的每个操作，在控制台与对外端口上发送同一个请求（路径参数为随机 ID，请求体为空对象）：
// 对外开放的操作在两边得到相同的状态码，只限控制台的操作在对外端口上返回 403；两边的响应都经契约校验。
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
				t.Errorf("%s 只限控制台，对外端口应返回 403：%d %s", k, extCode, extBody)
			}
			continue
		}
		if sse[k] {
			continue
		}
		conCode, conBody, _ := e.console().do(method, path, body, "X-Contract-Invalid-Request", "1")
		if extCode != conCode {
			t.Errorf("%s 在对外端口与控制台上的状态码应相同：%d / %d\n对外：%s\n控制台：%s", k, extCode, conCode, extBody, conBody)
		}
	}
}

// TestAgentContractMatchesConsole 逐一比对 agent-openapi.yaml 与 openapi.yaml 中的同名结构，以及同名接口的 operationId。
func TestAgentContractMatchesConsole(t *testing.T) {
	agent, console := contracttest.LoadFile(t, "agent-openapi.yaml"), contracttest.LoadFile(t, "openapi.yaml")
	for name, s := range agent.Components.Schemas {
		c, ok := console.Components.Schemas[name]
		if !ok {
			t.Errorf("Agent 契约中的结构 %s 不在控制台契约中", name)
			continue
		}
		a, _ := json.Marshal(s.Value)
		b, _ := json.Marshal(c.Value)
		if !bytes.Equal(a, b) {
			t.Errorf("结构 %s 在两份契约中不一致：\nAgent：%s\n控制台：%s", name, a, b)
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
					t.Errorf("Agent 独有操作不应出现在控制台契约：%s", m.OperationID)
				}
			}
			continue
		}
		if c, ok := cm[k]; !ok || c.OperationID != m.OperationID {
			t.Errorf("Agent 接口 %s 应与控制台的同名接口一致：%+v / %+v", k, m, c)
		}
	}
}

// TestAgentContractCatchesViolation 用 Agent 契约校验一个不合规的服务端，应发现问题。
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
		t.Fatal("不符合 Agent 契约的响应应被发现")
	}
}
