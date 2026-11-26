// Command fakeacp is a fake Agent Client Protocol agent for tests, playing Grok or Antigravity (FAKEACP_FLAVOR=grok or
// agy; without it, agy when the executable's name starts with agy, otherwise grok): --version and models answer as those CLIs do, and any arguments containing stdio or acp serve
// the protocol (version 1) over stdio, without network access and without reading credentials. Sessions are saved
// under $FAKEACP_HOME/sessions once they have had a prompt. Loading an unknown session fails like Grok ("Path not
// found.") or, for agy, succeeds empty, as the real ones do. FAKEACP_LOAD_SILENT=1 replays a session's history but
// never answers session/load (as Grok sometimes does). $FAKEACP_HOME/.logged-out makes models report logged out and
// prompts fail with auth_required. FAKEACP_ARGS_FILE gets one line per start (JSON: arguments and environment).
// With FAKEACP_NATIVE_HOME every new session also leaves the records the real CLI keeps under its HOME, even with no
// prompt: agy .gemini/antigravity-cli/conversations/<id>.db (with -wal) and brain/<id>/, Grok
// .grok/sessions/<escaped cwd>/<id>/; Grok also appends each prompt to .grok/sessions/<escaped cwd>/prompt_history.jsonl.
// FAKEACP_SLOW (a duration) delays --version and models; FAKEACP_HANG=grok or agy makes them hang for that flavor.
//
// The first "fake:" word of a prompt decides the reply:
//
//	fake:history      reply history=<earlier prompts in this session>
//	fake:config       reply with the session's settings (JSON: cwd, MCP server names, model, start arguments, mode set
//	                  with session/set_mode); a new agy session offers the modes default, accept-edits, auto and plan,
//	                  a Grok one none
//	fake:env          reply with the environment variables the platform cares about (JSON)
//	fake:images       reply images=<number of images>
//	fake:tool <cmd>   run one execute tool call, then reply ran
//	fake:toolfail     run one tool call that fails, then reply ran
//	fake:toolopen     start one execute tool call and never report its end, then reply ran
//	fake:background <durations>
//	                  as agy's run_command moved to the background: start one command (kind execute) per word, reply
//	                  backgrounded and end the prompt with usage_update. Each command ends after its duration, with
//	                  no prompt open (tool_call_update completed); the agent then replies by itself, "reported <n>"
//	                  for the n commands that ended before that reply began, and ends the reply with usage_update.
//	                  The word lost never reports its end; daemon starts a daemon command that never ends; now
//	                  ends before the prompt does (its end comes within the prompt, as agy's for a quick command).
//	                  <d1>+<d2> runs a command for d1, and the reply the agent gives by itself about it starts another
//	                  command for d2 (as agy does in its own reply, with no prompt open), still running when that reply
//	                  ends; its end brings another reply of the agent's own.
//	                  FAKEACP_WAKE_DELAY (a duration) is how long such a reply takes to begin and to finish.
//	                  session/cancel ends the session's commands still running, without a word.
//	fake:bgtask <dur> <cmd>
//	                  as Grok's run_terminal_command moved to the background: the tool call completes at once,
//	                  _x.ai/task_backgrounded and a background_tasks list follow, reply started. After <dur> (or on
//	                  _x.ai/task/kill {sessionId, taskId}) the task ends: _x.ai/task_completed with its snapshot, an
//	                  updated list, and Grok replies by itself in a prompt of its own (_x.ai/queue/changed runningPromptId
//	                  task-completed-<task id>, text "background done", turn_completed with its usage) once no prompt of
//	                  the client runs. With none running, that prompt is announced before the task's end, as the real one
//	                  does.
//	fake:texttool     reply some text, run one execute tool call, then reply ran (text on both sides of a tool call)
//	fake:permission   ask for permission, then reply permission=<chosen option>
//	fake:sleep <dur>  wait for the duration (the word after it), or until cancelled, then reply slept
//	fake:slow         stream a dot every 50ms until cancelled
//	fake:noack        the same, but ignore session/cancel
//	fake:refusal      stop with refusal
//	fake:exit         stream a little text, then exit with code 3
//	anything else     reply echo: <text>
//
// A prompt that ends normally (end_turn) reports its token usage as the CLI it plays does: agy in the result's usage,
// Grok in the result's _meta.usage and in a _x.ai/session_notification turn_completed before it. Grok announces each
// prompt it runs with _x.ai/queue/changed (runningPromptId, its prompt id prompt-<n>, also in the result's _meta) and
// its end with runningPromptId null; FAKEACP_LATE_QUEUE=1 announces a client's prompt once more after its answer.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

