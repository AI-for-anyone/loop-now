package commands_test

import (
	"net/url"
	"reflect"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

// agentStub starts a contract stub for the Agent API and returns an environment that connects to it with the Agent
// identity.
func agentStub(t *testing.T) (*clitest.Stub, *env) {
	s := clitest.NewAgent(t)
	return s, newEnv(t).agent(s.URL())
}

func lastReq(t *testing.T, s *clitest.Stub) clitest.Recorded {
	t.Helper()
	reqs := s.Requests()
	if len(reqs) == 0 {
		t.Fatal("the stub server received no request")
	}
	return reqs[len(reqs)-1]
}

// Both identities use the Inbox commands: the Agent identity goes through /agent/v1 (api/agent-openapi.yaml), the
// external identity through /api/v1 (api/openapi.yaml).
func TestInboxBothIdentities(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T) (*clitest.Stub, *env)
		base  string
		me    map[string]any
	}{
		{"Agent identity", agentStub, "/agent/v1", agentParticipant()},
		{"external identity", stub, "/api/v1", externalParticipant()},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, e := c.setup(t)
			other := consoleParticipant()
			s.On("GET /inbox/messages", clitest.Sample{Status: 200, Body: inboxPage("c2", inboxSummary(other, c.me, false), inboxSummary(other, c.me, true))})
			s.On("POST /inbox/messages/{messageId}/read", clitest.Sample{Status: 200, Body: inboxMessage(other, c.me)},
				clitest.Sample{Status: 404, Body: apiError("not_found", "message not found")})

			// By default both read and unread are included; --unread lists only unread; --cursor pages. Listing does
			// not call the read endpoint.
			d := e.run("inbox", "list").ok(t)
			if len(d["messages"].([]any)) != 2 || lastReq(t, s).RawQuery != "" || lastReq(t, s).Path != c.base+"/inbox/messages" {
				t.Fatalf("%v %+v", d, lastReq(t, s))
			}
			e.run("inbox", "list", "--unread", "--limit", "10").ok(t)
			if q, _ := url.ParseQuery(lastReq(t, s).RawQuery); q.Get("unread") != "true" || q.Get("limit") != "10" {
				t.Fatalf("%v", q)
			}
			e.run("inbox", "list", "--cursor", d["nextCursor"].(string)).ok(t)
			if q, _ := url.ParseQuery(lastReq(t, s).RawQuery); q.Get("cursor") != "c2" {
				t.Fatalf("%v", q)
			}
			for _, r := range s.Requests() {
				if r.Method != "GET" {
					t.Fatalf("listing summaries should not call other endpoints: %+v", r)
				}
			}

			// Read: returns the full text and the sender; for a message that is not one's own the server returns 404.
			r := e.run("inbox", "read", messageUUID)
			if d := r.ok(t); d["text"] != "Build done" || d["from"].(map[string]any)["address"] != "@console" {
				t.Fatalf("%v", d)
			}
			if lastReq(t, s).Op != "POST /inbox/messages/{messageId}/read" || r.Env.Meta.Attempts != 1 {
				t.Fatalf("%+v", lastReq(t, s))
			}
			r = e.run("inbox", "read", messageUUID)
			r.fails(t, "not_found", 7)
			if r.Env.Error.HTTPStatus != 404 {
				t.Fatalf("%s", r.Stdout)
			}
		})
	}
}

func TestInboxSendAndReply(t *testing.T) {
	s, e := agentStub(t)
	me := agentParticipant()
	s.On("POST /inbox/messages",
		clitest.Sample{Status: 201, Body: inboxMessage(me, agentParticipant())},
		clitest.Sample{Status: 201, Body: inboxMessage(me, agentParticipant())},
		clitest.Sample{Status: 201, Body: inboxMessage(me, consoleParticipant())},
		clitest.Sample{Status: 201, Body: inboxMessage(me, externalParticipant())},
		clitest.Sample{Status: 404, Body: apiError("not_found", "recipient address not found")},
		clitest.Sample{Status: 403, Body: apiError("forbidden", "cannot send to an Agent in another organization")})
	s.On("POST /inbox/messages/{messageId}/reply",
		clitest.Sample{Status: 201, Body: inboxMessage(me, consoleParticipant())},
		clitest.Sample{Status: 404, Body: apiError("not_found", "not a participant in the thread")})

	// Sending to an Agent inside a task: the address is passed as is, including # and the task ID after it; without
	// --when-busy the request has no whenBusy.
	e.run("inbox", "send", "Default project/dev#0192f1a2-task", "--text", "How is it going?").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"to": "Default project/dev#0192f1a2-task", "text": "How is it going?"}) {
		t.Fatalf("%v", b)
	}
	// Interrupt specified, with the body text from standard input.
	e.stdin = "Handle this now\n"
	e.run("inbox", "send", "Default project/dev", "--when-busy", "interrupt", "--text-file", "-").ok(t)
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"to": "Default project/dev", "text": "Handle this now\n", "whenBusy": "interrupt"}) {
		t.Fatalf("%v", b)
	}
	// Sending to the console operator and to an external identity on one's own initiative.
	if d := e.run("inbox", "send", "@console", "--text", "Build done").ok(t); d["to"].(map[string]any)["kind"] != "console" {
		t.Fatalf("%v", d)
	}
	e.run("inbox", "send", "@ci-bot", "--text", "Need a new token").ok(t)
	if b := lastBody(t, s); b["to"] != "@ci-bot" {
		t.Fatalf("%v", b)
	}
	// An address that cannot be resolved: 404; across organizations it is refused: 403. Sending is not retried.
	r := e.run("inbox", "send", "Default project/missing", "--text", "x")
	r.fails(t, "not_found", 7)
	r = e.run("inbox", "send", "Other org/Project/dev", "--text", "x")
	r.fails(t, "forbidden", 6)
	if r.Env.Meta.Attempts != 1 {
		t.Fatalf("%s", r.Stdout)
	}

	// Reply: the request body only has the text; 404 when not a participant in the thread.
	e.run("inbox", "reply", messageUUID, "--text", "Got it").ok(t)
	if req := lastReq(t, s); req.Op != "POST /inbox/messages/{messageId}/reply" {
		t.Fatalf("%+v", req)
	}
	if b := lastBody(t, s); !reflect.DeepEqual(b, map[string]any{"text": "Got it"}) {
		t.Fatalf("%v", b)
	}
	e.run("inbox", "reply", messageUUID, "--text", "x").fails(t, "not_found", 7)

	// No request is sent when the arguments are wrong.
	n := len(s.Requests())
	e.run("inbox", "send", "@console", "--text", "a", "--text-file", "-").fails(t, output.CodeInvalidArgument, 2)
	e.run("inbox", "threads").fails(t, output.CodeCommandNotAllowed, 2)
	if len(s.Requests()) != n {
		t.Fatal("no request should be sent for bad arguments or a disallowed identity")
	}
}

