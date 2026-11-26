// Command fakecodex is a fake Codex CLI for tests: it answers --version and login status, and `app-server` speaks the
// subset of the app-server protocol the platform uses (JSON-RPC over stdio, one JSON per line), without network access
// and without reading any credentials. Like the real one, a thread is saved (under $CODEX_HOME/fake-threads) only
// once it has had a turn; resuming, archiving or listing a thread that was never saved fails with "no rollout found".
// The granular approval policy is refused. thread/backgroundTerminals/terminate needs the experimental API asked for at
// initialize (as the real one does): it ends a command still running in the background (by its processId).
//
// The text of each message decides the reply (the first "fake:" word in it):
//
//	fake:history        reply history=<number of earlier turns in this thread> (forked threads count their copy)
//	fake:env            reply with the environment variables the platform cares about (JSON)
//	fake:config         reply with the thread and turn settings it received (JSON)
//	fake:images         reply images=<number of images>
//	fake:tool <cmd>     run one command item, then reply ran
//	fake:toolfail       run one command item that fails, then reply ran
//	fake:files          a file change of two files and a web search whose query comes with its end, then reply files
//	fake:approval       ask the client to approve a command, then reply approval=<decision>
//	fake:ask            ask the client a question, then reply answers=<JSON>
//	fake:sleep <dur>    wait for the duration (the word after it), or until interrupted, then reply slept
//	fake:slow           stream a dot every 50ms until interrupted
//	fake:noack          the same, but ignore turn/interrupt
//	fake:error <kind>   end the turn failed, with codexErrorInfo <kind>
//	fake:retry          report an error that will be retried, then reply ok
//	fake:usage          report token usage and rate limits, then reply usage
//	fake:exit           stream a little text, then exit with code 3
//	fake:whole          send the reply as one completed item without deltas
//	fake:two            reply with two message items, first and second
//	fake:background <dur> <cmd>
//	                    start a command item (processId pty-<n>) and complete the turn while it runs; the item completes
//	                    after <dur> (exit code 0, its output in aggregatedOutput), with no turn of its own
//	anything else       reply echo: <text>
//
// `exec` runs one turn on the message read from stdin (the last argument is -) and prints its events as JSON lines
// (--json), writing the last message to the file of --output-last-message. The first "fake:" word anywhere in the
// message decides: fake:args, fake:env and fake:stdin (the message as read) reply with those; fake:tool <cmd> starts a
// command item, waits 2 seconds, then replies ran; fake:error <msg> ends with turn.failed and exit code 1; fake:slow
// waits until it is killed; fake:empty replies nothing; anything else replies echo: <message>. Without --ephemeral it
// saves the session under $CODEX_HOME/sessions, as the real one does.
//
// Environment: FAKECODEX_VERSION changes the version; FAKECODEX_ARGS_FILE gets one line per start (JSON: arguments
// and environment); login status reports logged out when $CODEX_HOME/.fakecodex-logged-out exists;
// FAKECODEX_MODELS (JSON array) replaces the model list, and so does $CODEX_HOME/.fakecodex-models.json; when
// $CODEX_HOME/.fakecodex-revoked exists, model/list logs to stderr, as Codex 0.167.0 does, that refreshing the list
// online was rejected (401 token_revoked) and answers from its cache.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func version() string {
	if v := os.Getenv("FAKECODEX_VERSION"); v != "" {
		return v
	}
	return "0.167.0"
}

