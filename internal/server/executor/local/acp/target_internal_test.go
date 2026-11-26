package acp

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// detailSink also records the commands and targets a turn reports.
type detailSink struct {
	recordSink
	details []string
}

func (s *detailSink) ToolCommand(id, name, command string) {
	s.mu.Lock()
	s.details = append(s.details, "command:"+id+":"+command)
	s.mu.Unlock()
}
func (s *detailSink) ToolFiles(id, name string, paths []string) {
	s.mu.Lock()
	s.details = append(s.details, "files:"+id+":"+strings.Join(paths, "+"))
	s.mu.Unlock()
}
func (s *detailSink) ToolQuery(id, name, query string) {
	s.mu.Lock()
	s.details = append(s.details, "query:"+id+":"+query)
	s.mu.Unlock()
}

func inDetailTurn(id string) (*Client, *detailSink) {
	s := &detailSink{}
	c := &Client{}
	c.turn = &turn{sessionID: id, sink: s, done: make(chan struct{}), changed: make(chan struct{}, 1), tools: map[string]openTool{}}
	return c, s
}

// Antigravity's run_command gives its command as CommandLine (testdata/agy-execute.jsonl, from a real agy session):
// the command is recorded once.
func TestAgyCommandLineRecorded(t *testing.T) {
	f, err := os.Open("testdata/agy-execute.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, s := inDetailTurn("agy-session")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c.handle(sc.Bytes())
	}
	if got := strings.Join(s.details, ","); got != "command:call_159630061966072212:sleep 2; echo quick-one" {
		t.Fatalf("details: %s", got)
	}
}

// A read or edit reports its files (its locations, else the path in its input, which Antigravity may quote), a search
// its query, each once; other tools and inputs report nothing.
func TestToolTargets(t *testing.T) {
	c, s := inDetailTurn("s")
	for _, line := range []string{
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"r","kind":"read","title":"Read","status":"in_progress","locations":[{"path":"/w/a.go","line":3}],"rawInput":{"AbsolutePath":"/w/other.go"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"r","status":"completed","locations":[{"path":"/w/a.go"}]}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"e","kind":"edit","title":"Edit","status":"in_progress","rawInput":{"TargetFile":"\"/w/b.go\"","CodeContent":"PRIVATE"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"late","kind":"read","title":"Read","status":"in_progress"}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"late","status":"in_progress","locations":[{"path":"/w/c.go"}]}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"q","kind":"search","title":"Search","status":"in_progress","rawInput":{"Query":"func main","SearchPath":"/w"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t","kind":"think","title":"Think","status":"in_progress","rawInput":{"path":"/w/PRIVATE"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"n","kind":"read","title":"Read","status":"completed"}}}`,
	} {
		c.handle([]byte(line))
	}
	want := "files:r:/w/a.go,files:e:/w/b.go,files:late:/w/c.go,query:q:func main"
	if got := strings.Join(s.details, ","); got != want {
		t.Fatalf("details:\n got %s\nwant %s", got, want)
	}
}
