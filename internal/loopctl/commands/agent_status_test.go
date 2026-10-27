package commands_test

import (
	"loop-now/internal/loopctl/clitest"
	"net/url"
	"testing"
)

func TestProjectAgentStatusCommandRouting(t *testing.T) {
	for _, ref := range []string{agentUUID, "worker", "project/worker", "org/project/worker"} {
		t.Run(ref, func(t *testing.T) {
			s := clitest.NewAgent(t)
			s.On("GET /agents/status", clitest.Sample{Status: 200, Body: agentStatus()})
			e := newEnv(t).agent(s.Server.URL)
			e.run("agent", "status", ref).ok(t)
			requests := s.Requests()
			if len(requests) != 1 || requests[0].Path != "/agent/v1/agents/status" || requests[0].RawQuery != "agent="+url.QueryEscape(ref) {
				t.Fatalf("Agent must not resolve via management directory: %+v", requests)
			}
		})
	}
	// External UUID and address resolution use the original management API.
	for _, ref := range []string{agentUUID, "project/worker"} {
		t.Run("external/"+ref, func(t *testing.T) {
			s, e := stub(t)
			s.On("GET /agents", clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}})
			s.On("GET /agents/{agentId}/status", clitest.Sample{Status: 200, Body: agentStatus()})
			e.run("agent", "status", ref).ok(t)
			requests := s.Requests()
			expected := 1
			if ref != agentUUID {
				expected = 2
			}
			if len(requests) != expected || requests[len(requests)-1].Op != "GET /agents/{agentId}/status" {
				t.Fatal(requests)
			}
		})
	}
}
