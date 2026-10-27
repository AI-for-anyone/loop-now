package commands_test

import (
	"encoding/json"
	"io"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTaskInboxRunExplicitAndLegacy(t *testing.T) {
	s, e := agentStub(t)
	s.On("POST /inbox/messages/{messageId}/read", clitest.Sample{Status: 200, Body: inboxMessage(consoleParticipant(), agentParticipant())})
	s.On("POST /inbox/messages/{messageId}/reply", clitest.Sample{Status: 201, Body: inboxMessage(agentParticipant(), consoleParticipant())})
	e.run("inbox", "read", messageUUID, "--run", actionUUID).ok(t)
	var body map[string]any
	if err := json.Unmarshal(lastReq(t, s).Body, &body); err != nil || len(body) != 1 || body["runId"] != actionUUID {
		t.Fatal("explicit read run", body, err)
	}
	e.run("inbox", "reply", messageUUID, "--run", actionUUID, "--text", "ordinary reply").ok(t)
	if err := json.Unmarshal(lastReq(t, s).Body, &body); err != nil || body["runId"] != actionUUID || body["text"] != "ordinary reply" {
		t.Fatal("explicit reply run", body, err)
	}
	e.run("inbox", "read", messageUUID).ok(t)
	if len(lastReq(t, s).Body) != 0 {
		t.Fatal("legacy read gained implicit run")
	}
	for _, r := range s.Requests() {
		if r.Method == "GET" {
			t.Fatal("implicit run discovery")
		}
	}
}

func TestTaskInboxRunReadRetryAndReplySinglePost(t *testing.T) {
	for _, action := range []string{"read", "reply"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				bodies = append(bodies, string(raw))
				n := calls.Add(1)
				if action == "reply" {
					c, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						c.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if n == 1 {
					w.WriteHeader(503)
					io.WriteString(w, `{"error":{"code":"service_unavailable","message":"fixture"}}`)
					return
				}
				json.NewEncoder(w).Encode(inboxMessage(consoleParticipant(), agentParticipant()))
			}))
			defer srv.Close()
			e := newEnv(t).agent(srv.URL)
			e.vars[conn.EnvRetryMax] = "3"
			args := []string{"inbox", action, messageUUID, "--run", actionUUID}
			if action == "reply" {
				args = append(args, "--text", "single write")
			}
			result := e.run(args...)
			if action == "read" {
				result.ok(t)
				if calls.Load() != 2 || bodies[0] != bodies[1] {
					t.Fatal("read changed explicit run during retry")
				}
			} else {
				if result.Code == 0 || calls.Load() != 1 {
					t.Fatal("unknown reply write replayed")
				}
			}
		})
	}
}

func TestTaskInboxRunInvalidZeroDial(t *testing.T) {
	for _, run := range []string{"bad", "00000000-0000-0000-0000-000000000000"} {
		for _, action := range []string{"read", "reply"} {
			s, e := agentStub(t)
			args := []string{"inbox", action, messageUUID, "--run", run}
			if action == "reply" {
				args = append(args, "--text", "reply")
			}
			e.run(args...).fails(t, output.CodeInvalidArgument, 2)
			if len(s.Requests()) != 0 {
				t.Fatal("invalid run dialed")
			}
		}
	}
}
