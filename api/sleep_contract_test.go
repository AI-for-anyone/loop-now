package api_test

import (
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/sleepmodel"
	"testing"
	"time"
)

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var out any
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestSleepFormalContractSamples(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		t.Run(file, func(t *testing.T) {
			doc := contracttest.LoadFile(t, file)
			check := func(name string, v any) {
				t.Helper()
				if e := doc.Components.Schemas[name].Value.VisitJSON(jsonValue(t, v), openapi3.EnableJSONSchema2020()); e != nil {
					t.Fatalf("%s: %v", name, e)
				}
			}
			id, agent, run := uuid.New(), uuid.New(), uuid.New()
			now := time.Now().UTC()
			rec := sleepmodel.Record{ID: id, ScopeKind: "normal", AgentID: agent, SourceRunID: run, State: "requested", Seconds: 120, RequestedAt: now, Revision: 1}
			check("SleepInput", sleepmodel.Input{ActionID: uuid.New(), RunID: run, Seconds: 2147483647})
			check("SleepReceipt", sleepmodel.Receipt{Sleep: rec, EndTurn: true})
			check("SleepState", sleepmodel.State{})
			check("SleepState", sleepmodel.State{CurrentRunID: &run, Sleep: &rec})
			wake := now.Add(120 * time.Second)
			rec.State = "sleeping"
			rec.EnteredAt = &now
			rec.WakeAt = &wake
			rec.Revision = 2
			event := sleepmodel.Updated{ScopeKind: "normal", AgentID: agent, SleepRevision: 2, Sleep: &rec}
			check("SseSleepUpdated", event)
			event.Sleep = nil
			event.EndedSleepID = &id
			event.SleepRevision = 3
			check("SseSleepUpdated", event)
			task, runtime, node := uuid.New(), uuid.New(), uuid.New()
			version := int64(17)
			event.ScopeKind = "task"
			event.TaskID = &task
			event.RuntimeID = &runtime
			event.NodeRunID = &node
			event.TaskVersion = &version
			check("SseSleepUpdated", event)
			entry := apitypes.TimelineSleepEntry{TimelineBase: apitypes.TimelineBase{Type: "sleep", At: now}, Sleep: rec}
			check("TimelineSleepEntry", entry)
			if file == "openapi.yaml" {
				check("TimelineEntry", entry)
			}
			if file == "agent-openapi.yaml" {
				post, get := doc.Paths.Value("/sleep").Post, doc.Paths.Value("/sleep").Get
				if post.OperationID != "requestSleep" || get.OperationID != "getSleepState" || post.Extensions["x-agent-only"] != true || get.Extensions["x-agent-only"] != true {
					t.Fatal("Agent-only operation contract changed")
				}
				for status, code := range map[string]string{"401": "unauthenticated", "403": "forbidden", "409": "sleep_conflict", "422": "validation_failed"} {
					response := post.Responses.Value(status)
					if response == nil {
						t.Fatal("missing response", status)
					}
					if e := response.Value.Content["application/json"].Schema.Value.VisitJSON(map[string]any{"error": map[string]any{"code": code, "message": "fixed safe description"}}, openapi3.EnableJSONSchema2020()); e != nil {
						t.Fatal(status, e)
					}
				}
			} else if doc.Paths.Value("/sleep") != nil {
				t.Fatal("console must not expose sleep operation")
			}
			for _, bad := range []any{map[string]any{"actionId": id, "runId": run, "seconds": 0}, map[string]any{"actionId": id, "runId": run, "seconds": 1.5}, map[string]any{"actionId": id, "runId": run, "seconds": 2147483648}, map[string]any{"actionId": id, "runId": run, "seconds": 1, "agentId": agent}} {
				if e := doc.Components.Schemas["SleepInput"].Value.VisitJSON(jsonValue(t, bad), openapi3.EnableJSONSchema2020()); e == nil {
					t.Fatal("invalid input accepted", bad)
				}
			}
			missing := jsonValue(t, event).(map[string]any)
			delete(missing, "taskVersion")
			if e := doc.Components.Schemas["SseSleepUpdated"].Value.VisitJSON(missing, openapi3.EnableJSONSchema2020()); e == nil {
				t.Fatal("missing committed taskVersion accepted")
			}
		})
	}
}
