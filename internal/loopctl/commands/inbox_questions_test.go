package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/conn"
)

func TestInboxQuestionsBothIdentities(t *testing.T) {
	for _, setup := range []struct {
		name string
		fn   func(*testing.T) (*clitest.Stub, *env)
	}{{"agent", agentStub}, {"external", stub}} {
		t.Run(setup.name, func(t *testing.T) {
			s, e := setup.fn(t)
			response := inboxMessage(agentParticipant(), consoleParticipant())
			response["question"] = inboxQuestion("open")
			s.On("POST /inbox/messages", clitest.Sample{Status: 201, Body: response})
			s.On("POST /inbox/messages/{messageId}/reply", clitest.Sample{Status: 201, Body: response})
			got := e.run("inbox", "send", "@console", "--text", "merge?", "--option", "合并", "--option=暂缓").ok(t)
			if !reflect.DeepEqual(got, response) || !reflect.DeepEqual(lastBody(t, s), map[string]any{"to": "@console", "text": "merge?", "options": []any{"合并", "暂缓"}}) {
				t.Fatal("ordinary question input/output changed")
			}
			before := len(s.Requests())
			e.run("inbox", "send", "默认项目/dev#"+taskUUID, "--text", "question", "--option", "继续", "--node-run", nodeUUID).ok(t)
			if len(s.Requests()) != before+1 || lastBody(t, s)["nodeRunId"] != nodeUUID {
				t.Fatal("explicit nodeRunId not preserved or auto-read performed")
			}
			e.run("inbox", "reply", messageUUID, "--answer", "2").ok(t)
			if !reflect.DeepEqual(lastBody(t, s), map[string]any{"optionId": "2"}) {
				t.Fatal("answer-only fabricated body/node")
			}
			e.stdin = "extra context"
			e.run("inbox", "reply", messageUUID, "--answer", "1", "--text-file", "-", "--option", "再确认", "--option", "结束", "--node-run", nodeUUID, "--when-busy", "queue").ok(t)
			if !reflect.DeepEqual(lastBody(t, s), map[string]any{"optionId": "1", "text": "extra context", "options": []any{"再确认", "结束"}, "nodeRunId": nodeUUID, "whenBusy": "queue"}) {
				t.Fatal("answer plus new question lost fields/order")
			}
			e.run("inbox", "reply", messageUUID, "--text", "new question", "--option", "继续").ok(t)
			if lastBody(t, s)["nodeRunId"] != nil {
				t.Fatal("ordinary new question inferred Task sender")
			}
			e.run("inbox", "send", "默认项目/dev#"+taskUUID, "--text", "ordinary message").ok(t)
			if !reflect.DeepEqual(lastBody(t, s), map[string]any{"to": "默认项目/dev#" + taskUUID, "text": "ordinary message"}) {
				t.Fatal("ordinary message gained question/dispatch fields")
			}
			count := len(s.Requests())
			for _, args := range [][]string{
				{"inbox", "reply", messageUUID},
				{"inbox", "reply", messageUUID, "--option", "yes"},
				{"inbox", "reply", messageUUID, "--answer", "1", "--answer", "2"},
				{"inbox", "reply", messageUUID, "--answer", "1", "--text", "x", "--text-file", "-"},
				{"inbox", "send", "@console", "--text", "q", "--option="},
				{"inbox", "send", "@console", "--text", "q", "--option", "yes", "--node-run", "bad"},
				{"inbox", "reply", messageUUID, "--answer", "1", "--node-run", nodeUUID},
			} {
				r := e.run(args...)
				if r.Code != 2 {
					t.Fatal("invalid command accepted", args, r.Stdout)
				}
			}
			if len(s.Requests()) != count {
				t.Fatal("invalid question command sent request")
			}
			help := e.run("inbox", "reply", "--help").ok(t)
			seen := map[string]bool{}
			for _, f := range help["flags"].([]any) {
				flag := f.(map[string]any)
				seen[flag["name"].(string)] = flag["repeatable"].(bool)
			}
			if !seen["option"] || seen["answer"] || seen["text"] || seen["node-run"] || len(s.Requests()) != count {
				t.Fatal("help repeatability changed or caused network")
			}

		})
	}
}

