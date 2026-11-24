package commands_test

import (
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

// loopctl conversation reads the Agent's own conversation through the Agent API, passing the page position as is; it
// is not available to external identities.
func TestConversationCommand(t *testing.T) {
	const message = "0192f1a2-0000-7000-8000-0000000000f2"
	for _, tc := range []struct {
		argv  []string
		query string
	}{
		{[]string{"conversation"}, ""},
		{[]string{"conversation", "--limit", "5"}, "limit=5"},
		{[]string{"conversation", "--before", message}, "before=" + message},
		{[]string{"conversation", "--after", message, "--limit", "100"}, "after=" + message + "&limit=100"},
	} {
		s := clitest.NewAgent(t)
		s.On("GET /conversation", clitest.Sample{Status: 200, Body: conversationPage()})
		e := newEnv(t).agent(s.Server.URL)
		e.run(tc.argv...).ok(t)
		requests := s.Requests()
		if len(requests) != 1 || requests[0].Path != "/agent/v1/conversation" || requests[0].RawQuery != tc.query {
			t.Fatalf("%v: wrong request %+v", tc.argv, requests)
		}
	}
	s := clitest.NewAgent(t)
	e := newEnv(t).agent(s.Server.URL)
	e.run("conversation", "--before", message, "--after", message).fails(t, output.CodeInvalidArgument, 2)
	if len(s.Requests()) != 0 {
		t.Fatalf("invalid options must not send a request: %+v", s.Requests())
	}
}
