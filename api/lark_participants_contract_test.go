package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
)

func TestLarkParticipantsConsoleOnlyContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, v any, valid bool) {
		t.Helper()
		err := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (err == nil) != valid {
			t.Fatalf("%s valid=%v: %v", name, valid, err)
		}
	}
	participant := map[string]any{"agentId": uuid.NewString(), "runtimeId": uuid.NewString(), "baseSnapshotSHA": strings.Repeat("a", 64), "larkAllowed": true, "bindingRevision": float64(0), "prepared": false, "available": true, "unavailableReason": nil, "stepIds": []any{"followup"}}
	check("TaskLarkSwitchParticipant", participant, true)
	participant["larkAllowed"], participant["available"], participant["unavailableReason"] = nil, false, "snapshot_invalid"
	check("TaskLarkSwitchParticipant", participant, true)
	for _, key := range []string{"token", "ciphertext", "sessionFile", "appSecret", "currentCredential"} {
		participant[key] = "forbidden"
		check("TaskLarkSwitchParticipant", participant, false)
		delete(participant, key)
	}
	participant["bindingRevision"] = float64(-1)
	check("TaskLarkSwitchParticipant", participant, false)
	participant["bindingRevision"] = float64(0)
	now := "2026-10-20T00:00:00Z"
	detail := map[string]any{"sleepingInstances": []any{}, "task": map[string]any{"id": uuid.NewString(), "projectId": uuid.NewString(), "title": "t", "status": "blocked", "currentNodeRunId": nil, "currentStepId": nil, "currentAgentId": nil, "reason": nil, "createdAt": now, "updatedAt": now, "endedAt": nil, "link": "/tasks/t", "version": float64(1)}, "request": map[string]any{}, "workflow": map[string]any{"startStepId": "qa", "steps": []any{map[string]any{"id": "qa", "title": "QA", "description": "", "agentId": uuid.NewString(), "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}}, "edges": []any{}}, "nodes": []any{}, "artifacts": []any{}, "cleanup": "none"}
	check("ConsoleTaskDetail", detail, true) // old backend absence is supported.
	detail["larkSwitchParticipants"] = []any{}
	check("ConsoleTaskDetail", detail, true)
	detail["larkSwitchParticipants"] = []any{participant}
	check("ConsoleTaskDetail", detail, true)
	check("TaskDetail", detail, false) // Agent/shared/storage projection remains narrow.
	detail["larkSwitchParticipants"] = nil
	check("ConsoleTaskDetail", detail, false)
	operation := doc.Paths.Find("/tasks/{taskId}").Get
	if operation.Responses.Status(200).Value.Content["application/json"].Schema.Ref != "#/components/schemas/ConsoleTaskDetail" {
		t.Fatal("console route lost projection")
	}
	raw, _ := json.Marshal(agent.Paths)
	if strings.Contains(string(raw), "ConsoleTaskDetail") {
		t.Fatal("console projection reachable by Agent route")
	}
	for _, path := range []string{"/tasks/{taskId}/lark-account-switches", "/tasks/{taskId}/lark-account-switches/{actionId}"} {
		if doc.Paths.Find(path) == nil {
			t.Fatal("original action routes lost")
		}
	}
}
