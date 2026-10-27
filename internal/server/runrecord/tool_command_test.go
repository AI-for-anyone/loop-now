package runrecord

import (
	"encoding/json"
	"strings"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

func TestToolCommandScopedProjection(t *testing.T) {
	events := []store.LifecycleEvent{
		{Type: "tool.command", Data: map[string]any{"toolCallId": "one", "toolName": "bash", "command": "git status --short", "args": "PRIVATE", "output": "PRIVATE"}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "read", "toolName": "read", "command": "PRIVATE"}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "secret", "toolName": "bash", "command": "TOKEN=PRIVATE curl localhost"}},
	}
	commands := toolCommands(events)
	for _, tc := range []struct{ id, name, want string }{{"one", "bash", "git status --short"}, {"missing", "bash", ""}, {"read", "read", ""}, {"secret", "bash", "[含敏感凭证的命令已隐藏]"}} {
		got := withToolCommand(apitypes.TimelineToolEntry{ToolCallID: tc.id, ToolName: tc.name}, commands).(apitypes.TimelineToolEntry)
		if tc.want == "" && got.Command != nil || tc.want != "" && (got.Command == nil || *got.Command != tc.want) {
			t.Fatal("wrong tool association", tc.id)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private input/output escaped")
		}
	}
}