// The external identity views the exchange by thread.
func TestInboxThreads(t *testing.T) {
	s, e := stub(t)
	s.On("GET /inbox/threads", clitest.Sample{Status: 200, Body: threadPage()})
	s.On("GET /inbox/threads/{threadId}", clitest.Sample{Status: 200, Body: thread()})
	if d := e.run("inbox", "threads", "--limit", "5").ok(t); d["unreadTotal"] != float64(1) {
		t.Fatalf("%v", d)
	}
	d := e.run("inbox", "thread", threadUUID).ok(t)
	msgs := d["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["from"].(map[string]any)["kind"] != "agent" {
		t.Fatalf("%v", d)
	}
	for _, r := range s.Requests() {
		if r.Method != "GET" {
			t.Fatalf("viewing a thread should not change read state: %+v", r)
		}
	}
}

// whoami: both identities can look up their own address.
func TestWhoami(t *testing.T) {
	s, e := agentStub(t)
	s.On("GET /whoami", clitest.Sample{Status: 200, Body: whoamiBody("agent", "Default org/Default project/dev", "dev")})
	r := e.run("whoami")
	if d := r.ok(t); d["kind"] != "agent" || d["address"] != "Default org/Default project/dev" || r.Env.Meta.Identity != "agent" || r.Env.Meta.ServerVersion == "" {
		t.Fatalf("%s", r.Stdout)
	}
	x, ex := stub(t)
	x.On("GET /whoami", clitest.Sample{Status: 200, Body: whoamiBody("external", "@ci-bot", "ci-bot")})
	if d := ex.run("whoami").ok(t); d["kind"] != "external" || d["address"] != "@ci-bot" {
		t.Fatalf("%v", d)
	}
}

// Invalid token and missing permission: the server's 401 and 403 are reported by category.
func TestAuthErrors(t *testing.T) {
	s, e := agentStub(t)
	s.On("GET /whoami", clitest.Sample{Status: 401, Body: apiError("unauthenticated", "token revoked")})
	e.run("whoami").fails(t, "unauthenticated", 5)
	x, ex := stub(t)
	x.On("GET /skills", clitest.Sample{Status: 403, Body: apiError("forbidden", "console only")})
	ex.run("skill", "list").fails(t, "forbidden", 6)
}

// Referring to an Agent by address: it is encoded following URL rules and GET /agents?address= is called, with the
// address passed as is (tasks 6.1).
func TestAgentAddressLookup(t *testing.T) {
	s, e := stub(t)
	s.On("GET /agents",
		clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}},
		clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}},
		clitest.Sample{Status: 200, Body: map[string]any{"agents": []any{}}})
	s.On("GET /agents/{agentId}", clitest.Sample{Status: 200, Body: agentDetail()})

	for _, addr := range []string{"默认项目/dev 助手", "reviewer"} { // check-english: zh (non-ASCII Agent address in the lookup query)
		e.run("agent", "get", addr).ok(t)
		reqs := s.Requests()
		lookup, detail := reqs[len(reqs)-2], reqs[len(reqs)-1]
		if q, _ := url.ParseQuery(lookup.RawQuery); lookup.Op != "GET /agents" || q.Get("address") != addr {
			t.Fatalf("%+v", lookup)
		}
		if detail.Path != "/api/v1/agents/"+agentUUID {
			t.Fatalf("%+v", detail)
		}
	}
	r := e.run("agent", "get", "Default project/missing#x")
	r.fails(t, output.CodeNotFound, 7)
	if r.Env.Error.HTTPStatus != 0 {
		t.Fatalf("a failed lookup by address should have no httpStatus: %s", r.Stdout)
	}
	if q, _ := url.ParseQuery(lastReq(t, s).RawQuery); q.Get("address") != "Default project/missing#x" {
		t.Fatalf("# should be encoded, not truncated: %s", lastReq(t, s).RawQuery)
	}
}
