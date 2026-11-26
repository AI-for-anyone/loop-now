package local

import (
	"testing"

	"github.com/google/uuid"
)

// Every runtime's shell tool has its command recorded, whatever the runtime calls the tool.
func TestToolCommandRecordedForEveryShellName(t *testing.T) {
	obs := &recObs{}
	e := &Executor{obs: obs}
	agent, run := uuid.New(), uuid.New()
	for _, name := range []string{"bash", "shell", "execute"} {
		e.toolCommand(agent, run, name+"-call", name, "echo "+name)
	}
	e.toolCommand(agent, run, "empty", "shell", "  ")
	got := map[string]any{}
	for _, ev := range obs.eventsOf(agent, "tool.command") {
		got[ev.ev.Data["toolCallId"].(string)] = ev.ev.Data["command"]
	}
	if len(got) != 3 || got["bash-call"] != "echo bash" || got["shell-call"] != "echo shell" || got["execute-call"] != "echo execute" {
		t.Fatalf("recorded commands: %v", got)
	}
}
