package taskmodel

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestTaskLarkSwitchRequiredNonNullCAS(t *testing.T) {
	base := map[string]any{"actionId": uuid.New(), "expectedTaskVersion": 1, "expectedNodeRunId": uuid.New(), "expectedAgentId": uuid.New(), "expectedRuntimeId": uuid.New(), "expectedLarkBindingRevision": 0, "expectedMembers": []any{map[string]any{"agentId": uuid.New(), "runtimeId": uuid.New(), "expectedBindingRevision": 0}, map[string]any{"agentId": uuid.New(), "runtimeId": uuid.New(), "expectedBindingRevision": 0}}, "targetAccountId": uuid.New(), "targetVersionId": uuid.New(), "expectedAccountRevision": 1}
	valid, _ := json.Marshal(base)
	var in TaskLarkSwitchInput
	if err := json.Unmarshal(valid, &in); err != nil {
		t.Fatal(err)
	}
	for key := range base {
		for _, mode := range []string{"missing", "null"} {
			t.Run(key+"_"+mode, func(t *testing.T) {
				var body map[string]any
				_ = json.Unmarshal(valid, &body)
				if mode == "missing" {
					delete(body, key)
				} else {
					body[key] = nil
				}
				raw, _ := json.Marshal(body)
				if err := json.Unmarshal(raw, &in); err == nil {
					t.Fatal("missing/null key accepted")
				}
			})
		}
	}
	for _, key := range []string{"agentId", "runtimeId", "expectedBindingRevision"} {
		t.Run("member_null_"+key, func(t *testing.T) {
			var body map[string]any
			_ = json.Unmarshal(valid, &body)
			body["expectedMembers"].([]any)[0].(map[string]any)[key] = nil
			raw, _ := json.Marshal(body)
			if err := json.Unmarshal(raw, &in); err == nil {
				t.Fatal("null member CAS accepted")
			}
		})
	}
	for _, raw := range []string{"null", "{}", `{"actionId":null}`} {
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatal("non-input accepted", raw)
		}
	}
}