func main() {
	if f := os.Getenv("FAKECODEX_ARGS_FILE"); f != "" {
		env := map[string]string{}
		for _, k := range []string{"CODEX_HOME", "HOME", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_SANDBOX"} {
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
		fmt.Println("codex-cli " + version())
	case len(args) > 1 && args[0] == "login" && args[1] == "status":
		if _, err := os.Stat(filepath.Join(codexHome(), ".fakecodex-logged-out")); err == nil {
			fmt.Fprintln(os.Stderr, "Not logged in")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Logged in using ChatGPT")
	case len(args) > 0 && args[0] == "app-server":
		serve()
	case len(args) > 0 && args[0] == "exec":
		execTurn(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "fakecodex: unknown arguments", args)
		os.Exit(2)
	}
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type savedTurn struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type thread struct {
	ID                    string         `json:"id"`
	Cwd                   string         `json:"cwd"`
	Turns                 []savedTurn    `json:"turns"`
	Model                 string         `json:"-"`
	DeveloperInstructions string         `json:"-"`
	Config                map[string]any `json:"-"`
	ApprovalPolicy        any            `json:"-"`
	Sandbox               any            `json:"-"`
}

func threadPath(id string) string { return filepath.Join(codexHome(), "fake-threads", id+".json") }

func (t *thread) save() {
	os.MkdirAll(filepath.Dir(threadPath(t.ID)), 0o700)
	b, _ := json.Marshal(t)
	os.WriteFile(threadPath(t.ID), b, 0o600)
}

func load(id string) (*thread, error) {
	b, err := os.ReadFile(threadPath(id))
	if err != nil {
		return nil, fmt.Errorf("no rollout found for thread id %s", id)
	}
	var t thread
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

type server struct {
	out     sync.Mutex
	mu      sync.Mutex
	threads map[string]*thread
	roots   []string
	nextReq int
	waiting map[string]chan json.RawMessage // id of a request to the client -> its result
	stops   map[string]chan struct{}        // running turn id -> closed on turn/interrupt
	// experimental: initialize asked for the experimental API; terminals are the commands running in the background,
	// by processId.
	experimental bool
	terminals    map[string]*terminal
	nextPty      int
}

type terminal struct {
	threadID, turnID string
	item             map[string]any
	timer            *time.Timer
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

func (s *server) fail(id json.RawMessage, code int, msg string) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (s *server) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// ask sends a request to the client and waits for its result.
func (s *server) ask(method string, params any) json.RawMessage {
	s.mu.Lock()
	s.nextReq++
	id := fmt.Sprintf("srv-%d", s.nextReq)
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

func serve() {
	s := &server{threads: map[string]*thread{}, waiting: map[string]chan json.RawMessage{}, stops: map[string]chan struct{}{}, terminals: map[string]*terminal{}}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
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
				if m.Error != nil {
					ch <- json.RawMessage(`{"error":true}`)
				} else {
					ch <- m.Result
				}
			}
			continue
		}
		if m.ID == nil {
			continue // a notification (initialized)
		}
		s.request(m.ID, m.Method, m.Params)
	}
}

func (s *server) request(id json.RawMessage, method string, raw json.RawMessage) {
	var p map[string]any
	json.Unmarshal(raw, &p)
	str := func(k string) string { v, _ := p[k].(string); return v }
	switch method {
	case "initialize":
		if caps, ok := p["capabilities"].(map[string]any); ok && caps["experimentalApi"] == true {
			s.mu.Lock()
			s.experimental = true
			s.mu.Unlock()
		}
		s.result(id, map[string]any{"userAgent": "fakecodex/" + version(), "codexHome": codexHome(), "platformFamily": "unix", "platformOs": "macos"})
	case "model/list":
		models := []any{
			map[string]any{"id": "gpt-test", "model": "gpt-test", "displayName": "GPT Test", "hidden": false, "isDefault": true, "defaultReasoningEffort": "medium",
				"inputModalities": []string{"text", "image"}, "supportedReasoningEfforts": efforts("low", "medium", "high", "xhigh", "ultra")},
			map[string]any{"id": "gpt-mini", "model": "gpt-mini", "displayName": "GPT Mini", "hidden": false, "isDefault": false, "defaultReasoningEffort": "low",
				"inputModalities": []string{"text"}, "supportedReasoningEfforts": efforts("low", "medium")},
			map[string]any{"id": "gpt-hidden", "model": "gpt-hidden", "displayName": "Hidden", "hidden": true, "isDefault": false, "defaultReasoningEffort": "low",
				"inputModalities": []string{"text"}, "supportedReasoningEfforts": efforts("low")},
		}
		if custom := os.Getenv("FAKECODEX_MODELS"); custom != "" {
			models = nil
			json.Unmarshal([]byte(custom), &models)
		}
		if custom, err := os.ReadFile(filepath.Join(codexHome(), ".fakecodex-models.json")); err == nil {
			models = nil
			json.Unmarshal(custom, &models)
		}
		if _, err := os.Stat(filepath.Join(codexHome(), ".fakecodex-revoked")); err == nil {
			fmt.Fprintln(os.Stderr, "\x1b[2m2026-11-24T05:27:39.181431Z\x1b[0m \x1b[31mERROR\x1b[0m \x1b[2mcodex_models_manager::manager\x1b[0m\x1b[2m:\x1b[0m failed to refresh available models: unexpected status 401 Unauthorized: Encountered invalidated oauth token for user, failing request, url: https://chatgpt.com/backend-api/codex/models?client_version=0.167.0, auth error: 401, auth error code: token_revoked")
		}
		s.result(id, map[string]any{"data": models, "nextCursor": nil})
	case "thread/start", "thread/resume", "thread/fork":
		if _, granular := p["approvalPolicy"].(map[string]any); granular {
			s.fail(id, -32600, "askForApproval.granular requires experimentalApi capability")
			return
		}
		var t *thread
		switch method {
		case "thread/start":
			t = &thread{ID: newID()}
		case "thread/resume":
			var err error
			if t, err = load(str("threadId")); err != nil {
				s.fail(id, -32600, err.Error())
				return
			}
		case "thread/fork":
			src, err := load(str("threadId"))
			if err != nil {
				s.fail(id, -32600, err.Error())
				return
			}
			t = &thread{ID: newID()}
			for _, turn := range src.Turns {
				t.Turns = append(t.Turns, turn)
				if turn.ID == str("lastTurnId") {
					break
				}
			}
			t.save()
		}
		t.Cwd, t.Model, t.DeveloperInstructions = str("cwd"), str("model"), str("developerInstructions")
		t.Config, _ = p["config"].(map[string]any)
		t.ApprovalPolicy, t.Sandbox = p["approvalPolicy"], p["sandbox"]
		s.mu.Lock()
		s.threads[t.ID] = t
		s.mu.Unlock()
		s.result(id, map[string]any{"thread": map[string]any{"id": t.ID, "turns": []any{}}, "model": t.Model})
	case "thread/turns/list":
		t, err := load(str("threadId"))
		if err != nil {
			s.fail(id, -32600, err.Error())
			return
		}
		var data []any
		for i := len(t.Turns) - 1; i >= 0; i-- {
			data = append(data, map[string]any{"id": t.Turns[i].ID, "items": []any{}, "status": "completed"})
		}
		if limit, ok := p["limit"].(float64); ok && int(limit) < len(data) {
			data = data[:int(limit)]
		}
		s.result(id, map[string]any{"data": data, "nextCursor": nil})
	case "thread/archive":
		tid := str("threadId")
		if _, err := load(tid); err != nil {
			s.fail(id, -32600, err.Error())
			return
		}
		dst := filepath.Join(codexHome(), "fake-archived", tid+".json")
		os.MkdirAll(filepath.Dir(dst), 0o700)
		os.Rename(threadPath(tid), dst)
		s.result(id, map[string]any{})
	case "skills/extraRoots/set":
		s.mu.Lock()
		s.roots = nil
		for _, r := range p["extraRoots"].([]any) {
			s.roots = append(s.roots, r.(string))
		}
		s.mu.Unlock()
		s.result(id, map[string]any{})
	case "turn/start":
		s.mu.Lock()
		t := s.threads[str("threadId")]
		s.mu.Unlock()
		if t == nil {
			s.fail(id, -32600, "thread not loaded: "+str("threadId"))
			return
		}
		turnID := newID()
		stop := make(chan struct{})
		s.mu.Lock()
		s.stops[turnID] = stop
		s.mu.Unlock()
		s.result(id, map[string]any{"turn": map[string]any{"id": turnID, "items": []any{}, "status": "inProgress"}})
		go s.turn(t, turnID, p, stop)
	case "thread/compact/start":
		tid := str("threadId")
		s.result(id, map[string]any{})
		turnID := newID()
		go func() {
			s.notify("turn/started", map[string]any{"threadId": tid, "turn": map[string]any{"id": turnID, "items": []any{}, "status": "inProgress"}})
			item := map[string]any{"type": "contextCompaction", "id": newID()}
			s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": item, "startedAtMs": 0})
			s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": item})
			s.notify("turn/completed", map[string]any{"threadId": tid, "turn": map[string]any{"id": turnID, "items": []any{}, "status": "completed"}})
		}()
	case "thread/backgroundTerminals/terminate":
		s.mu.Lock()
		experimental := s.experimental
		s.mu.Unlock()
		if !experimental {
			s.fail(id, -32600, "thread/backgroundTerminals/terminate requires experimentalApi capability")
			return
		}
		s.result(id, map[string]any{"terminated": s.endTerminal(str("processId"), true)})
	case "turn/interrupt":
		s.mu.Lock()
		stop := s.stops[str("turnId")]
		delete(s.stops, str("turnId"))
		s.mu.Unlock()
		if stop != nil {
			close(stop)
		}
		s.result(id, map[string]any{})
	default:
		s.fail(id, -32601, "fakecodex: unknown method "+method)
	}
}

func efforts(levels ...string) []any {
	var out []any
	for _, l := range levels {
		out = append(out, map[string]any{"reasoningEffort": l, "description": l})
	}
	return out
}

func (s *server) turn(t *thread, turnID string, p map[string]any, stop chan struct{}) {
	tid := t.ID
	text, images := "", 0
	for _, in := range p["input"].([]any) {
		m := in.(map[string]any)
		switch m["type"] {
		case "text":
			text += m["text"].(string)
		case "image":
			images++
		}
	}
	prior := len(t.Turns)
	t.Turns = append(t.Turns, savedTurn{ID: turnID, Text: text})
	t.save()
	s.notify("turn/started", map[string]any{"threadId": tid, "turn": map[string]any{"id": turnID, "items": []any{}, "status": "inProgress"}})
	done := func(status string, turnErr any) {
		s.notify("turn/completed", map[string]any{"threadId": tid, "turn": map[string]any{"id": turnID, "items": []any{}, "status": status, "error": turnErr}})
	}
	reply := func(text string) {
		item := newID()
		s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": item, "text": ""}, "startedAtMs": 0})
		half := len(text) / 2
		for _, part := range []string{text[:half], text[half:]} {
			if part != "" {
				s.notify("item/agentMessage/delta", map[string]any{"threadId": tid, "turnId": turnID, "itemId": item, "delta": part})
			}
		}
		s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": item, "text": text}})
	}
	cmd, arg := "", ""
	if i := strings.Index(text, "fake:"); i >= 0 {
		word := strings.Fields(text[i:])
		cmd = word[0]
		if len(word) > 1 {
			arg = strings.Join(word[1:], " ")
		}
	}
	switch cmd {
	case "fake:history":
		reply(fmt.Sprintf("history=%d", prior))
	case "fake:env":
		env := map[string]string{}
		for _, k := range []string{"CODEX_HOME", "HOME", "OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_SANDBOX"} {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		b, _ := json.Marshal(env)
		reply(string(b))
	case "fake:config":
		s.mu.Lock()
		roots := append([]string(nil), s.roots...)
		s.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"cwd": t.Cwd, "model": p["model"], "effort": p["effort"], "threadModel": t.Model, "developerInstructions": t.DeveloperInstructions,
			"config": t.Config, "approvalPolicy": t.ApprovalPolicy, "sandbox": t.Sandbox, "extraRoots": roots})
		reply(string(b))
	case "fake:images":
		reply(fmt.Sprintf("images=%d", images))
	case "fake:tool", "fake:toolfail":
		item := map[string]any{"type": "commandExecution", "id": newID(), "command": arg, "status": "inProgress"}
		s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": item, "startedAtMs": 0})
		item["status"], item["exitCode"] = "completed", 0
		if cmd == "fake:toolfail" {
			item["status"], item["exitCode"] = "failed", 1
		}
		s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": item})
		reply("ran")
	case "fake:files":
		// A file change knows its files when it starts; a web search's query comes only with its end.
		patch := map[string]any{"type": "fileChange", "id": newID(), "status": "inProgress", "changes": []any{
			map[string]any{"path": "/w/a.go", "kind": map[string]any{"type": "update"}, "diff": "PRIVATE"},
			map[string]any{"path": "/w/b.go", "kind": map[string]any{"type": "add"}, "diff": "PRIVATE"}}}
		s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": patch, "startedAtMs": 0})
		patch["status"] = "completed"
		s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": patch})
		search := map[string]any{"type": "webSearch", "id": newID(), "query": ""}
		s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": search, "startedAtMs": 0})
		search["query"], search["results"] = "go 1.27 release", []any{"PRIVATE"}
		s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": search})
		reply("files")
	case "fake:approval":
		r := s.ask("item/commandExecution/requestApproval", map[string]any{"threadId": tid, "turnId": turnID, "itemId": newID(), "command": "rm -rf build"})
		var decision struct {
			Decision string `json:"decision"`
		}
		json.Unmarshal(r, &decision)
		reply("approval=" + decision.Decision)
	case "fake:ask":
		r := s.ask("item/tool/requestUserInput", map[string]any{"threadId": tid, "turnId": turnID, "itemId": newID(), "questions": []any{}})
		reply("answers=" + string(r))
	case "fake:sleep":
		var d time.Duration
		if f := strings.Fields(arg); len(f) > 0 {
			d, _ = time.ParseDuration(f[0])
		}
		select {
		case <-stop:
			done("interrupted", nil)
			return
		case <-time.After(d):
		}
		reply("slept")
	case "fake:slow", "fake:noack":
		item := newID()
		deadline := time.After(30 * time.Second)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				if cmd == "fake:slow" {
					done("interrupted", nil)
					return
				}
				stop = nil
			case <-tick.C:
				s.notify("item/agentMessage/delta", map[string]any{"threadId": tid, "turnId": turnID, "itemId": item, "delta": "."})
			case <-deadline:
				done("completed", nil)
				return
			}
		}
	case "fake:error":
		turnErr := map[string]any{"message": "boom: " + arg, "codexErrorInfo": arg}
		s.notify("error", map[string]any{"threadId": tid, "turnId": turnID, "willRetry": false, "error": turnErr})
		done("failed", turnErr)
		return
	case "fake:retry":
		s.notify("error", map[string]any{"threadId": tid, "turnId": turnID, "willRetry": true, "error": map[string]any{"message": "retrying", "codexErrorInfo": "serverOverloaded"}})
		reply("ok")
	case "fake:usage":
		s.notify("thread/tokenUsage/updated", map[string]any{"threadId": tid, "turnId": turnID, "tokenUsage": map[string]any{
			"last":  map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120},
			"total": map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120}}})
		s.notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 42}}})
		reply("usage")
	case "fake:exit":
		s.notify("item/agentMessage/delta", map[string]any{"threadId": tid, "turnId": turnID, "itemId": newID(), "delta": "partial"})
		time.Sleep(50 * time.Millisecond)
		os.Exit(3)
	case "fake:whole":
		s.notify("item/completed", map[string]any{"threadId": tid, "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": newID(), "text": "whole"}})
	case "fake:two":
		reply("first")
		reply("second")
	case "fake:background":
		durText, command, _ := strings.Cut(arg, " ")
		dur, _ := time.ParseDuration(durText)
		s.mu.Lock()
		s.nextPty++
		pty := fmt.Sprintf("pty-%d", s.nextPty)
		item := map[string]any{"type": "commandExecution", "id": newID(), "command": command, "cwd": t.Cwd, "processId": pty, "status": "inProgress", "commandActions": []any{}}
		term := &terminal{threadID: tid, turnID: turnID, item: item}
		s.terminals[pty] = term
		s.mu.Unlock()
		s.notify("item/started", map[string]any{"threadId": tid, "turnId": turnID, "item": item, "startedAtMs": 0})
		term.timer = time.AfterFunc(dur, func() { s.endTerminal(pty, false) })
		reply("started")
	default:
		reply("echo: " + text)
	}
	done("completed", nil)
}

