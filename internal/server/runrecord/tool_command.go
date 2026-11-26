package runrecord

import (
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/toolcommand"
)

// toolDetails are the commands and targets recorded for tool calls, by toolCallId.
type toolDetails struct {
	commands, targets map[string]string
}

// Commands and targets are persisted in the existing scoped lifecycle JSON
// records (tool.command, tool.target). No session scan/backfill and no new
// table/column is needed for old tool summaries. A tool.command event is
// written only for a shell tool, whatever that runtime names it, and a
// tool.target event only for a file or search tool, so each goes to the entry
// with the same toolCallId. Both are projected again on read.
func toolDetailsOf(events []store.LifecycleEvent) toolDetails {
	out := toolDetails{commands: map[string]string{}, targets: map[string]string{}}
	for _, ev := range events {
		id := str(ev.Data, "toolCallId")
		if id == "" {
			continue
		}
		switch ev.Type {
		case "tool.command":
			if command := toolcommand.Display(str(ev.Data, "command")); command != "" {
				out.commands[id] = command
			}
		case "tool.target":
			target := str(ev.Data, "target")
			if str(ev.Data, "kind") == "query" {
				target = toolcommand.Query(target)
			} else {
				target = toolcommand.Files("", []string{target})
			}
			if target != "" {
				out.targets[id] = target
			}
		}
	}
	return out
}

func withToolDetails(value any, d toolDetails) any {
	if tool, ok := value.(apitypes.TimelineToolEntry); ok {
		if command := d.commands[tool.ToolCallID]; command != "" {
			tool.Command = &command
		}
		if target := d.targets[tool.ToolCallID]; target != "" {
			tool.Target = &target
		}
		return tool
	}
	return value
}
