package taskmodel

import (
	"encoding/json"
	"testing"
)

func TestTaskConsoleMessageInput(t *testing.T) {
	base := `"actionId":"0192f1a2-0000-7000-8000-000000000001","expectedAgentId":"0192f1a2-0000-7000-8000-000000000002","expectedRuntimeId":"0192f1a2-0000-7000-8000-000000000003","text":"原节点消息"`
	var in TaskNodeMessageInput
	if err := json.Unmarshal([]byte("{"+base+"}"), &in); err != nil || in.WhenBusy != "queue" {
		t.Fatal("missing whenBusy must default queue", err)
	}
	if err := json.Unmarshal([]byte("{"+base+`,"whenBusy":"interrupt"}`), &in); err != nil || in.WhenBusy != "interrupt" {
		t.Fatal("explicit interrupt lost", err)
	}
	for _, extra := range []string{`,"whenBusy":null`, `,"whenBusy":1`, `,"options":[]`, `,"answerTo":"synthetic"`, `,"recipient":"@console"`} {
		if err := json.Unmarshal([]byte("{"+base+extra+"}"), &in); err == nil {
			t.Fatal("invalid or question field accepted", extra)
		}
	}
}