func flavor() string {
	if f := os.Getenv("FAKEACP_FLAVOR"); f != "" {
		return f
	}
	if strings.HasPrefix(filepath.Base(os.Args[0]), "agy") {
		return "agy"
	}
	return "grok"
}

func home() string {
	if h := os.Getenv("FAKEACP_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".fakeacp")
}

func loggedOut() bool {
	_, err := os.Stat(filepath.Join(home(), ".logged-out"))
	return err == nil
}

func main() {
	if f := os.Getenv("FAKEACP_ARGS_FILE"); f != "" {
		env := map[string]string{}
		for _, k := range []string{"HOME", "XAI_API_KEY", "GEMINI_API_KEY", "GROK_HOME", "FAKEACP_PROBE"} {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": env})
		if out, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			out.Write(append(b, '\n'))
			out.Close()
		}
	}
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--version" || args[0] == "models") {
		// FAKEACP_SLOW delays these short commands; FAKEACP_HANG=<flavor> makes them hang for that flavor.
		if d, err := time.ParseDuration(os.Getenv("FAKEACP_SLOW")); err == nil {
			time.Sleep(d)
		}
		if os.Getenv("FAKEACP_HANG") == flavor() {
			select {}
		}
	}
	switch {
	case len(args) > 0 && args[0] == "--version":
		if flavor() == "agy" {
			fmt.Println("1.5.14")
		} else {
			fmt.Println("grok 1.0.88 (fake)")
		}
	case len(args) > 0 && args[0] == "models":
		if flavor() == "agy" {
			if loggedOut() {
				fmt.Fprintln(os.Stderr, "Error: not logged in")
				os.Exit(1)
			}
			fmt.Println("Fetching available models...\nfake-high\tFake (High)\nfake-low\tFake (Low)")
			return
		}
		if loggedOut() {
			fmt.Println("You are not logged in. Run grok login.")
			return
		}
		fmt.Println("You are logged in with grok.com.\n\nDefault model: fake-1\n\nAvailable models:\n  * fake-1 (default)\n  - fake-2")
	case slices.Contains(args, "stdio") || slices.Contains(args, "acp"):
		serve(args)
	default:
		fmt.Fprintln(os.Stderr, "fakeacp: unknown arguments", args)
		os.Exit(2)
	}
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type session struct {
	ID      string   `json:"id"`
	Cwd     string   `json:"cwd"`
	Prompts []string `json:"prompts"`
	MCP     []string `json:"-"`
	Model   string   `json:"-"`
	Mode    string   `json:"-"`
}

func sessionPath(id string) string { return filepath.Join(home(), "sessions", id+".json") }

func (s *session) save() {
	os.MkdirAll(filepath.Dir(sessionPath(s.ID)), 0o700)
	b, _ := json.Marshal(s)
	os.WriteFile(sessionPath(s.ID), b, 0o600)
}

type server struct {
	args     []string
	out      sync.Mutex
	mu       sync.Mutex
	sessions map[string]*session
	nextReq  int
	waiting  map[string]chan json.RawMessage
	cancels  map[string]chan struct{}
	// running are the background commands still going (tool call id to session id); ended counts, per session, the
	// commands that ended since the agent's last reply began; waking is set while the agent replies on its own.
	running map[string]string
	ended   map[string]int
	waking  map[string]bool
	// then is the command (its duration) a background command's own reply starts, by tool call id; next are those due
	// in the session's next own reply.
	then map[string]string
	next map[string][]string
	// tasks are Grok's background tasks still running, by task id; turns is held, per session, while a prompt (the
	// client's or Grok's own) runs.
	tasks map[string]*grokTask
	turns map[string]*sync.Mutex
}

type grokTask struct {
	sid, id, command string
	timer            *time.Timer
}

// turnLock is the session's prompt lock.
func (s *server) turnLock(sid string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turns[sid] == nil {
		s.turns[sid] = &sync.Mutex{}
	}
	return s.turns[sid]
}

func (s *server) xai(method string, sid string, update map[string]any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": map[string]any{"sessionId": sid, "update": update}})
}

// grokTaskList is the background_tasks list of a session's running tasks.
func (s *server) grokTaskList(sid string, ended *grokTask, exit any) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []any{}
	for _, t := range s.tasks {
		if t.sid == sid {
			list = append(list, map[string]any{"task_id": t.id, "command": t.command, "description": "Run it", "kind": "bash", "status": "running"})
		}
	}
	if ended != nil {
		list = append(list, map[string]any{"task_id": ended.id, "command": ended.command, "description": "Run it", "kind": "bash", "status": "completed", "exit_code": exit})
	}
	return map[string]any{"sessionUpdate": "background_tasks", "tasks": list}
}

