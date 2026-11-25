package api_test

import (
	"slices"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// Every reason the executor may stop a background task for is in the enums of BackgroundTask.stopReason and
// TimelineBackgroundTaskEnded.stopReason, and a run projects its background tasks (started and followed) as the
// contract says, in both contracts.
func TestBackgroundTasksContract(t *testing.T) {
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		doc := contracttest.LoadFile(t, file)
		for _, name := range []string{"BackgroundTask", "TimelineBackgroundTaskEnded"} {
			enum := doc.Components.Schemas[name].Value.Properties["stopReason"].Value.Enum
			for _, reason := range executor.BackgroundStopReasons {
				if !slices.Contains(enum, any(reason)) {
					t.Errorf("%s %s.stopReason is missing %s", file, name, reason)
				}
			}
			if len(enum) != len(executor.BackgroundStopReasons)+1 {
				t.Errorf("%s %s.stopReason lists reasons the executor never gives: %v", file, name, enum)
			}
		}
		run, other := uuid.New(), uuid.New()
		at := time.Now().UTC()
		code, out, reason, call := int64(2), "tail", "person", "call-1"
		started := store.BackgroundTask{Key: uuid.New(), RunID: &run, NativeID: "b1", Runtime: "claude-code", Kind: "command", Label: "make", ToolCallID: &call,
			StartedAt: at, Status: "stopped", EndedAt: &at, ExitCode: &code, StopReason: &reason, StopRequestedAt: &at, Output: &out}
		followed := store.BackgroundTask{Key: uuid.New(), RunID: &other, NativeID: "b0", Runtime: "claude-code", Kind: "subagent", Label: "review", StartedAt: at, Status: "running"}
		for _, origin := range []string{"console", executor.OriginBackground} {
			projected := apitypes.FromRun(store.Run{ID: run, Status: "completed", Origin: origin, QueuedAt: at, BackgroundTasks: []store.BackgroundTask{started},
				TriggerBackgroundTasks: []store.BackgroundTask{followed}})
			value := jsonValue(t, projected).(map[string]any)
			if _, present := value["triggerBackgroundTasks"]; present != (origin == executor.OriginBackground) {
				t.Errorf("%s %s: triggerBackgroundTasks only on a follow-up: %v", file, origin, value["triggerBackgroundTasks"])
			}
			if err := doc.Components.Schemas["Run"].Value.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
				t.Errorf("%s %s: %v", file, origin, err)
			}
		}
	}
}
