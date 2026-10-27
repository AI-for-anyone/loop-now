package apitypes

import (
	"encoding/json"
	"testing"
)

func TestInboxQuestionSendRunContextPresence(t *testing.T) {
	for _, tail := range []string{"", `,"runId":"00000000-0000-0000-0000-000000000001"`} {
		var in InboxSendInput
		if err := json.Unmarshal([]byte(`{"to":"@console","text":"question","options":["yes"],"nodeRunId":"00000000-0000-0000-0000-000000000002"`+tail+`}`), &in); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{`"runId":null`, `"runId":"00000000-0000-0000-0000-000000000000"`, `"runId":1`, `"RunID":null`, `"RunID":"00000000-0000-0000-0000-000000000001"`} {
		var in InboxSendInput
		if json.Unmarshal([]byte(`{"to":"@console","text":"question",`+field+`}`), &in) == nil {
			t.Fatal("invalid run presence accepted")
		}
	}
}
