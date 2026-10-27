package api_test

import (
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"testing"
)

func TestTaskNodeInboxFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, v any, valid bool) {
		t.Helper()
		err := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (err == nil) != valid {
			t.Fatalf("%s valid=%v: %v", name, valid, err)
		}
	}
	clone := func(v map[string]any) map[string]any {
		b, _ := json.Marshal(v)
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return out
	}
	id := uuid.NewString()
	now := "2026-10-18T09:00:00Z"
	input := map[string]any{"actionId": id, "expectedAgentId": id, "expectedRuntimeId": id, "text": "hello"}
	check("TaskNodeInboxInput", input, true)
	for _, key := range []string{"actionId", "expectedAgentId", "expectedRuntimeId", "text"} {
		bad := clone(input)
		delete(bad, key)
		check("TaskNodeInboxInput", bad, false)
		bad[key] = nil
		check("TaskNodeInboxInput", bad, false)
	}
	for _, key := range []string{"options", "optionId", "answerTo", "taskVersion", "runId"} {
		bad := clone(input)
		bad[key] = nil
		check("TaskNodeInboxInput", bad, false)
	}
	for _, v := range []any{nil, "invalid"} {
		bad := clone(input)
		bad["whenBusy"] = v
		check("TaskNodeInboxInput", bad, false)
	}
	for _, v := range []string{"", " \n\t"} {
		bad := clone(input)
		bad["text"] = v
		check("TaskNodeInboxInput", bad, false)
	}
	for _, v := range []any{nil, uuid.Nil.String(), "invalid"} {
		check("InboxReadInput", map[string]any{"runId": v}, false)
	}
	check("InboxReadInput", nil, false)
	check("InboxReadInput", map[string]any{}, true)
	check("InboxReadInput", map[string]any{"runId": id}, true)
	check("InboxReadInput", map[string]any{"unknown": id}, false)
	reply := map[string]any{"text": "reply", "runId": id}
	check("InboxReplyInput", reply, true)
	reply["runId"] = nil
	check("InboxReplyInput", reply, false)
	participant := map[string]any{"kind": "console", "address": "@console", "name": "console", "id": nil, "deleted": false, "pluginId": nil, "channelId": nil}
	message := map[string]any{"id": id, "threadId": id, "inReplyTo": nil, "from": participant, "to": participant, "preview": "hello", "whenBusy": "queue", "createdAt": now, "readAt": nil, "text": "hello", "cancelledAt": nil, "task": map[string]any{"taskId": id, "nodeRunId": id, "runtimeId": id}}
	record := map[string]any{"taskId": id, "nodeRunId": id, "agentId": id, "runtimeId": id, "message": message, "runLinks": []any{}, "notExecuted": nil}
	check("TaskNodeInboxRecord", record, true)
	receipt := clone(record)
	delete(receipt, "runLinks")
	delete(receipt, "notExecuted")
	receipt["actionId"] = id
	receipt["status"] = "accepted"
	check("TaskNodeInboxReceipt", receipt, true)
	receipt["runId"] = id
	check("TaskNodeInboxReceipt", receipt, false)
	record["runLinks"] = []any{map[string]any{"runId": id, "role": "read"}}
	check("TaskNodeInboxRecord", record, true)
	record["notExecuted"] = map[string]any{"reason": "target_invalidated", "at": now}
	check("TaskNodeInboxRecord", record, true)
	check("TaskNodeInboxPage", map[string]any{"items": []any{record}, "nextCursor": nil}, true)
	check("TaskNodeInboxPage", map[string]any{"items": nil, "nextCursor": nil}, false)
	summary := map[string]any{"id": id, "origin": "inbox_reminder", "executionMode": "task_conversation", "status": "running", "queuedAt": now, "startedAt": now, "endedAt": nil, "errorCode": nil, "errorMessage": nil}
	usage := map[string]any{"calls": 0, "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}
	timeline := map[string]any{"taskId": id, "nodeRunId": id, "agentId": id, "runtimeId": id, "run": summary, "entries": []any{}, "usage": usage, "nextCursor": nil}
	check("TaskNodeRunTimeline", timeline, true)
	// Task summaries accept their own safe machine code, while the shared ended
	// entry keeps its existing narrow code contract. Unknown entry codes must
	// be projected to null rather than silently broadening the shared enum.
	summary["errorCode"] = "task_conflict"
	ended := map[string]any{"type": "ended", "at": now, "status": "failed", "endReason": nil, "errorCode": "task_conflict"}
	timeline["entries"] = []any{ended}
	check("TaskNodeRunTimeline", timeline, false)
	ended["errorCode"] = nil
	check("TaskNodeRunTimeline", timeline, true)
	ended["errorCode"] = "server_restarted"
	check("TaskNodeRunTimeline", timeline, false)
	ended["errorCode"] = "internal_error"
	check("TaskNodeRunTimeline", timeline, true)
	for _, mode := range []string{"", "task_node", "task_conversation"} {
		summary["executionMode"] = mode
		check("TaskNodeRunSummary", summary, true)
	}
	summary["executionMode"] = "normal"
	check("TaskNodeRunSummary", summary, false)
	prefix := "/tasks/{taskId}/nodes/{nodeRunId}"
	for _, suffix := range []string{"/inbox-messages", "/inbox-actions/{actionId}", "/runs/{runId}/timeline"} {
		p := prefix + suffix
		item := doc.Paths.Find(p)
		if item == nil {
			t.Fatal(p)
		}
		if agent.Paths.Find(p) != nil {
			t.Fatalf("management route in Agent document: %s", p)
		}
		for _, op := range item.Operations() {
			if op.Extensions["x-access"] != "console" {
				t.Fatal("management route ACL")
			}
		}
	}
	op := doc.Paths.Find("/tasks/{taskId}/events").Get
	b, _ := json.Marshal(op.Extensions["x-sse-events"])
	events := map[string]any{}
	_ = json.Unmarshal(b, &events)
	if events["task.inbox.updated"] == nil {
		t.Fatal("missing actual Inbox SSE")
	}
}