func TestInboxQuestionErrorsAndProjection(t *testing.T) {
	for _, setup := range []struct {
		name string
		fn   func(*testing.T) (*clitest.Stub, *env)
	}{{"agent", agentStub}, {"external", stub}} {
		t.Run(setup.name, func(t *testing.T) {
			s, e := setup.fn(t)
			for _, tc := range []struct{ code, state string }{{"already_answered", "answered"}, {"question_expired", "expired"}, {"question_closed", "closed"}} {
				details := map[string]any{"question": inboxQuestion(tc.state)}
				body := apiError(tc.code, "original question unavailable")
				body["error"].(map[string]any)["details"] = details
				s.On("POST /inbox/messages/{messageId}/reply", clitest.Sample{Status: 409, Body: body})
				before := len(s.Requests())
				r := e.run("inbox", "reply", messageUUID, "--answer", "2")
				r.fails(t, tc.code, 8)
				var got any
				if err := json.Unmarshal(r.Env.Error.Details, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, details) || r.Env.Meta.Attempts != 1 || len(s.Requests()) != before+1 {
					t.Fatal("409 question details changed or write retried")
				}
			}
			response := inboxMessage(consoleParticipant(), agentParticipant())
			response["question"] = inboxQuestion("answered")
			response["answerTo"] = map[string]any{"messageId": messageUUID, "optionId": "2", "optionLabel": "暂缓"}
			s.On("POST /inbox/messages/{messageId}/read", clitest.Sample{Status: 200, Body: response})
			if got := e.run("inbox", "read", messageUUID).ok(t); !reflect.DeepEqual(got, response) {
				t.Fatal("question/answer projection changed")
			}
			response = inboxMessage(platformParticipant(), agentParticipant())
			response["notice"] = map[string]any{"type": "question_expired", "messageId": messageUUID, "reason": "到期"}
			s.On("POST /inbox/messages/{messageId}/read", clitest.Sample{Status: 200, Body: response})
			if got := e.run("inbox", "read", messageUUID).ok(t); !reflect.DeepEqual(got, response) {
				t.Fatal("platform projection changed")
			}
			s.On("POST /inbox/messages", clitest.Sample{Status: 403, Body: apiError("forbidden", "platform is not a recipient")})
			e.run("inbox", "send", "@platform", "--text", "x").fails(t, "forbidden", 6)
		})
	}
}

// The strict contract stub accepts only valid request bodies. A raw HTTP stub here
// proves semantic option limits are sent to the server rather than duplicated in CLI.
func TestInboxQuestionOptionValidationStaysOnServer(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan map[string]any, 3)
	errBody := apiError("validation_failed", "options invalid")
	doc := clitest.Load(t, "openapi.yaml")
	if err := doc.Components.Schemas["Error"].Value.VisitJSON(errBody, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Header().Set("Loop-Now-Version", testVersion)
		w.WriteHeader(422)
		_ = json.NewEncoder(w).Encode(errBody)
	}))
	defer server.Close()
	e := newEnv(t)
	e.vars[conn.EnvURL] = server.URL
	e.vars[conn.EnvToken] = extToken
	e.vars[conn.EnvCAFile] = (&recorder{srv: server}).caFile(t)
	for _, options := range [][]string{{"same", "same"}, {strings.Repeat("长", 51)}, {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}} {
		args := []string{"inbox", "send", "@console", "--text", "question"}
		for _, option := range options {
			args = append(args, "--option", option)
		}
		e.run(args...).fails(t, "validation_failed", 9)
		got := <-bodies
		if len(got["options"].([]any)) != len(options) {
			t.Fatal("CLI filtered semantic option values")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("semantic options did not reach server exactly once each")
	}
}

func TestInboxQuestionSharedSchemas(t *testing.T) {
	a, b := clitest.Load(t, "openapi.yaml"), clitest.Load(t, "agent-openapi.yaml")
	for _, name := range []string{"InboxSendInput", "InboxReplyInput", "InboxQuestionOptions", "InboxQuestionOption", "InboxAnswer", "InboxQuestion", "InboxAnswerTo", "InboxNotice", "QuestionClosedDetails", "InboxMessage", "InboxMessageSummary", "InboxParticipant"} {
		left, err := json.Marshal(a.Components.Schemas[name].Value)
		if err != nil {
			t.Fatal(err)
		}
		right, err := json.Marshal(b.Components.Schemas[name].Value)
		if err != nil {
			t.Fatal(err)
		}
		if string(left) != string(right) {
			t.Fatalf("shared question contract differs: %s", name)
		}
	}
}
