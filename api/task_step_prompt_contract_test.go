package api_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"loop-now/internal/server/contracttest"
)

// A workflow step may carry its own prompt (the node Agent's standing briefing) and may ask to start from its Agent's
// regular conversation; both are optional, in the console and in the Agent contract alike.
func TestTaskStepPromptContract(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		doc := contracttest.LoadFile(t, file)
		check := func(v map[string]any, valid bool) {
			t.Helper()
			err := doc.Components.Schemas["TaskWorkflowStep"].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
			if (err == nil) != valid {
				t.Fatalf("%s valid=%v for %v: %v", file, valid, v, err)
			}
		}
		step := func(extra map[string]any) map[string]any {
			s := map[string]any{"id": "work", "title": "Work", "description": "Do it", "agentId": uuid.NewString(),
				"inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}
			for k, v := range extra {
				s[k] = v
			}
			return s
		}
		check(step(nil), true)
		check(step(map[string]any{"prompt": "Background and decisions"}), true)
		check(step(map[string]any{"prompt": ""}), true)
		check(step(map[string]any{"prompt": strings.Repeat("a", 32768)}), true)
		check(step(map[string]any{"prompt": strings.Repeat("a", 32769)}), false)
		check(step(map[string]any{"prompt": nil}), false)
		check(step(map[string]any{"prompt": 1}), false)
		check(step(map[string]any{"withConversation": true}), true)
		check(step(map[string]any{"withConversation": false}), true)
		check(step(map[string]any{"withConversation": nil}), false)
		check(step(map[string]any{"withConversation": "yes"}), false)
		for _, name := range []string{"prompt", "withConversation"} {
			if p := doc.Components.Schemas["TaskWorkflowStep"].Value.Properties[name]; p == nil || p.Value.Description == "" {
				t.Fatalf("%s: TaskWorkflowStep.%s needs a description", file, name)
			}
		}
	}
}