// execTurn answers one message as codex exec --json does.
func execTurn(args []string) {
	var outFile string
	ephemeral := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output-last-message", "-o":
			if i+1 < len(args) {
				outFile = args[i+1]
			}
		case "--ephemeral":
			ephemeral = true
		}
	}
	in, _ := io.ReadAll(os.Stdin)
	text := string(in)
	thread := newID()
	if !ephemeral {
		dir := filepath.Join(codexHome(), "sessions")
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "rollout-"+thread+".jsonl"), in, 0o600)
	}
	event := func(v map[string]any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	event(map[string]any{"type": "thread.started", "thread_id": thread})
	event(map[string]any{"type": "turn.started"})
	cmd, arg := "", ""
	if i := strings.Index(text, "fake:"); i >= 0 {
		line, _, _ := strings.Cut(text[i:], "\n")
		cmd, arg, _ = strings.Cut(strings.TrimSpace(line), " ")
	}
	reply := func(r string) {
		event(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_1", "type": "agent_message", "text": r}})
		if outFile != "" {
			_ = os.WriteFile(outFile, []byte(r), 0o600)
		}
	}
	switch cmd {
	case "fake:args":
		b, _ := json.Marshal(os.Args[1:])
		reply(string(b))
	case "fake:env":
		env := map[string]string{}
		for _, k := range []string{"CODEX_HOME", "HOME", "OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_SANDBOX"} {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		b, _ := json.Marshal(env)
		reply(string(b))
	case "fake:stdin":
		reply(text)
	case "fake:tool":
		event(map[string]any{"type": "item.started", "item": map[string]any{"id": "item_0", "type": "command_execution", "command": arg, "status": "in_progress"}})
		time.Sleep(2 * time.Second)
		event(map[string]any{"type": "item.completed", "item": map[string]any{"id": "item_0", "type": "command_execution", "command": arg, "exit_code": 0, "status": "completed"}})
		reply("ran")
	case "fake:error":
		event(map[string]any{"type": "turn.failed", "error": map[string]any{"message": arg}})
		os.Exit(1)
	case "fake:slow":
		time.Sleep(time.Hour)
	case "fake:empty":
	default:
		reply("echo: " + strings.TrimSpace(text))
	}
	event(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})
}

// endTerminal ends a command running in the background: it completes (its time is up) or is terminated; it reports
// whether one was running.
func (s *server) endTerminal(pty string, terminated bool) bool {
	s.mu.Lock()
	t := s.terminals[pty]
	delete(s.terminals, pty)
	s.mu.Unlock()
	if t == nil {
		return false
	}
	t.timer.Stop()
	item := map[string]any{}
	for k, v := range t.item {
		item[k] = v
	}
	item["status"], item["exitCode"], item["aggregatedOutput"] = "completed", 0, strings.Repeat("noise\n", 1000)+"finished\n"
	if terminated {
		item["status"], item["exitCode"], item["aggregatedOutput"] = "failed", -1, "terminated\n"
	}
	s.notify("item/completed", map[string]any{"threadId": t.threadID, "turnId": t.turnID, "item": item})
	return true
}
