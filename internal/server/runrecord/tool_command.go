package runrecord

import (
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/toolcommand"
)

// Commands are persisted in the existing scoped lifecycle JSON records. No
// session scan/backfill and no new table/column is needed for old tool summaries.
// A tool.command event is written only for a shell tool, whatever that runtime
// names it, so the command goes to the entry with the same toolCallId.
func toolCommands(events []store.LifecycleEvent) map[string]string {
	out := map[string]string{}
	for _, ev := range events {
		if ev.Type != "tool.command" {
			continue
		}
		if id := str(ev.Data, "toolCallId"); id != "" {
			if command := toolcommand.Display(str(ev.Data, "command")); command != "" {
				out[id] = command
			}
		}
	}
	return out
}

func withToolCommand(value any, commands map[string]string) any {
	if tool, ok := value.(apitypes.TimelineToolEntry); ok {
		if command := commands[tool.ToolCallID]; command != "" {
			tool.Command = &command
		}
		return tool
	}
	return value
}
