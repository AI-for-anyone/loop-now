package api_test

import (
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
)

func TestTaskLarkSwitchFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(schema string, value any, valid bool) {
		t.Helper()
		err := doc.Components.Schemas[schema].Value.VisitJSON(value, openapi3.EnableJSONSchema2020())
		if (err == nil) != valid {
			t.Fatalf("%s valid=%t: %v", schema, valid, err)
		}
	}
	copy := func(v map[string]any) map[string]any {
		b, _ := json.Marshal(v)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	id := func() string { return uuid.NewString() }
	member := func() map[string]any {
		return map[string]any{"agentId": id(), "runtimeId": id(), "expectedBindingRevision": float64(0)}
	}
	in := map[string]any{"actionId": id(), "expectedTaskVersion": float64(1), "expectedNodeRunId": id(), "expectedAgentId": id(), "expectedRuntimeId": id(), "expectedLarkBindingRevision": float64(0), "expectedMembers": []any{member(), member()}, "targetAccountId": id(), "targetVersionId": id(), "expectedAccountRevision": float64(1)}
	check("TaskLarkSwitchInput", in, true)
	for key := range in {
		bad := copy(in)
		bad[key] = nil
		check("TaskLarkSwitchInput", bad, false)
		delete(bad, key)
		check("TaskLarkSwitchInput", bad, false)
	}
	for _, field := range []string{"credential", "scope", "baseSnapshotSHA", "confirm", "userId"} {
		bad := copy(in)
		bad[field] = "rejected"
		check("TaskLarkSwitchInput", bad, false)
	}
	for _, members := range []any{[]any{}, []any{member()}, []any{member(), member(), member()}} {
		bad := copy(in)
		bad["expectedMembers"] = members
		check("TaskLarkSwitchInput", bad, false)
	}
	now := "2026-10-20T00:00:00Z"
	identity := map[string]any{"brand": "feishu", "appId": "app-fixture", "openId": "ou-fixture", "displayName": "user", "grantedScopes": nil, "verifiedAt": now}
	showMember := func(role string) map[string]any {
		return map[string]any{"agentId": id(), "runtimeId": id(), "role": role, "previousBindingRevision": float64(0), "baseSnapshotSHA": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	}
	out := map[string]any{"actionId": id(), "taskId": id(), "sourceNodeRunId": id(), "sourceAgentId": id(), "sourceRuntimeId": id(), "status": "pending", "reservationTaskVersion": float64(2), "target": map[string]any{"accountId": id(), "versionId": id(), "accountRevision": float64(1), "identity": identity, "runtimeVariantVersion": "lark-cli-user-1.0.94-loop-authsidecar.1"}, "members": []any{showMember("publisher"), showMember("followup")}, "bindingRevision": nil, "continuation": nil, "dispatchStatus": nil, "recoveryInstruction": nil, "errorCode": nil, "errorMessage": nil, "createdAt": now, "finishedAt": nil}
	check("TaskLarkSwitchReceipt", out, true)
	bad := copy(out)
	bad["dispatchStatus"] = "queued"
	check("TaskLarkSwitchReceipt", bad, false)
	out["status"] = "succeeded"
	out["bindingRevision"] = float64(1)
	out["continuation"] = map[string]any{"nodeRunId": id(), "attempt": float64(2), "runId": id()}
	out["recoveryInstruction"] = "仅继续QA，保留已有成果"
	out["finishedAt"] = now
	for _, status := range []string{"queued", "starting", "running", "aborting", "completed", "failed", "interrupted"} {
		out["dispatchStatus"] = status
		check("TaskLarkSwitchReceipt", out, true)
	}
	bad = copy(out)
	bad["continuation"] = nil
	check("TaskLarkSwitchReceipt", bad, false)
	bad = copy(out)
	bad["target"].(map[string]any)["identity"].(map[string]any)["grantedScopes"] = []any{}
	check("TaskLarkSwitchReceipt", bad, true)
	for _, path := range []string{"/tasks/{taskId}/lark-account-switches", "/tasks/{taskId}/lark-account-switches/{actionId}"} {
		item := doc.Paths.Find(path)
		if item == nil {
			t.Fatal("missing console route")
		}
		if agent.Paths.Find(path) != nil {
			t.Fatal("Agent management route exposed")
		}
		for _, op := range item.Operations() {
			if op.Extensions["x-access"] != "console" {
				t.Fatal("management route not console-only")
			}
		}
	}
	for name, value := range map[string]map[string]any{
		"TaskSubmitInput": {"actionId": id(), "output": map[string]any{}, "edgeId": nil},
		"TaskFailInput":   {"actionId": id(), "reason": "execution failed"},
		"InboxSendInput":  {"to": "@console", "text": "question"},
	} {
		check(name, value, true)
		value["runId"] = id()
		check(name, value, true)
		value["runId"] = nil
		check(name, value, false)
		value["runId"] = uuid.Nil.String()
		check(name, value, false)
	}
}
