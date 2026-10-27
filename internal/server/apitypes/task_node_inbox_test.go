package apitypes_test

import (
	"encoding/json"
	"loop-now/internal/server/apitypes"
	"testing"
)

func TestTaskNodeInboxRunInputPresence(t *testing.T) {
	for _, raw := range []string{`null`, `{"runId":null}`, `{"runId":"00000000-0000-0000-0000-000000000000"}`, `{"runId":"bad"}`, `{"unknown":1}`} {
		var read apitypes.InboxReadInput
		if json.Unmarshal([]byte(raw), &read) == nil {
			t.Fatalf("accepted invalid read %s", raw)
		}
	}
	for _, raw := range []string{`{"text":"reply","runId":null}`, `{"text":"reply","runId":"00000000-0000-0000-0000-000000000000"}`, `{"text":"reply","runId":"bad"}`, `{"text":"reply","optionId":null}`, `{"text":"reply","nodeRunId":null}`} {
		var reply apitypes.InboxReplyInput
		if json.Unmarshal([]byte(raw), &reply) == nil {
			t.Fatalf("accepted invalid reply %s", raw)
		}
	}
	var read apitypes.InboxReadInput
	if e := json.Unmarshal([]byte(`{}`), &read); e != nil || read.RunID != nil {
		t.Fatal("legacy empty read changed", e)
	}
	var reply apitypes.InboxReplyInput
	if e := json.Unmarshal([]byte(`{"text":"reply","runId":"0192f1a2-0000-7000-8000-000000000001"}`), &reply); e != nil || reply.RunID == nil {
		t.Fatal("explicit run lost", e)
	}
}