// endGrokTask ends a background task, by its time or killed, and has Grok reply by itself once no prompt runs.
func (s *server) endGrokTask(id string, killed bool) bool {
	s.mu.Lock()
	t := s.tasks[id]
	delete(s.tasks, id)
	s.mu.Unlock()
	if t == nil {
		return false
	}
	t.timer.Stop()
	snapshot := map[string]any{"task_id": t.id, "command": t.command, "output": strings.Repeat("noise\n", 1000) + "finished\n", "exit_code": 0, "signal": nil,
		"completed": true, "kind": "bash", "explicitly_killed": false}
	var exit any = 0
	if killed {
		snapshot["output"], snapshot["exit_code"], snapshot["signal"], snapshot["completed"], snapshot["explicitly_killed"] = "", nil, "SIGKILL", false, true
		exit = nil
	}
	prompt := "task-completed-" + t.id
	announce := func() {
		s.write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/queue/changed", "params": map[string]any{"sessionId": t.sid, "entries": []any{}, "runningPromptId": prompt}})
	}
	ended := func() {
		s.xai("_x.ai/task_completed", t.sid, map[string]any{"sessionUpdate": "task_completed", "task_snapshot": snapshot})
		s.xai("_x.ai/session_notification", t.sid, s.grokTaskList(t.sid, t, exit))
	}
	lock := s.turnLock(t.sid)
	idle := lock.TryLock()
	if idle {
		// As Grok 1.0.92 does with no prompt running: its own prompt is announced before the task's end.
		announce()
		ended()
	} else {
		ended()
	}
	go func() {
		if !idle {
			lock.Lock()
			announce()
		}
		defer lock.Unlock()
		s.update(t.sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "background done"}})
		u := map[string]any{"inputTokens": 700, "outputTokens": 9, "totalTokens": 709, "cachedReadTokens": 500, "cacheCreationTokens": 0, "reasoningTokens": 4, "modelCalls": 1}
		u["modelUsage"] = map[string]any{"fake-1-build": maps.Clone(u)}
		s.xai("_x.ai/session_notification", t.sid, map[string]any{"sessionUpdate": "turn_completed", "prompt_id": prompt, "stop_reason": "end_turn", "usage": u})
		s.write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/queue/changed", "params": map[string]any{"sessionId": t.sid, "entries": []any{}, "runningPromptId": nil}})
	}()
	return true
}

func (s *server) write(v any) {
	b, _ := json.Marshal(v)
	s.out.Lock()
	os.Stdout.Write(append(b, '\n'))
	s.out.Unlock()
}

func (s *server) result(id json.RawMessage, v any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
}

func (s *server) fail(id json.RawMessage, code int, msg string, data any) {
	e := map[string]any{"code": code, "message": msg}
	if data != nil {
		e["data"] = data
	}
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (s *server) update(sid string, u map[string]any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": u}})
}

func (s *server) ask(method string, params any) json.RawMessage {
	s.mu.Lock()
	s.nextReq++
	id := fmt.Sprintf("agent-%d", s.nextReq)
	ch := make(chan json.RawMessage, 1)
	s.waiting[id] = ch
	s.mu.Unlock()
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		return nil
	}
}

// toolEnd is the end of a tool call as agy sends it: the output as rawOutput and as content, a list of ToolCallContent
// (a message chunk's content is a single block).
func toolEnd(tid, status, output string) map[string]any {
	return map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tid, "status": status, "kind": "execute",
		"rawOutput": map[string]any{"output": output, "exit_code": 0},
		"content":   []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": output}}}}
}

