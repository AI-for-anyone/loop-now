package commands_test

import (
	"reflect"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func TestAgentPromptExplicitVersion(t *testing.T) {
	s, e := stub(t)
	s.On("PATCH /agents/{agentId}", clitest.Sample{Status: 200, Body: agentDetail()})
	for _, text := range []string{"new prompt", "", "p"} {
		e.stdin = text
		before := len(s.Requests())
		e.run("agent", "update", agentUUID, "--prompt-file", "-", "--expected-prompt-version", "7").ok(t)
		if len(s.Requests()) != before+1 {
			t.Fatal("prompt update read or retried automatically")
		}
		if body := lastBody(t, s); !reflect.DeepEqual(body, map[string]any{"prompt": text, "expectedPromptVersion": float64(7)}) {
			t.Fatal("incorrect explicit prompt condition", body)
		}
	}
	// Names still resolve to IDs; the lookup must not silently choose a Prompt version.
	s.On("GET /agents", clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}})
	e.stdin = "draft"
	before := len(s.Requests())
	e.run("agent", "update", agentAddress, "--name", "renamed", "--prompt-file", "-", "--expected-prompt-version", "3").ok(t)
	requests := s.Requests()[before:]
	if len(requests) != 2 || requests[0].Method != "GET" || requests[1].Method != "PATCH" {
		t.Fatal("address update added unexpected requests")
	}
	if lastBody(t, s)["expectedPromptVersion"] != float64(3) {
		t.Fatal("address resolution replaced editing baseline")
	}
}

func TestAgentPromptVersionErrorsBeforeNetwork(t *testing.T) {
	e := newEnv(t)
	e.vars[conn.EnvURL] = "https://127.0.0.1:1"
	e.vars[conn.EnvToken] = extToken
	e.block = true
	for _, version := range []string{"", "0", "-1", "null", "1.5", "abc", "9223372036854775808"} {
		args := []string{"agent", "update", agentAddress, "--prompt-file", "-"}
		if version != "" {
			args = append(args, "--expected-prompt-version", version)
		}
		e.stdin = ""
		e.run(args...).fails(t, output.CodeInvalidArgument, 2)
	}
	e.run("agent", "update", agentUUID, "--name", "name", "--expected-prompt-version", "1").fails(t, output.CodeInvalidArgument, 2)
	// Creation retains its existing input and does not accept an update precondition flag.
	e.run("agent", "create", "--project", projectUUID, "--name", "name", "--provider", "anthropic", "--model", "claude-x", "--prompt-file", "-", "--expected-prompt-version", "1").fails(t, output.CodeInvalidArgument, 2)
	if len(e.dials) != 0 {
		t.Fatal("invalid Prompt inputs performed network lookup")
	}
}

func TestAgentPromptConflictDoesNotRefreshOrRetry(t *testing.T) {
	s, e := stub(t)
	s.On("PATCH /agents/{agentId}", clitest.Sample{Status: 412, Body: apiError("precondition_failed", "Prompt 已被修改，请核对最新内容")})
	e.stdin = "retained draft"
	r := e.run("agent", "update", agentUUID, "--name", "also drafted", "--prompt-file", "-", "--expected-prompt-version", "1")
	r.fails(t, "precondition_failed", 8)
	if len(s.Requests()) != 1 || r.Env.Meta.Attempts != 1 || lastReq(t, s).Method != "PATCH" {
		t.Fatal("conflict caused automatic read/retry")
	}
	if lastBody(t, s)["expectedPromptVersion"] != float64(1) || e.stdin != "retained draft" {
		t.Fatal("conflict altered editing baseline/draft")
	}
}
