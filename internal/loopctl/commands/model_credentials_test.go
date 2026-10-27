package commands_test

import (
	"reflect"
	"strings"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

func TestModelAPIKeyProviderInput(t *testing.T) {
	s, e := stub(t)
	meta := credentialMeta()
	meta["provider"] = "anthropic"
	meta["authMethod"] = "api_key"
	meta["versionId"] = actionUUID
	meta["accountLabel"] = nil
	s.On("PUT /agents/{agentId}/model-credential", clitest.Sample{Status: 200, Body: meta})
	for _, version := range []string{"", `,"expectedConfigVersion":7`} {
		e.stdin = `{"provider":"anthropic","key":"secret-model-key"` + version + `}`
		r := e.run("agent", "credential", "set-model", agentUUID, "--from-file", "-")
		if !reflect.DeepEqual(r.ok(t), meta) || strings.Contains(r.Stdout, "secret-model-key") {
			t.Fatal("model metadata changed or secret exposed")
		}
		body := lastBody(t, s)
		if body["provider"] != "anthropic" || body["key"] != "secret-model-key" || body["env"] != nil {
			t.Fatal("incorrect provider API Key request")
		}
		if version != "" && body["expectedConfigVersion"] != float64(7) {
			t.Fatal("configuration version lost")
		}
	}
	for _, input := range []string{
		`{"Provider":"anthropic","key":"secret"}`, `{"ANTHROPIC_API_KEY":"secret"}`, `{"env":{"ANTHROPIC_API_KEY":"secret"}}`,
		`{"provider":"openai-codex","oauth":{"access":"secret"}}`, `{"provider":"anthropic","key":"secret","refreshToken":"secret"}`,
		`{"provider":"anthropic"}`, `{"key":"secret"}`, `{"provider":null,"key":"secret"}`, `{"provider":"anthropic","key":null}`,
		`{"provider":"anthropic","key":""}`, `{"provider":"anthropic","key":1}`, `null`, `[]`,
		`{"provider":"anthropic","key":"secret","expectedConfigVersion":null}`,
		`{"provider":"anthropic","key":"secret","expectedConfigVersion":0}`,
		`{"provider":"anthropic","key":"secret","expectedConfigVersion":1.5}`,
		`{"provider":"anthropic","key":"secret"} {}`, "\xff",
	} {
		n := len(s.Requests())
		e.stdin = input
		r := e.run("agent", "credential", "set-model", agentUUID, "--from-file", "-")
		r.fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != n || strings.Contains(r.Stdout, "secret") {
			t.Fatal("invalid model credential sent or exposed")
		}
	}
}
