package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
)

func TestTimelineToolCommandContract(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		t.Run(file, func(t *testing.T) {
			doc := contracttest.LoadFile(t, file)
			schema := doc.Components.Schemas["TimelineTool"].Value
			entry := apitypes.TimelineToolEntry{TimelineBase: apitypes.TimelineBase{Type: "tool", At: time.Now().UTC()}, ToolCallID: "actual-call", ToolName: "bash", State: "started"}
			for _, command := range []*string{nil, ptrCommand("git status --short")} {
				entry.Command = command
				if err := schema.VisitJSON(jsonValue(t, entry), openapi3.EnableJSONSchema2020()); err != nil {
					t.Fatal(err)
				}
			}
			for _, command := range []any{nil, 123, strings.Repeat("x", 4097)} {
				bad := jsonValue(t, entry).(map[string]any)
				bad["command"] = command
				if err := schema.VisitJSON(bad, openapi3.EnableJSONSchema2020()); err == nil {
					t.Fatal("invalid command accepted")
				}
			}
		})
	}
}

func ptrCommand(s string) *string { return &s }
