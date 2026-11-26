package local

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"loop-now/internal/server/toolcommand"
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

// A file tool's files are recorded relative to where the CLI works when inside it, absolute otherwise, long ones cut;
// a search's query is recorded on the command redaction rule.
func TestToolTargetRecorded(t *testing.T) {
	obs := &recObs{}
	e := &Executor{obs: obs}
	agent, run := uuid.New(), uuid.New()
	a := sinkAdapter{cwd: "/work/app", event: func(typ string, data map[string]any) { e.event(agent, &run, typ, data) }}
	long := "/elsewhere/" + strings.Repeat("d/", 400) + "x.go"
	a.ToolFiles("read", "Read", []string{"/work/app/internal/main.go"})
	a.ToolFiles("outside", "Read", []string{"/etc/hosts"})
	a.ToolFiles("long", "Write", []string{long})
	a.ToolFiles("patch", "apply_patch", []string{"/work/app/a.go", "/work/app/b.go"})
	a.ToolFiles("none", "Read", nil)
	a.ToolQuery("grep", "Grep", "func main")
	a.ToolQuery("secret", "Grep", "api_key=abc123")
	got := map[string]string{}
	for _, ev := range obs.eventsOf(agent, "tool.target") {
		got[ev.ev.Data["toolCallId"].(string)] = ev.ev.Data["kind"].(string) + " " + ev.ev.Data["target"].(string)
	}
	want := map[string]string{"read": "files internal/main.go", "outside": "files /etc/hosts", "patch": "files a.go, b.go",
		"grep": "query func main", "secret": "query " + toolcommand.HiddenQuery}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %q, want %q", id, got[id], w)
		}
	}
	if l := got["long"]; utf8.RuneCountInString(l) != len("files ")+toolcommand.MaxTargetChars || !strings.HasSuffix(l, "/x.go") {
		t.Errorf("long path: %q", l)
	}
	if _, ok := got["none"]; ok || len(got) != 6 {
		t.Errorf("recorded: %v", got)
	}
}