func usage() map[string]any {
	return map[string]any{"sessionUpdate": "usage_update", "used": 100, "size": 1000}
}

// commandEnded reports the end of a background command (unless session/cancel ended it) and wakes the agent, which
// then replies by itself.
func (s *server) commandEnded(sid, tid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[tid]; !ok {
		return
	}
	delete(s.running, tid)
	s.update(sid, toolEnd(tid, "completed", "finished"))
	s.ended[sid]++
	if d, ok := s.then[tid]; ok {
		delete(s.then, tid)
		s.next[sid] = append(s.next[sid], d)
	}
	if !s.waking[sid] {
		s.waking[sid] = true
		go s.wake(sid)
	}
}

// startCommand starts a command as agy's run_command (see fake:background) for the word w; then is the command the
// agent's own reply about its end starts.
func (s *server) startCommand(sid, w, then string) {
	tid := newID()
	input := map[string]any{"CommandLine": "sleep " + w, "WaitMsBeforeAsync": 500}
	if w == "daemon" {
		input["IsDaemon"] = true
	}
	s.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": input["CommandLine"], "kind": "execute", "status": "in_progress", "rawInput": input})
	if w == "now" {
		s.update(sid, toolEnd(tid, "completed", "done now"))
		return
	}
	if d, err := time.ParseDuration(w); err == nil {
		s.mu.Lock()
		s.running[tid] = sid
		if then != "" {
			s.then[tid] = then
		}
		s.mu.Unlock()
		go func() {
			time.Sleep(d)
			s.commandEnded(sid, tid)
		}()
	}
}

// wake is the agent replying by itself to the commands that ended, one reply after another while more end.
func (s *server) wake(sid string) {
	delay, _ := time.ParseDuration(os.Getenv("FAKEACP_WAKE_DELAY"))
	for {
		time.Sleep(delay)
		s.mu.Lock()
		n := s.ended[sid]
		s.ended[sid] = 0
		if n == 0 {
			s.waking[sid] = false
			s.mu.Unlock()
			return
		}
		next := s.next[sid]
		delete(s.next, sid)
		s.mu.Unlock()
		s.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "\n\nreported "}})
		time.Sleep(delay)
		s.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": fmt.Sprint(n)}})
		for _, w := range next {
			s.startCommand(sid, w, "")
		}
		s.update(sid, usage())
	}
}

func models() map[string]any {
	return map[string]any{"currentModelId": "fake-1", "availableModels": []any{
		map[string]any{"modelId": "fake-1", "name": "Fake One", "_meta": map[string]any{"reasoningEfforts": []any{
			map[string]any{"id": "high", "default": true}, map[string]any{"id": "low", "default": false}}}},
		map[string]any{"modelId": "fake-2", "name": "Fake Two"},
	}}
}

func serve(args []string) {
	s := &server{args: args, sessions: map[string]*session{}, waiting: map[string]chan json.RawMessage{}, cancels: map[string]chan struct{}{},
		running: map[string]string{}, ended: map[string]int{}, waking: map[string]bool{}, then: map[string]string{}, next: map[string][]string{}, tasks: map[string]*grokTask{}, turns: map[string]*sync.Mutex{}}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" {
			var id string
			json.Unmarshal(m.ID, &id)
			s.mu.Lock()
			ch := s.waiting[id]
			delete(s.waiting, id)
			s.mu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
			continue
		}
		var p map[string]any
		json.Unmarshal(m.Params, &p)
		if m.ID == nil {
			if m.Method == "session/cancel" {
				sid, _ := p["sessionId"].(string)
				s.mu.Lock()
				if c := s.cancels[sid]; c != nil {
					close(c)
					delete(s.cancels, sid)
				}
				for tid, owner := range s.running {
					if owner == sid {
						delete(s.running, tid)
					}
				}
				s.mu.Unlock()
			}
			continue
		}
		s.request(m.ID, m.Method, p)
	}
}

