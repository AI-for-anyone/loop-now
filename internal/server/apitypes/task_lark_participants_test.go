package apitypes

import (
	"encoding/json"
	"testing"

	"loop-now/internal/server/taskmodel"
)

func TestLarkParticipantsProjectionDoesNotEnterBaseTask(t *testing.T) {
	base := taskmodel.Detail{}
	entries := []taskmodel.LarkSwitchParticipant{}
	view := ConsoleTaskDetail{Detail: base, LarkSwitchParticipants: &entries}
	raw, e := json.Marshal(view)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if string(fields["larkSwitchParticipants"]) != "[]" {
		t.Fatal("supported empty projection omitted or null")
	}
	raw, _ = json.Marshal(ConsoleTaskDetail{Detail: base})
	json.Unmarshal(raw, &fields)
	// Decode into a fresh map because json.Unmarshal retains absent map keys.
	fields = nil
	json.Unmarshal(raw, &fields)
	if _, ok := fields["larkSwitchParticipants"]; ok {
		t.Fatal("unsupported capability should be absent")
	}
	raw, _ = json.Marshal(base)
	fields = nil
	json.Unmarshal(raw, &fields)
	if _, ok := fields["larkSwitchParticipants"]; ok {
		t.Fatal("base task leaked management projection")
	}
}
