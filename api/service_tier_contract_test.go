package api_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"loop-now/internal/server/contracttest"
	"testing"
)

func TestCodexServiceTierContract(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		doc := contracttest.LoadFile(t, file)
		schema := doc.Components.Schemas["CodexServiceTier"].Value
		for _, value := range []any{"default", "priority", "ultrafast"} {
			if err := schema.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatal(err)
			}
		}
		for _, value := range []any{nil, "auto", "fast", "", 1} {
			if err := schema.VisitJSON(value, openapi3.EnableJSONSchema2020()); err == nil {
				t.Fatal("invalid tier accepted", value)
			}
		}
		if file != "openapi.yaml" {
			continue
		}
		for _, name := range []string{"AgentCreate", "AgentUpdate"} {
			for _, tier := range []any{nil, "default", "priority", "ultrafast"} {
				body := map[string]any{"serviceTier": tier}
				if name == "AgentCreate" {
					body["name"] = "fixture"
					body["model"] = map[string]any{"provider": "openai-codex", "modelId": "fixture"}
				}
				if err := doc.Components.Schemas[name].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
					t.Fatal(name, tier, err)
				}
			}
		}
	}
}