func (s *server) request(id json.RawMessage, method string, p map[string]any) {
	str := func(k string) string { v, _ := p[k].(string); return v }
	mcpNames := func() []string {
		var out []string
		list, _ := p["mcpServers"].([]any)
		for _, e := range list {
			m, _ := e.(map[string]any)
			n, _ := m["name"].(string)
			out = append(out, n)
		}
		return out
	}
	switch method {
	case "initialize":
		res := map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true, "promptCapabilities": map[string]any{"image": true}}}
		if flavor() != "agy" {
			res["_meta"] = map[string]any{"agentVersion": "1.0.88", "modelState": models()}
		}
		s.result(id, res)
	case "session/new":
		ss := &session{ID: newID(), Cwd: str("cwd"), MCP: mcpNames()}
		s.mu.Lock()
		s.sessions[ss.ID] = ss
		s.mu.Unlock()
		nativeRecord(ss)
		s.result(id, map[string]any{"sessionId": ss.ID, "models": models(), "modes": sessionModes()})
	case "session/load":
		sid := str("sessionId")
		b, err := os.ReadFile(sessionPath(sid))
		ss := &session{ID: sid, Cwd: str("cwd"), MCP: mcpNames()}
		if err != nil && flavor() != "agy" {
			s.fail(id, -32603, "Path not found.", map[string]any{"code": "FS_NOT_FOUND"})
			return
		}
		if err == nil {
			json.Unmarshal(b, ss)
			ss.Cwd, ss.MCP = str("cwd"), mcpNames()
			for _, prompt := range ss.Prompts {
				s.update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": prompt}})
				s.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "earlier answer"}})
			}
		}
		s.mu.Lock()
		s.sessions[sid] = ss
		s.mu.Unlock()
		if os.Getenv("FAKEACP_LOAD_SILENT") == "1" && len(ss.Prompts) > 0 {
			return
		}
		s.result(id, map[string]any{"models": models()})
	case "session/set_mode":
		s.mu.Lock()
		if ss := s.sessions[str("sessionId")]; ss != nil {
			ss.Mode = str("modeId")
		}
		s.mu.Unlock()
		s.result(id, map[string]any{})
	case "session/set_model":
		s.mu.Lock()
		if ss := s.sessions[str("sessionId")]; ss != nil {
			ss.Model = str("modelId")
		}
		s.mu.Unlock()
		s.result(id, map[string]any{})
	case "_x.ai/task/kill":
		sid, task := str("sessionId"), str("taskId")
		s.mu.Lock()
		known := s.sessions[sid] != nil
		s.mu.Unlock()
		if !known {
			s.result(id, map[string]any{"result": nil, "error": "session not found"})
			return
		}
		outcome := "not_found"
		if s.endGrokTask(task, true) {
			outcome = "killed"
		}
		s.result(id, map[string]any{"result": map[string]any{"task_id": task, "outcome": outcome}, "error": nil})
	case "session/prompt":
		s.mu.Lock()
		ss := s.sessions[str("sessionId")]
		cancel := make(chan struct{})
		if ss != nil {
			s.cancels[ss.ID] = cancel
		}
		s.mu.Unlock()
		if ss == nil {
			s.fail(id, -32602, "unknown session", nil)
			return
		}
		nativePrompt(ss, p)
		go s.prompt(id, ss, p, cancel)
	default:
		s.fail(id, -32601, "fakeacp: unknown method "+method, nil)
	}
}

