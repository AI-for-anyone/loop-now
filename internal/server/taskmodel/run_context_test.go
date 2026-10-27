package taskmodel

import (
	"encoding/json"
	"testing"
)

func TestTaskControlOptionalRunContext(t *testing.T) {
	for _, suffix := range []string{"", `,"runId":"00000000-0000-0000-0000-000000000001"`} {
		var submit SubmitInput
		if err := json.Unmarshal([]byte(`{"actionId":"00000000-0000-0000-0000-000000000002","output":{"number":9007199254740993},"edgeId":null`+suffix+`}`), &submit); err != nil {
			t.Fatal(err)
		}
		if submit.Output["number"] != json.Number("9007199254740993") {
			t.Fatal("custom decoder changed output number precision")
		}
		var fail FailInput
		if err := json.Unmarshal([]byte(`{"actionId":"00000000-0000-0000-0000-000000000002","reason":"failed"`+suffix+`}`), &fail); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{`"runId":null`, `"runId":"00000000-0000-0000-0000-000000000000"`, `"runId":"bad"`, `"runId":1`, `"RunID":"00000000-0000-0000-0000-000000000001"`} {
		var submit SubmitInput
		var fail FailInput
		if json.Unmarshal([]byte(`{"actionId":"00000000-0000-0000-0000-000000000002","output":{},"edgeId":null,`+field+`}`), &submit) == nil {
			t.Fatal("invalid submit run accepted")
		}
		if json.Unmarshal([]byte(`{"actionId":"00000000-0000-0000-0000-000000000002","reason":"failed",`+field+`}`), &fail) == nil {
			t.Fatal("invalid fail run accepted")
		}
	}
}
