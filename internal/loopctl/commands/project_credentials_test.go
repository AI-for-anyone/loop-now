package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func TestProjectCredentialInvalidInputZeroDial(t *testing.T) {
	inputs := []string{
		``, `null`, `[]`, `{}`, `{"type":"api_key"}`, `{"type":"api_key","key":null}`, `{"type":"api_key","key":1}`,
		`{"type":"api_key","key":" "}`, `{"type":"api_key","key":"sentinel","metadata":{}}`,
		`{"type":"api_key","key":"sentinel","expectedRevision":1}`, `{"type":"api_key","key":"sentinel","provider":"anthropic"}`,
		`{"credential":{"type":"api_key","key":"sentinel"}}`, `{"type":"api_key","key":"sentinel","key":"other"}`,
		`{"type":"api_key","type":"api_key","key":"sentinel"}`, `{"type":"api_key","key":"sentinel"} {}`,
		`{"type":"oauth","access":"sentinel","refresh":"sentinel"}`, `{"type":"cli","fields":{"token":"sentinel"}}`,
		"{\"type\":\"api_key\",\"key\":\"\xffsentinel\"}",
	}
	for i, input := range inputs {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			rec := newRecorder(t, true)
			e := newEnv(t)
			e.vars[conn.EnvURL] = rec.srv.URL
			e.vars[conn.EnvToken] = extToken
			e.vars[conn.EnvCAFile] = rec.caFile(t)
			e.block = true
			e.stdin = input
			r := e.run("project", "model-account", "create", "named-project", "--provider", "anthropic", "--name", "label", "--from-file", "-")
			r.fails(t, output.CodeInvalidArgument, 2)
			if e.dialCount() != 0 || strings.Contains(r.Stdout, "sentinel") {
				t.Fatal("invalid input dialed or leaked secret")
			}
		})
	}
	for _, input := range []string{`{"type":"cli","fields":null}`, `{"type":"cli","fields":{}}`, `{"type":"cli","fields":{"token":null}}`, `{"type":"cli","fields":{"token":1}}`, `{"type":"cli","fields":{"token":"sentinel","token":"other"}}`, `{"type":"cli","fields":{"token":"sentinel"},"expectedRevision":1}`, `{"type":"api_key","key":"sentinel"}`} {
		s, e := credentialStub(t)
		e.stdin = input
		e.run("project", "cli-account", "replace", "named-project", credentialAccountID, "--expected-revision", "1", "--from-file", "-", "--yes").fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != 0 {
			t.Fatal("invalid CLI input resolved project")
		}
	}
}
func TestProjectCredentialParameters(t *testing.T) {
	for _, args := range [][]string{
		{"project", "model-account", "get", "named-project", "display name"},
		{"project", "model-account", "rename", "named-project", credentialAccountID, "--name", "label", "--expected-revision", "0"},
		{"project", "model-account", "delete", "named-project", credentialAccountID, "--expected-revision", "9223372036854775808", "--yes"},
		{"project", "model-account", "create", "named-project", "--provider", "anthropic", "--name", "label", "--key", "sentinel", "--from-file", "-"},
		{"project", "model-account", "login", "named-project", "--provider", "openai-codex", "--name", "label", "--account-id", credentialAccountID, "--expected-revision", "1"},
		{"project", "model-account", "login", "named-project", "--provider", "openai-codex", "--account-id", credentialAccountID},
		{"project", "model-account", "login", "named-project", "--provider", "openai-codex", "--account-id", credentialAccountID, "--expected-revision", "1"},
		{"agent", "credential-selection", "set", "named-agent", "--provider", "anthropic", "--cli", "gh", "--account-id", credentialAccountID, "--expected-config-version", "1"},
		{"agent", "credential-selection", "set", "named-agent", "--provider", "anthropic", "--account-id", "display label", "--expected-config-version", "1"},
		{"agent", "credential-selection", "clear", "named-agent", "--provider", "anthropic", "--expected-config-version", "1"},
		{"agent", "credential-selection", "clear", "named-agent", "--expected-config-version", "1", "--yes"},
	} {
		s, e := credentialStub(t)
		r := e.run(args...)
		if r.Code != 2 {
			t.Fatalf("%v: %s", args, r.Stdout)
		}
		if len(s.Requests()) != 0 {
			t.Fatal("invalid args made request")
		}
		if strings.Contains(r.Stdout, "sentinel") {
			t.Fatal("secret flag value echoed")
		}
	}
}

func TestProjectCredentialSecretOutputProtection(t *testing.T) {
	secret := "synthetic-quote-\"-雪-key"
	for _, status := range []int{200, 422, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.Header().Set(conn.HeaderVersion, testVersion)
				w.WriteHeader(status)
				if status == 200 {
					_ = json.NewEncoder(w).Encode(map[string]any{"unsafe": secret, "nested": []any{secret}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "validation_failed", "message": "echo " + secret, "details": map[string]any{"unsafe": secret}, "fields": map[string]string{"credential": secret}}})
				}
			}))
			defer srv.Close()
			e := newEnv(t)
			e.vars[conn.EnvURL] = srv.URL
			e.vars[conn.EnvToken] = extToken
			e.vars[conn.EnvCAFile] = writeFile(t, "ca.pem", string(pemCert(srv)))
			raw, _ := json.Marshal(map[string]any{"type": "api_key", "key": secret})
			e.stdin = string(raw)
			r := e.run("project", "model-account", "create", credentialProjectID, "--provider", "anthropic", "--name", "label", "--from-file", "-")
			if status == 200 {
				r.ok(t)
			} else if r.Env.Error == nil {
				t.Fatal("missing error")
			}
			var decoded any
			_ = json.Unmarshal([]byte(r.Stdout), &decoded)
			b, _ := json.Marshal(decoded)
			if strings.Contains(string(b), "synthetic-quote") || !strings.Contains(r.Stdout, "[REDACTED]") {
				t.Fatal("credential appeared in output")
			}
		})
	}
}
func TestProjectCredentialUnknownWriteNotRetried(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_, _ = io.ReadAll(r.Body)
		c, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = c.Close()
	}))
	defer srv.Close()
	e := newEnv(t)
	e.vars[conn.EnvURL] = srv.URL
	e.vars[conn.EnvToken] = extToken
	e.vars[conn.EnvCAFile] = writeFile(t, "ca.pem", string(pemCert(srv)))
	e.vars[conn.EnvRetryMax] = "3"
	e.stdin = `{"type":"api_key","key":"synthetic-key"}`
	r := e.run("project", "model-account", "create", credentialProjectID, "--provider", "anthropic", "--name", "duplicate allowed", "--from-file", "-")
	if r.Code == 0 || count.Load() != 1 {
		t.Fatalf("unknown POST: calls=%d %s", count.Load(), r.Stdout)
	}
	if !strings.Contains(r.Stdout, "写入结果未知") {
		t.Fatal("must state uncertain outcome")
	}
	if strings.Contains(r.Stdout, "synthetic-key") {
		t.Fatal("request secret leaked")
	}
}