func (s *server) prompt(id json.RawMessage, ss *session, p map[string]any, cancel chan struct{}) {
	text, images := "", 0
	for _, b := range p["prompt"].([]any) {
		m := b.(map[string]any)
		switch m["type"] {
		case "text":
			text += m["text"].(string)
		case "image":
			images++
		}
	}
	if loggedOut() {
		s.fail(id, -32000, "Authentication required", nil)
		return
	}
	lock := s.turnLock(ss.ID)
	lock.Lock()
	defer lock.Unlock()
	prior := len(ss.Prompts)
	promptID := fmt.Sprintf("prompt-%d", prior+1)
	if flavor() != "agy" {
		queue := func(running any) {
			s.write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/queue/changed", "params": map[string]any{"sessionId": ss.ID, "entries": []any{}, "runningPromptId": running}})
		}
		queue(promptID)
		defer func() {
			queue(nil)
			if os.Getenv("FAKEACP_LATE_QUEUE") == "1" {
				go func() {
					time.Sleep(100 * time.Millisecond)
					queue(promptID)
				}()
			}
		}()
	}
	ss.Prompts = append(ss.Prompts, text)
	ss.save()
	reply := func(t string) {
		half := len(t) / 2
		for _, part := range []string{t[:half], t[half:]} {
			if part != "" {
				s.update(ss.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": part}})
			}
		}
	}
	stop := func(reason string) { s.result(id, map[string]any{"stopReason": reason}) }
	// A prompt that ends normally reports its usage as the real CLIs do (see promptUsage).
	ended := func() { s.result(id, s.promptUsage(ss.ID, promptID)) }
	cmd, arg := "", ""
	if i := strings.Index(text, "fake:"); i >= 0 {
		w := strings.Fields(text[i:])
		cmd = w[0]
		if len(w) > 1 {
			arg = strings.Join(w[1:], " ")
		}
	}
	switch cmd {
	case "fake:history":
		reply(fmt.Sprintf("history=%d", prior))
	case "fake:config":
		b, _ := json.Marshal(map[string]any{"cwd": ss.Cwd, "mcp": ss.MCP, "model": ss.Model, "args": s.args, "mode": ss.Mode})
		reply(string(b))
	case "fake:env":
		env := map[string]string{}
		for _, k := range []string{"HOME", "XAI_API_KEY", "GEMINI_API_KEY"} {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		b, _ := json.Marshal(env)
		reply(string(b))
	case "fake:images":
		reply(fmt.Sprintf("images=%d", images))
	case "fake:tool", "fake:toolfail":
		tid := newID()
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": "Run " + arg, "kind": "execute", "status": "in_progress", "rawInput": map[string]any{"command": arg}})
		status := "completed"
		if cmd == "fake:toolfail" {
			status = "failed"
		}
		s.update(ss.ID, toolEnd(tid, status, "ran "+arg))
		reply("ran")
	case "fake:texttool":
		reply("Let me run it.")
		tid := newID()
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": "Run true", "kind": "execute", "status": "in_progress", "rawInput": map[string]any{"command": "true"}})
		s.update(ss.ID, toolEnd(tid, "completed", "ran true"))
		reply("ran")
	case "fake:toolopen":
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": newID(), "title": "Run " + arg, "kind": "execute", "status": "in_progress", "rawInput": map[string]any{"command": arg}})
		reply("ran")
	case "fake:background":
		for _, w := range strings.Fields(arg) {
			w, then, _ := strings.Cut(w, "+")
			s.startCommand(ss.ID, w, then)
		}
		reply("backgrounded")
		s.update(ss.ID, usage())
	case "fake:bgtask":
		durText, command, _ := strings.Cut(arg, " ")
		dur, _ := time.ParseDuration(durText)
		tid, task := newID(), newID()
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": "Run " + command, "kind": "execute", "status": "pending",
			"rawInput": map[string]any{"command": command, "description": "Run it", "timeout": 0, "background": true}})
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tid, "status": "completed", "rawOutput": "[bg] " + command + " (" + task + ")"})
		t := &grokTask{sid: ss.ID, id: task, command: command}
		s.mu.Lock()
		s.tasks[task] = t
		s.mu.Unlock()
		t.timer = time.AfterFunc(dur, func() { s.endGrokTask(task, false) })
		s.xai("_x.ai/task_backgrounded", ss.ID, map[string]any{"sessionUpdate": "task_backgrounded", "tool_call_id": tid, "task_id": task, "command": command,
			"cwd": ss.Cwd, "output_file": filepath.Join(home(), task+".log"), "description": "Run it"})
		s.xai("_x.ai/session_notification", ss.ID, s.grokTaskList(ss.ID, nil, nil))
		reply("started")
	case "fake:permission":
		r := s.ask("session/request_permission", map[string]any{"sessionId": ss.ID, "toolCall": map[string]any{"toolCallId": newID(),
			"title": "Run make deploy", "kind": "execute", "rawInput": map[string]any{"command": "make deploy"}},
			"options": []any{map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}, map[string]any{"optionId": "yes", "name": "Allow", "kind": "allow_once"}}})
		var out struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		json.Unmarshal(r, &out)
		reply("permission=" + out.Outcome.OptionID)
	case "fake:sleep":
		var d time.Duration
		if f := strings.Fields(arg); len(f) > 0 {
			d, _ = time.ParseDuration(f[0])
		}
		select {
		case <-cancel:
			stop("cancelled")
			return
		case <-time.After(d):
		}
		reply("slept")
	case "fake:slow", "fake:noack":
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		deadline := time.After(30 * time.Second)
		for {
			select {
			case <-cancel:
				if cmd == "fake:slow" {
					stop("cancelled")
					return
				}
				cancel = nil
			case <-tick.C:
				reply(".")
			case <-deadline:
				stop("end_turn")
				return
			}
		}
	case "fake:refusal":
		stop("refusal")
		return
	case "fake:exit":
		reply("partial")
		time.Sleep(50 * time.Millisecond)
		os.Exit(3)
	default:
		reply("echo: " + text)
	}
	ended()
}

