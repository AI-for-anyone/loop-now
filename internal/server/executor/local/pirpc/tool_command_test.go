package pirpc

import (
	"strings"
	"sync"
	"testing"
)

type commandRecorder struct {
	*recorder
	muCommand sync.Mutex
	commands  []string
}

func (r *commandRecorder) ToolCommand(id, name, command string) {
	r.muCommand.Lock()
	defer r.muCommand.Unlock()
	r.commands = append(r.commands, id+"|"+name+"|"+command)
}

func TestPipeActualBashCommandInput(t *testing.T) {
	f := newPipeFake(t)
	rec := &commandRecorder{recorder: newRecorder()}
	ch := goPrompt(testCtx(t), f.c, "x", rec)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"agent_start"}`, evStart(), evDelta("working"), evEnd("working", "toolUse", ""),
		`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"git status --short","other":"PRIVATE-ARG"}}`,
		`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","args":{"command":"not-the-start-command"},"result":{"output":"PRIVATE-OUTPUT"},"isError":false}`,
		`{"type":"tool_execution_start","toolCallId":"c2","toolName":"read","args":{"command":"PRIVATE-NON-BASH","path":"PRIVATE-PATH"}}`,
		`{"type":"tool_execution_end","toolCallId":"c2","toolName":"read","isError":false}`,
		`{"type":"tool_execution_start","toolCallId":"c3","toolName":"bash","args":{}}`,
		evSettled(false))
	if out := recvPrompt(t, ch); out.err != nil {
		t.Fatal(out.err)
	}
	rec.muCommand.Lock()
	defer rec.muCommand.Unlock()
	if strings.Join(rec.commands, ",") != "c1|bash|git status --short" {
		t.Fatal("wrong input/extra arguments observed", rec.commands)
	}
}

type targetRecorder struct {
	*recorder
	mu      sync.Mutex
	targets []string
}

func (r *targetRecorder) ToolFiles(id, name string, paths []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, id+"|"+name+"|"+strings.Join(paths, "+"))
}
func (r *targetRecorder) ToolQuery(id, name, query string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, id+"|"+name+"|"+query)
}

// Pi's read, edit and write report their path, grep and find their pattern; nothing else of the arguments.
func TestPipeToolTargets(t *testing.T) {
	f := newPipeFake(t)
	rec := &targetRecorder{recorder: newRecorder()}
	ch := goPrompt(testCtx(t), f.c, "x", rec)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"agent_start"}`, evStart(), evDelta("working"), evEnd("working", "toolUse", ""),
		`{"type":"tool_execution_start","toolCallId":"r","toolName":"read","args":{"path":"/w/a.go","offset":3}}`,
		`{"type":"tool_execution_start","toolCallId":"e","toolName":"edit","args":{"path":"b.go","oldText":"PRIVATE","newText":"PRIVATE"}}`,
		`{"type":"tool_execution_start","toolCallId":"w","toolName":"write","args":{"path":"c.go","content":"PRIVATE"}}`,
		`{"type":"tool_execution_start","toolCallId":"g","toolName":"grep","args":{"pattern":"func main","path":"/w"}}`,
		`{"type":"tool_execution_start","toolCallId":"f","toolName":"find","args":{"pattern":"*.go"}}`,
		`{"type":"tool_execution_start","toolCallId":"b","toolName":"bash","args":{"command":"ls","path":"PRIVATE"}}`,
		evSettled(false))
	if out := recvPrompt(t, ch); out.err != nil {
		t.Fatal(out.err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := strings.Join(rec.targets, ","); got != "r|read|/w/a.go,e|edit|b.go,w|write|c.go,g|grep|func main,f|find|*.go" {
		t.Fatal("targets", got)
	}
}
