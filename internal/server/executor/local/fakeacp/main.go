// Command fakeacp is a fake Agent Client Protocol agent for tests, playing Grok or Antigravity (FAKEACP_FLAVOR=grok,
// the default, or agy): --version and models answer as those CLIs do, and any arguments containing stdio or acp serve
// the protocol (version 1) over stdio, without network access and without reading credentials. Sessions are saved
// under $FAKEACP_HOME/sessions once they have had a prompt. Loading an unknown session fails like Grok ("Path not
// found.") or, for agy, succeeds empty, as the real ones do. FAKEACP_LOAD_SILENT=1 replays a session's history but
// never answers session/load (as Grok sometimes does). $FAKEACP_HOME/.logged-out makes models report logged out and
// prompts fail with auth_required. FAKEACP_ARGS_FILE gets one line per start (JSON: arguments and environment).
//
// The first "fake:" word of a prompt decides the reply:
//
//	fake:history      reply history=<earlier prompts in this session>
//	fake:config       reply with the session's settings (JSON: cwd, MCP server names, model)
//	fake:env          reply with the environment variables the platform cares about (JSON)
//	fake:images       reply images=<number of images>
//	fake:tool <cmd>   run one execute tool call, then reply ran
//	fake:toolfail     run one tool call that fails, then reply ran
//	fake:permission   ask for permission, then reply permission=<chosen option>
//	fake:slow         stream a dot every 50ms until cancelled
//	fake:noack        the same, but ignore session/cancel
//	fake:refusal      stop with refusal
//	fake:exit         stream a little text, then exit with code 3
//	anything else     reply echo: <text>
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
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
		for _, k := range []string{"HOME", "XAI_API_KEY", "GEMINI_API_KEY", "GROK_HOME"} {
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

func models() map[string]any {
	return map[string]any{"currentModelId": "fake-1", "availableModels": []any{
		map[string]any{"modelId": "fake-1", "name": "Fake One", "_meta": map[string]any{"reasoningEfforts": []any{
			map[string]any{"id": "high", "default": true}, map[string]any{"id": "low", "default": false}}}},
		map[string]any{"modelId": "fake-2", "name": "Fake Two"},
	}}
}

func serve(args []string) {
	s := &server{args: args, sessions: map[string]*session{}, waiting: map[string]chan json.RawMessage{}, cancels: map[string]chan struct{}{}}
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
		if flavor() == "grok" {
			res["_meta"] = map[string]any{"agentVersion": "1.0.88", "modelState": models()}
		}
		s.result(id, res)
	case "session/new":
		ss := &session{ID: newID(), Cwd: str("cwd"), MCP: mcpNames()}
		s.mu.Lock()
		s.sessions[ss.ID] = ss
		s.mu.Unlock()
		s.result(id, map[string]any{"sessionId": ss.ID, "models": models()})
	case "session/load":
		sid := str("sessionId")
		b, err := os.ReadFile(sessionPath(sid))
		ss := &session{ID: sid, Cwd: str("cwd"), MCP: mcpNames()}
		if err != nil && flavor() == "grok" {
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
	case "session/set_model":
		s.mu.Lock()
		if ss := s.sessions[str("sessionId")]; ss != nil {
			ss.Model = str("modelId")
		}
		s.mu.Unlock()
		s.result(id, map[string]any{})
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
	prior := len(ss.Prompts)
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
		b, _ := json.Marshal(map[string]any{"cwd": ss.Cwd, "mcp": ss.MCP, "model": ss.Model, "args": s.args})
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
		s.update(ss.ID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tid, "status": status})
		reply("ran")
	case "fake:permission":
		r := s.ask("session/request_permission", map[string]any{"sessionId": ss.ID, "toolCall": map[string]any{"toolCallId": newID()},
			"options": []any{map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}, map[string]any{"optionId": "yes", "name": "Allow", "kind": "allow_once"}}})
		var out struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		json.Unmarshal(r, &out)
		reply("permission=" + out.Outcome.OptionID)
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
	stop("end_turn")
}