// promptUsage is the result of a prompt that ends normally, with its token usage as the real CLIs report it (numbers
// made up): agy 1.5.14 in the result's usage (input leaves out cache reads; output includes thoughts), after a
// usage_update; Grok 1.0.92 in its _meta.usage (input includes cache reads; output includes reasoning), with flat
// copies in _meta and the same usage in its own turn_completed notification first.
func (s *server) promptUsage(sid, promptID string) map[string]any {
	if flavor() == "agy" {
		s.update(sid, usage())
		return map[string]any{"stopReason": "end_turn", "usage": map[string]any{"inputTokens": 1200, "outputTokens": 30, "cachedReadTokens": 800,
			"cachedWriteTokens": 0, "thoughtTokens": 12, "totalTokens": 2030}}
	}
	u := map[string]any{"inputTokens": 1931, "outputTokens": 31, "totalTokens": 1962, "cachedReadTokens": 217, "cacheCreationTokens": 0,
		"reasoningTokens": 30, "modelCalls": 1, "apiDurationMs": 3111, "costUsdTicks": 355600000}
	u["modelUsage"] = map[string]any{"fake-1-build": maps.Clone(u)}
	s.write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/session_notification", "params": map[string]any{"sessionId": sid,
		"update": map[string]any{"sessionUpdate": "turn_completed", "prompt_id": promptID, "usage": u}}})
	return map[string]any{"stopReason": "end_turn", "_meta": map[string]any{"usage": u, "totalTokens": 1962, "modelId": "fake-1", "inputTokens": 1931, "promptId": promptID,
		"outputTokens": 31}}
}

// nativeRecord leaves the records the real CLI keeps for a new session under FAKEACP_NATIVE_HOME (see the package
// comment).
func nativeRecord(ss *session) {
	root := os.Getenv("FAKEACP_NATIVE_HOME")
	if root == "" {
		return
	}
	if flavor() == "agy" {
		dir := filepath.Join(root, ".gemini", "antigravity-cli")
		_ = os.MkdirAll(filepath.Join(dir, "conversations"), 0o700)
		_ = os.MkdirAll(filepath.Join(dir, "brain", ss.ID), 0o700)
		for _, f := range []string{filepath.Join(dir, "conversations", ss.ID+".db"), filepath.Join(dir, "conversations", ss.ID+".db-wal"), filepath.Join(dir, "brain", ss.ID, "notes.md")} {
			_ = os.WriteFile(f, []byte("record"), 0o600)
		}
		return
	}
	dir := filepath.Join(root, ".grok", "sessions", url.PathEscape(ss.Cwd), ss.ID)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte("record"), 0o600)
}

// nativePrompt appends a prompt to Grok's prompt history of the session's folder under FAKEACP_NATIVE_HOME, as the real
// one does.
func nativePrompt(ss *session, p map[string]any) {
	root := os.Getenv("FAKEACP_NATIVE_HOME")
	if root == "" || flavor() == "agy" {
		return
	}
	dir := filepath.Join(root, ".grok", "sessions", url.PathEscape(ss.Cwd))
	_ = os.MkdirAll(dir, 0o700)
	b, _ := json.Marshal(map[string]any{"sessionId": ss.ID, "prompt": p["prompt"]})
	if f, err := os.OpenFile(filepath.Join(dir, "prompt_history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.Write(append(b, '\n'))
		f.Close()
	}
}

// sessionModes are the session modes a new session offers, as the real agents do: agy's default, accept-edits, auto and
// plan (agy 1.5.14); Grok lists none (1.0.92).
func sessionModes() any {
	if flavor() != "agy" {
		return nil
	}
	var modes []any
	for _, id := range []string{"default", "accept-edits", "auto", "plan"} {
		modes = append(modes, map[string]any{"id": id, "name": id})
	}
	return map[string]any{"currentModeId": "default", "availableModes": modes}
}
