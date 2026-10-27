package commands_test

import (
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"loop-now/internal/loopctl/clitest"
)

func sleepRecord(state string, task bool) map[string]any {
	r := map[string]any{
		"id": artifactUUID, "scopeKind": "normal", "agentId": agentUUID,
		"taskId": nil, "runtimeId": nil, "nodeRunId": nil, "sourceRunId": runUUID,
		"state": state, "seconds": 120, "requestedAt": now, "enteredAt": nil, "wakeAt": nil,
		"endedAt": nil, "endReason": nil, "wakeRunId": nil, "reason": nil, "revision": 9,
	}
	if task {
		r["scopeKind"], r["taskId"], r["runtimeId"], r["nodeRunId"] = "task", taskUUID, agentUUID, nodeUUID
	}
	if state != "requested" {
		r["enteredAt"], r["wakeAt"] = now, "2026-10-13T00:02:00Z"
	}
	if state == "woken" {
		r["endedAt"], r["endReason"], r["wakeRunId"] = now, "queued_work", actionUUID
	}
	if state == "cancelled" {
		r["endedAt"], r["endReason"] = now, "task_ended"
	}
	return r
}

func TestSleepFormalSchemas(t *testing.T) {
	for _, file := range []string{"agent-openapi.yaml", "openapi.yaml"} {
		t.Run(file, func(t *testing.T) {
			doc := clitest.Load(t, file)
			check := func(name string, body any) {
				t.Helper()
				if err := doc.Components.Schemas[name].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			for _, task := range []bool{false, true} {
				for _, state := range []string{"requested", "sleeping", "woken", "cancelled"} {
					check("SleepRecord", sleepRecord(state, task))
				}
			}
			check("SleepInput", map[string]any{"runId": runUUID, "actionId": actionUUID, "seconds": 2147483647, "reason": nil})
			check("SleepReceipt", map[string]any{"sleep": sleepRecord("requested", false), "endTurn": true})
			check("SleepState", map[string]any{"currentRunId": nil, "sleep": nil})
			check("SleepState", map[string]any{"currentRunId": runUUID, "sleep": sleepRecord("requested", false)})
			check("SleepState", map[string]any{"currentRunId": nil, "sleep": sleepRecord("sleeping", true)})
			check("TimelineSleepEntry", map[string]any{"type": "sleep", "at": now, "sleep": sleepRecord("sleeping", false)})
			check("SseSleepUpdated", map[string]any{"scopeKind": "normal", "agentId": agentUUID, "taskId": nil, "runtimeId": nil, "nodeRunId": nil, "taskVersion": nil, "sleepRevision": 9, "sleep": nil, "endedSleepId": artifactUUID})
			check("SseSleepUpdated", map[string]any{"scopeKind": "task", "agentId": agentUUID, "taskId": taskUUID, "runtimeId": agentUUID, "nodeRunId": nodeUUID, "taskVersion": 1, "sleepRevision": 9, "sleep": sleepRecord("sleeping", true), "endedSleepId": nil})
		})
	}
}

func TestSleepCommandsFormalContract(t *testing.T) {
	for _, state := range []string{"requested", "sleeping"} {
		s, e := agentStub(t)
		r := sleepRecord(state, true)
		s.On("GET /sleep", clitest.Sample{Status: 200, Body: map[string]any{"currentRunId": nil, "sleep": r}})
		if got := e.run("sleep", "status").ok(t)["sleep"].(map[string]any); got["revision"] != float64(9) || got["nodeRunId"] != nodeUUID || got["state"] != state {
			t.Fatal(got)
		}
		if got := ops(s); !reflect.DeepEqual(got, []string{"GET /sleep"}) {
			t.Fatal(got)
		}
	}
	s, e := agentStub(t)
	s.On("POST /sleep", clitest.Sample{Status: 202, Body: map[string]any{"sleep": sleepRecord("requested", false), "endTurn": true}})
	r := e.run(sleepArgs()...)
	r.ok(t)
	if r.Env.Meta.Attempts != 1 || !reflect.DeepEqual(ops(s), []string{"POST /sleep"}) {
		t.Fatal(r.Stdout, ops(s))
	}
	for _, tc := range []struct {
		status, exit int
		code         string
	}{{401, 5, "unauthenticated"}, {403, 6, "forbidden"}, {409, 8, "sleep_conflict"}, {422, 9, "validation_failed"}} {
		s, e := agentStub(t)
		s.On("POST /sleep", clitest.Sample{Status: tc.status, Body: map[string]any{"error": map[string]any{"code": tc.code, "message": "测试拒绝"}}})
		e.run(sleepArgs()...).fails(t, tc.code, tc.exit)
		if !reflect.DeepEqual(ops(s), []string{"POST /sleep"}) {
			t.Fatal(ops(s))
		}
	}
}
