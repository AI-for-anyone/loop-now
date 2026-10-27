package api_test

import (
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/store"
)

func TestRunTriggerInboxMessageContract(t *testing.T) {
	trigger := uuid.New()
	for _, file := range []string{"openapi.yaml", "agent-openapi.yaml"} {
		t.Run(file, func(t *testing.T) {
			schema := contracttest.LoadFile(t, file).Components.Schemas["Run"].Value
			for _, tc := range []struct {
				name, origin string
				trigger      *uuid.UUID
			}{
				{"triggered", "inbox_reminder", &trigger},
				{"legacy reminder", "inbox_reminder", nil},
				{"ordinary console", "console", nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					stored := store.Run{ID: uuid.New(), Status: "queued", Origin: tc.origin, QueuedAt: time.Now().UTC(), TriggerInboxID: tc.trigger}
					projected := apitypes.FromRun(stored)
					value := jsonValue(t, projected).(map[string]any)
					if tc.trigger == nil {
						if _, present := value["triggerInboxMessageId"]; present {
							t.Fatal("missing trigger must be omitted, not null")
						}
					} else if value["triggerInboxMessageId"] != trigger.String() {
						t.Fatal("trigger must map from the original stored message ID")
					}
					if err := schema.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
						t.Fatal(err)
					}
					// History and message projections must use the same Run mapping.
					message := apitypes.FromMessage(store.Message{}, stored)
					if (message.Run.TriggerInboxMessageID == nil) != (tc.trigger == nil) {
						t.Fatal("message Run lost trigger")
					}
					if tc.trigger != nil && *message.Run.TriggerInboxMessageID != *tc.trigger {
						t.Fatal("message Run changed trigger")
					}
				})
			}
			value := jsonValue(t, apitypes.FromRun(store.Run{ID: uuid.New(), Status: "queued", QueuedAt: time.Now().UTC()})).(map[string]any)
			for _, bad := range []any{nil, 123} {
				value["triggerInboxMessageId"] = bad
				if err := schema.VisitJSON(value, openapi3.EnableJSONSchema2020()); err == nil {
					t.Fatal("null/non-string trigger accepted")
				}
			}
			field := schema.Properties["triggerInboxMessageId"].Value
			if field.Format != "uuid" {
				t.Fatal("trigger must be a UUID")
			}
		})
	}
}
