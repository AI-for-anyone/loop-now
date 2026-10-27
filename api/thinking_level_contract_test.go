package api_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
)

func TestThinkingLevelContract(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		doc := contracttest.LoadFile(t, file)
		schema := doc.Components.Schemas["ThinkingLevel"].Value
		for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
			if e := schema.VisitJSON(level, openapi3.EnableJSONSchema2020()); e != nil {
				t.Fatal(file, level, e)
			}
		}
		for _, bad := range []any{nil, "", "ultra", 12} {
			if e := schema.VisitJSON(bad, openapi3.EnableJSONSchema2020()); e == nil {
				t.Fatal("invalid global level accepted")
			}
		}
		if file == "openapi.yaml" {
			// An old/synthetic catalog without thinking metadata remains a
			// valid old response; missing capability is never an invented level.
			old := apitypes.Model{ID: "old", Name: "old", Reasoning: true, Input: []string{"text"}}
			if e := doc.Components.Schemas["Model"].Value.VisitJSON(jsonValue(t, old), openapi3.EnableJSONSchema2020()); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"AgentCreate", "AgentUpdate"} {
				for _, level := range []any{nil, "high"} {
					v := map[string]any{"thinkingLevel": level}
					if name == "AgentCreate" {
						v["name"] = "fixture"
						v["model"] = map[string]any{"provider": "fixture", "modelId": "fixture"}
					}
					if e := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020()); e != nil {
						t.Fatal(name, e)
					}
				}
			}
		}
	}
}
