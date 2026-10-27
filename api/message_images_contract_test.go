package api_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"testing"
)

func TestMessageImageContract(t *testing.T) {
	d := contracttest.LoadFile(t, "openapi.yaml")
	a := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, v any, valid bool) {
		t.Helper()
		e := d.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (e == nil) != valid {
			t.Fatalf("%s valid=%v: %v", name, valid, e)
		}
	}
	id := uuid.NewString()
	for _, name := range []string{"SendMessageInput", "TaskNodeInboxInput"} {
		base := map[string]any{"text": ""}
		if name == "TaskNodeInboxInput" {
			base["actionId"] = id
			base["expectedAgentId"] = id
			base["expectedRuntimeId"] = id
		}
		check(name, base, false)
		base["imageIds"] = []any{id}
		check(name, base, true)
		base["imageIds"] = nil
		check(name, base, false)
		base["imageIds"] = []any{}
		check(name, base, false)
		base["text"] = "old text"
		check(name, base, true)
		base["text"] = nil
		check(name, base, false)
	}
	check("TaskNodeMessageInput", map[string]any{"actionId": id, "expectedAgentId": id, "expectedRuntimeId": id, "text": "legacy direct", "imageIds": []any{id}}, false)
	for _, path := range []string{"/agents/{agentId}/message-images/{imageId}", "/tasks/{taskId}/nodes/{nodeRunId}/message-images/{imageId}", "/message-images/{imageId}"} {
		if d.Paths.Find(path) == nil {
			t.Fatal("missing console image route", path)
		}
		if a.Paths.Find(path) != nil {
			t.Fatal("image management exposed to agent", path)
		}
	}
	check("MessageImage", map[string]any{"id": id, "mimeType": "image/png", "size": 12, "width": 1, "height": 1}, true)
	ref := map[string]any{"id": id, "mimeType": "image/png", "size": 12, "width": 1, "height": 1}
	party := map[string]any{"kind": "console", "address": "@console", "name": "console", "id": nil, "deleted": false, "pluginId": nil, "channelId": nil}
	message := map[string]any{"id": id, "threadId": id, "inReplyTo": nil, "from": party, "to": party, "preview": "", "whenBusy": nil, "createdAt": "2026-10-22T01:00:00Z", "readAt": nil, "text": "", "cancelledAt": nil, "task": nil, "images": []any{ref}}
	check("InboxMessage", message, true)
	check("TaskNodeInboxReceipt", map[string]any{"actionId": id, "taskId": id, "nodeRunId": id, "agentId": id, "runtimeId": id, "status": "accepted", "message": message}, true)
	message["images"] = nil
	check("InboxMessage", message, false)

	check("MessageImage", map[string]any{"id": id, "mimeType": "image/svg+xml", "size": 12, "width": 1, "height": 1}, false)
}
