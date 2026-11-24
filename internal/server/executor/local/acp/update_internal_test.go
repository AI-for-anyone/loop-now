package acp

import (
	"bufio"
	"os"
	"strings"
	"sync"
	"testing"
)

type recordSink struct {
	mu    sync.Mutex
	text  strings.Builder
	tools []string
}

func (s *recordSink) TextDelta(d string) { s.mu.Lock(); s.text.WriteString(d); s.mu.Unlock() }
func (s *recordSink) ToolStarted(id, name string) {
	s.mu.Lock()
	s.tools = append(s.tools, "start:"+id+":"+name)
	s.mu.Unlock()
}
func (s *recordSink) ToolEnded(id, name string, isError bool) {
	state := "ok"
	if isError {
		state = "error"
	}
	s.mu.Lock()
	s.tools = append(s.tools, "end:"+id+":"+name+":"+state)
	s.mu.Unlock()
}
func (s *recordSink) list() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.tools, ",")
}

// inTurn is a client with a turn in progress on session id, as Prompt sets it up.
func inTurn(id string) (*Client, *recordSink) {
	s := &recordSink{}
	c := &Client{}
	c.turn = &turn{sessionID: id, sink: s, done: make(chan struct{}), changed: make(chan struct{}, 1), tools: map[string]openTool{}}
	return c, s
}

// agy's run_command as it comes (testdata/agy-execute.jsonl, from a real agy 1.5.14 session with the session id and
// times replaced): its end carries the command's output as content, a list of ToolCallContent, unlike a message
// chunk's single ContentBlock. The end counts: the tool succeeded.
func TestToolEndWithOutputContent(t *testing.T) {
	f, err := os.Open("testdata/agy-execute.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, s := inTurn("agy-session")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c.handle(sc.Bytes())
	}
	if got := s.list(); got != "start:call_159630061966072212:execute,end:call_159630061966072212:execute:ok" {
		t.Fatalf("tools: %s", got)
	}
}

// Every field of an update is read on its own: content of either shape, or a field of an unexpected type, leaves the
// rest (the tool call id, the status) in use. A message chunk's content is still its text.
func TestUpdateFieldsReadOnTheirOwn(t *testing.T) {
	c, s := inTurn("s")
	for _, line := range []string{
		// A tool call that comes already completed, with its output as a content list.
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"a","kind":"read","title":"Read a.txt","status":"completed","content":[{"type":"content","content":{"type":"text","text":"hello"}}]}}}`,
		// A title that is not a string, raw input that is not an object, content that is a single block: the call is
		// still named by its kind and its end still counts.
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"b","kind":"execute","title":42,"status":"in_progress","rawInput":"ls -la"}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"b","status":"failed","title":["x"],"content":{"type":"text","text":"oops"}}}}`,
		// A message chunk.
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}}}`,
		// A chunk whose content is a list is no text, and breaks nothing.
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":[{"type":"text","text":"ignored"}]}}}`,
	} {
		c.handle([]byte(line))
	}
	if got := s.list(); got != "start:a:read,end:a:read:ok,start:b:execute,end:b:execute:error" {
		t.Fatalf("tools: %s", got)
	}
	if s.text.String() != "done" {
		t.Fatalf("text: %q", s.text.String())
	}
}
