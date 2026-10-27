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
