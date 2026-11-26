// Command fakeclaude is a fake Claude Code for tests: it speaks the same protocol as claude -p
// --input-format/--output-format stream-json, without network access and without reading any credentials. It also
// answers --version, auth status --json (including fields such as email, to check that the platform drops them) and
// purge.
//
// The text of each user message decides the reply:
//
//	fake:history       reply history=<number of earlier user messages in this session> (continued or forked sessions count too)
//	fake:env           reply with the environment variables the platform cares about (JSON)
//	fake:args          reply with the startup arguments (JSON)
//	fake:images        reply images=<number of images>
//	fake:tool <cmd>    call the Bash tool once, then reply ran
//	fake:tool2 <cmd>   same, but the tool call appears only in the full assistant message and its result is an error; reply ran2
//	fake:files         call Read, Edit, NotebookEdit, Grep, Glob and WebSearch once each (full assistant message), then reply files
//	fake:bigline       print one very long line, then print nothing more
//	fake:subagent      first print some sub-Agent text (non-empty parent_tool_use_id), then reply main
//	fake:error <msg>   end the turn with an error
//	fake:notloggedin   end with api_error, as when not logged in
//	fake:slow          print a dot every 50ms until interrupted (control message or SIGINT)
//	fake:noack         same, but do not answer the interrupt control message, so only SIGINT works
//	fake:exit          print a little text, then exit with exit code 3
//	fake:ratelimit     print a rate_limit_event first, then reply
//	fake:usage2        result carries usage for two models
//	fake:background <dur> <cmd>
//	                   run <cmd> with the Bash tool in the background (as Claude Code 2.1.332 reports it: background_tasks_changed,
//	                   task_started, a tool result naming the task and its output file), reply started; after <dur> the task
//	                   ends (task_updated, task_notification, an empty background_tasks_changed) and, when no turn is going on,
//	                   Claude Code replies by itself: a turn of its own (init, text "background done", result with usage)
//	fake:wait <dur>    reply waited after <dur>
//	fake:ask <cmd>     ask the host to use the Bash tool on <cmd> (a can_use_tool control request, as with
//	                   --permission-prompt-tool stdio, in claude 2.1.332's shape), then reply allowed, or denied:
//	                   <message>; an interrupt while waiting cancels the request (control_cancel_request) and ends the
//	                   turn interrupted. Without --permission-prompt-tool stdio it replies sandbox-denied at once.
//	anything else      reply echo: <text>
//
// FAKECLAUDE_FOLLOWUP_NO_RESULT=1 leaves out the result of such a turn of its own.
//
// FAKECLAUDE_STALE_TASK=<id>[,<id>...] behaves as claude 2.1.332 does when it continues a session in which background
// tasks were still running: 0.3 s after start, before any message is handled, it reports each as stopped
// (task_notification) and then settles them in a turn of its own with no output (session_title_changed, init, result
// success with an empty result and num_turns 0); a message that came meanwhile is handled after it.
//
// The control request stop_task (request.task_id) stops a background task: it ends as stopped (killed), and Claude Code
// replies by itself as after any end; every control request is answered with success.
//
// With --output-format json (print mode, one message read whole from stdin, one result printed) the first "fake:"
// word anywhere in the message decides: fake:args, fake:env and fake:stdin (the message as read) reply as above;
// fake:error <msg> ends with an error result and exit code 1; fake:slow waits until it is killed; fake:empty replies
// nothing; anything else replies echo: <message>. Without --no-session-persistence it writes a session record, as the
// real one does.
//
// Environment variables: FAKECLAUDE_VERSION changes the version; FAKECLAUDE_EXIT_AT_START=1 reports an unknown option
// and exits right at startup; FAKECLAUDE_NO_INIT=1 prints nothing even when a message arrives; FAKECLAUDE_LOGGED_IN=0,
// or a .fakeclaude-logged-out file under HOME, makes auth status report logged out; FAKECLAUDE_AUTH_FAIL=1 makes auth
// status fail; FAKECLAUDE_NO_TRANSCRIPT=1 writes no session record (simulating the platform not finding it);
// FAKECLAUDE_EXTRA_SKILLS (comma-separated) is added to the Skill list in init, simulating bundled Skills added by a
// new claude; FAKECLAUDE_ARGS_FILE gets one line of arguments (JSON) appended per start; purge calls are logged to
// FAKECLAUDE_LOG and to .fakeclaude-purge.log under HOME (one line each: purge <path> -y).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/claudecli"
)

var (
	outMu sync.Mutex
	out   = bufio.NewWriter(os.Stdout)
	// turnMu is held while a turn is written: a reply Claude Code gives by itself waits for the turn in progress, and
	// the next message waits for such a reply.
	turnMu sync.Mutex
	// tasks are the background tasks running, by task id.
	tasksMu sync.Mutex
	tasks   = map[string]*bgTask{}
	nextBg  int
)

type bgTask struct {
	id, toolUse, command, outFile string
	timer                         *time.Timer
}

func emit(v any) {
	b, _ := json.Marshal(v)
	outMu.Lock()
	defer outMu.Unlock()
	out.Write(append(b, '\n'))
	out.Flush()
}

func version() string {
	if v := os.Getenv("FAKECLAUDE_VERSION"); v != "" {
		return v
	}
	return "9.9.9"
}

func main() {
	args := os.Args[1:]
	switch {
	case len(args) > 0 && (args[0] == "--version" || args[0] == "-v"):
		fmt.Println(version() + " (Claude Code)")
		return
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		authStatus()
		return
	case len(args) >= 1 && args[0] == "purge":
		purge(args[1:])
		return
	}
	if f := os.Getenv("FAKECLAUDE_ARGS_FILE"); f != "" {
		b, _ := json.Marshal(args)
		if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fh.Write(append(b, '\n'))
			fh.Close()
		}
	}
	if os.Getenv("FAKECLAUDE_EXIT_AT_START") == "1" {
		fmt.Fprintln(os.Stderr, "error: unknown option '--bogus-flag'")
		os.Exit(1)
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--output-format" && args[i+1] == "json" {
			printMode(args)
			return
		}
	}
	run(args)
}

// printMode answers one message read whole from stdin with one JSON result, as claude -p --output-format json does.
func printMode(args []string) {
	in, _ := io.ReadAll(os.Stdin)
	text := string(in)
	if !slices.Contains(args, "--no-session-persistence") {
		s := &session{id: "fake-print-session"}
		s.path = transcript(s.id)
		s.record(text)
	}
	cmd, arg := "", ""
	if i := strings.Index(text, "fake:"); i >= 0 {
		line, _, _ := strings.Cut(text[i:], "\n")
		cmd, arg, _ = strings.Cut(strings.TrimSpace(line), " ")
	}
	result := func(isError bool, r string) {
		b, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": isError, "duration_ms": 1, "num_turns": 1,
			"result": r, "session_id": "fake-print-session", "total_cost_usd": 0.001})
		fmt.Println(string(b))
	}
	switch cmd {
	case "fake:args":
		b, _ := json.Marshal(os.Args[1:])
		result(false, string(b))
	case "fake:env":
		result(false, envReport())
	case "fake:stdin":
		result(false, text)
	case "fake:error":
		result(true, arg)
		os.Exit(1)
	case "fake:slow":
		time.Sleep(time.Hour)
	case "fake:empty":
		result(false, "")
	default:
		result(false, "echo: "+strings.TrimSpace(text))
	}
}

func authStatus() {
	if os.Getenv("FAKECLAUDE_AUTH_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "auth status failed for private@example.com")
		os.Exit(2)
	}
	home, _ := os.UserHomeDir()
	// The platform runs auth status with a minimal environment, so tests control it with a marker file under HOME
	// instead.
	_, loggedOut := os.Stat(filepath.Join(home, ".fakeclaude-logged-out"))
	// Like the real claude: without USER it cannot find the local login.
	loggedIn := os.Getenv("FAKECLAUDE_LOGGED_IN") != "0" && loggedOut != nil && os.Getenv("USER") != ""
	method := "claude.ai"
	if !loggedIn {
		method = "none"
	}
	b, _ := json.Marshal(map[string]any{"loggedIn": loggedIn, "authMethod": method, "apiProvider": "firstParty", "subscriptionType": "max",
		"planTier": "max_20x", "loginExpiresAt": "2099-01-01T00:00:00Z", "email": "private@example.com", "orgId": "org-private-id", "orgName": "Private Org",
		"configDirectory": filepath.Join(home, ".claude"), "projectsDirectory": filepath.Join(home, ".claude", "projects"), "analyticsDisabled": false, "storedClaudeAiLogin": true})
	fmt.Println(string(b))
	if !loggedIn {
		os.Exit(1) // the real claude exits with 1 when not logged in, but still prints the JSON
	}
}

func purge(args []string) {
	home, _ := os.UserHomeDir()
	// The platform also runs purge with a minimal environment: besides FAKECLAUDE_LOG, it is also logged under HOME.
	for _, f := range []string{os.Getenv("FAKECLAUDE_LOG"), filepath.Join(home, ".fakeclaude-purge.log")} {
		if f == "" {
			continue
		}
		if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(fh, "purge "+strings.Join(args, " "))
			fh.Close()
		}
	}
	fmt.Println("Purged 1 item(s).")
}

// flags are the startup arguments the fake claude cares about.
type flags struct {
	model, sessionID, resume string
	fork                     bool
	// promptTool is --permission-prompt-tool: with stdio, permission prompts come to the host as can_use_tool control
	// requests; without it (as claude 2.1.332 does), a tool that needs approval is denied at once.
	promptTool string
	tools      []string
	mcp        []string
	skills     []string // Skills from --plugin-dir (<plugin name>:<Skill name>) and FAKECLAUDE_EXTRA_SKILLS
}

func parse(args []string) flags {
	var f flags
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--model":
			f.model = next()
		case "--session-id":
			f.sessionID = next()
		case "--resume", "-r":
			f.resume = next()
		case "--permission-prompt-tool":
			f.promptTool = next()
		case "--fork-session":
			f.fork = true
		case "--plugin-dir":
			dir := next()
			var manifest struct {
				Name string `json:"name"`
			}
			if b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil && json.Unmarshal(b, &manifest) == nil {
				entries, _ := os.ReadDir(filepath.Join(dir, "skills"))
				for _, e := range entries {
					if e.IsDir() {
						f.skills = append(f.skills, manifest.Name+":"+e.Name())
					}
				}
			}
		case "--tools":
			for _, t := range strings.Split(next(), ",") {
				if t = strings.TrimSpace(t); t != "" {
					f.tools = append(f.tools, t)
				}
			}
		case "--mcp-config":
			var cfg struct {
				MCPServers map[string]any `json:"mcpServers"`
			}
			v := next()
			raw := []byte(v)
			if !strings.HasPrefix(strings.TrimSpace(v), "{") {
				raw, _ = os.ReadFile(v)
			}
			if json.Unmarshal(raw, &cfg) == nil {
				for name := range cfg.MCPServers {
					f.mcp = append(f.mcp, name)
				}
				sort.Strings(f.mcp)
			}
		}
	}
	for _, s := range strings.Split(os.Getenv("FAKECLAUDE_EXTRA_SKILLS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			f.skills = append(f.skills, s)
		}
	}
	return f
}

func projectsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// transcript is the session record: it lives under ~/.claude/projects/<encoded working directory>, encoded with the
// same function the platform uses (the one applied after path resolution).
func transcript(id string) string {
	cwd, _ := os.Getwd()
	return filepath.Join(projectsDir(), claudecli.ProjectDirName(cwd), id+".jsonl")
}

// history reads the number of user messages already in the session to continue: when resume is a .jsonl path it reads
// that file, otherwise it looks up the session ID under projects.
func history(resume string) (int, []string) {
	path := resume
	if !strings.HasSuffix(resume, ".jsonl") {
		matches, _ := filepath.Glob(filepath.Join(projectsDir(), "*", resume+".jsonl"))
		if len(matches) == 0 {
			fmt.Fprintln(os.Stderr, "No conversation found with session ID: "+resume)
			os.Exit(1)
		}
		path = matches[0]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read session: "+err.Error())
		os.Exit(1)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0, nil
	}
	return len(lines), lines
}

type session struct {
	id    string
	path  string
	count int
}

func (s *session) record(text string) {
	if os.Getenv("FAKECLAUDE_NO_TRANSCRIPT") == "1" {
		s.count++
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": s.id, "text": text})
	if fh, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		fh.Write(append(b, '\n'))
		fh.Close()
	}
	s.count++
}

func openSession(f flags) *session {
	s := &session{id: f.sessionID}
	if f.resume != "" {
		n, lines := history(f.resume)
		if !f.fork && s.id == "" {
			s.id = strings.TrimSuffix(filepath.Base(f.resume), ".jsonl")
		}
		if s.id == "" {
			s.id = "forked-session"
		}
		s.path = transcript(s.id)
		s.count = n
		if f.fork {
			_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
			_ = os.WriteFile(s.path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
		}
		return s
	}
	if s.id == "" {
		s.id = "fake-session"
	}
	s.path = transcript(s.id)
	return s
}

// inbound is one line on stdin.
type inbound struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype string `json:"subtype"`
		TaskID  string `json:"task_id"`
	} `json:"request"`
	Response struct {
		RequestID string `json:"request_id"`
		Response  struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"response"`
	} `json:"response"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func run(args []string) {
	f := parse(args)
	model := "claude-" + f.model + "-fake"
	sess := openSession(f)
	lines := make(chan inbound)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
		for sc.Scan() {
			var in inbound
			if json.Unmarshal(sc.Bytes(), &in) == nil {
				lines <- in
			}
		}
		close(lines)
	}()
	if stale := os.Getenv("FAKECLAUDE_STALE_TASK"); stale != "" {
		turnMu.Lock()
		go func() {
			defer turnMu.Unlock()
			time.Sleep(300 * time.Millisecond)
			settleStale(strings.Split(stale, ","), sess, model)
		}()
	}
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	for {
		select {
		case <-sigint:
			os.Exit(0)
		case in, ok := <-lines:
			if !ok {
				return
			}
			switch in.Type {
			case "control_request":
				emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": in.RequestID, "response": map[string]any{}}})
				if in.Request.Subtype == "stop_task" {
					go endTask(in.Request.TaskID, false, sess, model)
				}
			case "user":
				if os.Getenv("FAKECLAUDE_NO_INIT") == "1" {
					continue
				}
				text, images := content(in.Message.Content)
				turnMu.Lock()
				prior := sess.count
				sess.record(text)
				turn(f, sess, model, text, images, prior, lines, sigint)
				turnMu.Unlock()
			}
		}
	}
}

func content(raw json.RawMessage) (string, int) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, 0
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	text, images := "", 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text += b.Text
		case "image":
			images++
		}
	}
	return text, images
}

func turn(f flags, sess *session, model, text string, images, prior int, lines chan inbound, sigint chan os.Signal) {
	var servers []map[string]string
	for _, name := range f.mcp {
		servers = append(servers, map[string]string{"name": name, "status": "connected"})
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sess.id, "model": model, "claude_code_version": version(),
		"tools": f.tools, "skills": f.skills, "mcp_servers": servers, "permissionMode": "bypassPermissions", "apiKeySource": "none"})
	emit(map[string]any{"type": "system", "subtype": "status", "status": "requesting", "session_id": sess.id})
	emit(map[string]any{"type": "some_future_type", "payload": "ignored"})
	cmd, arg, _ := strings.Cut(text, " ")
	usage := map[string]any{model: map[string]any{"inputTokens": 10, "outputTokens": 5, "cacheReadInputTokens": 3, "cacheCreationInputTokens": 2, "thinkingTokens": 0, "costUSD": 0.01}}
	switch cmd {
	case "fake:history":
		reply(model, fmt.Sprintf("history=%d", prior))
	case "fake:env":
		reply(model, envReport())
	case "fake:args":
		b, _ := json.Marshal(os.Args[1:])
		reply(model, string(b))
	case "fake:images":
		reply(model, fmt.Sprintf("images=%d", images))
	case "fake:tool":
		id := "toolu_fake_1"
		stream(nil, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_tool", "model": model}})
		stream(nil, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{}}})
		emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_tool", "model": model, "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": arg}}}}})
		emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok", "is_error": false}}}})
		reply(model, "ran")
	case "fake:tool2":
		// The tool call appears only in the full assistant message (no content_block_start).
		id := "toolu_fake_2"
		emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_tool2", "model": model, "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": arg}}}}})
		emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok", "is_error": true}}}})
		reply(model, "ran2")
	case "fake:files":
		tools := []any{
			map[string]any{"type": "tool_use", "id": "toolu_read", "name": "Read", "input": map[string]any{"file_path": "/w/a.go", "offset": 3}},
			map[string]any{"type": "tool_use", "id": "toolu_edit", "name": "Edit", "input": map[string]any{"file_path": "/w/b.go", "old_string": "PRIVATE", "new_string": "PRIVATE"}},
			map[string]any{"type": "tool_use", "id": "toolu_nb", "name": "NotebookEdit", "input": map[string]any{"notebook_path": "/w/n.ipynb", "new_source": "PRIVATE"}},
			map[string]any{"type": "tool_use", "id": "toolu_grep", "name": "Grep", "input": map[string]any{"pattern": "func main", "path": "PRIVATE"}},
			map[string]any{"type": "tool_use", "id": "toolu_glob", "name": "Glob", "input": map[string]any{"pattern": "**/*.go"}},
			map[string]any{"type": "tool_use", "id": "toolu_web", "name": "WebSearch", "input": map[string]any{"query": "go 1.27 release"}},
		}
		emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_files", "model": model, "content": tools}})
		for _, tool := range tools {
			id := tool.(map[string]any)["id"]
			emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "PRIVATE", "is_error": false}}}})
		}
		reply(model, "files")
	case "fake:bigline":
		// One very long line of output, then nothing more (if the reader stops reading, this blocks).
		stream(nil, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": strings.Repeat("x", 8192)}})
		time.Sleep(30 * time.Second)
		return
	case "fake:subagent":
		parent := "toolu_task"
		stream(&parent, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "SUBAGENT"}})
		reply(model, "main")
	case "fake:error":
		result(map[string]any{"subtype": "success", "is_error": true, "result": arg, "terminal_reason": "model_error", "api_error_status": 500}, usage)
		return
	case "fake:notloggedin":
		result(map[string]any{"subtype": "success", "is_error": true, "result": "Not logged in · Please run /login", "terminal_reason": "api_error", "api_error_status": nil}, map[string]any{})
		return
	case "fake:ask":
		if f.promptTool != "stdio" {
			reply(model, "sandbox-denied")
			break
		}
		emit(map[string]any{"type": "control_request", "request_id": "perm-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash",
			"display_name": "Bash", "input": map[string]any{"command": arg, "description": "Run " + arg}, "description": "Run " + arg,
			"decision_reason_type": "subcommandResults", "tool_use_id": "toolu_ask"}})
	ask:
		for in := range lines {
			switch {
			case in.Type == "control_response" && in.Response.RequestID == "perm-1":
				if in.Response.Response.Behavior == "allow" {
					reply(model, "allowed")
				} else {
					reply(model, "denied: "+in.Response.Response.Message)
				}
				break ask
			case in.Type == "control_request" && in.Request.Subtype == "interrupt":
				emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": in.RequestID, "response": map[string]any{}}})
				emit(map[string]any{"type": "control_cancel_request", "request_id": "perm-1"})
				result(map[string]any{"subtype": "error_during_execution", "is_error": true, "result": nil, "terminal_reason": "aborted_streaming"}, map[string]any{})
				return
			}
		}
	case "fake:slow", "fake:noack":
		stream(nil, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_slow", "model": model}})
		stream(nil, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		deadline := time.After(30 * time.Second)
		for {
			select {
			case <-tick.C:
				stream(nil, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "."}})
			case in := <-lines:
				if in.Type == "control_request" && cmd == "fake:slow" {
					emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": in.RequestID, "response": map[string]any{"still_queued": []any{}}}})
					emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}})
					result(map[string]any{"subtype": "error_during_execution", "is_error": true, "result": nil, "terminal_reason": "aborted_streaming", "errors": []string{"[ede_diagnostic] interrupted"}}, map[string]any{})
					return
				}
			case <-sigint:
				result(map[string]any{"subtype": "error_during_execution", "is_error": true, "result": nil, "terminal_reason": "aborted_streaming"}, map[string]any{})
				time.Sleep(100 * time.Millisecond)
				os.Exit(0)
			case <-deadline:
				reply(model, "slow-done")
				return
			}
		}
	case "fake:exit":
		stream(nil, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_exit", "model": model}})
		stream(nil, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "partial"}})
		fmt.Fprintln(os.Stderr, "fakeclaude: crashing on purpose")
		os.Exit(3)
	case "fake:ratelimit":
		emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "allowed", "rateLimitType": "five_hour", "resetsAt": 1794382800}})
		reply(model, "limited")
	case "fake:background":
		durText, command, _ := strings.Cut(arg, " ")
		dur, _ := time.ParseDuration(durText)
		tasksMu.Lock()
		nextBg++
		t := &bgTask{id: fmt.Sprintf("b%d", nextBg), toolUse: fmt.Sprintf("toolu_bg_%d", nextBg), command: command}
		t.outFile = filepath.Join(os.TempDir(), fmt.Sprintf("fakeclaude-%d-%s.output", os.Getpid(), t.id))
		tasks[t.id] = t
		list := backgroundList()
		tasksMu.Unlock()
		stream(nil, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_bg", "model": model}})
		stream(nil, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": t.toolUse, "name": "Bash", "input": map[string]any{}}})
		emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_bg", "model": model, "content": []any{map[string]any{"type": "tool_use", "id": t.toolUse, "name": "Bash",
			"input": map[string]any{"command": command, "description": "Run it in the background", "run_in_background": true}}}}})
		emit(map[string]any{"type": "system", "subtype": "background_tasks_changed", "tasks": list, "session_id": sess.id})
		emit(map[string]any{"type": "system", "subtype": "task_started", "task_id": t.id, "tool_use_id": t.toolUse, "is_backgrounded": true, "task_type": "local_bash",
			"description": "Run it in the background", "session_id": sess.id})
		emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": t.toolUse,
			"content": "Command running in background with ID: " + t.id + ". Output is being written to: " + t.outFile, "is_error": false}}}})
		t.timer = time.AfterFunc(dur, func() { endTask(t.id, true, sess, model) })
		reply(model, "started")
	case "fake:wait":
		dur, _ := time.ParseDuration(arg)
		time.Sleep(dur)
		reply(model, "waited")
	case "fake:usage2":
		usage["claude-helper-fake"] = map[string]any{"inputTokens": 1, "outputTokens": 1, "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0, "thinkingTokens": 7}
		reply(model, "two models")
	default:
		reply(model, "echo: "+text)
	}
	result(map[string]any{"subtype": "success", "is_error": false, "result": lastReply, "terminal_reason": "completed"}, usage)
}

var lastReply string

func stream(parent *string, event map[string]any) {
	emit(map[string]any{"type": "stream_event", "event": event, "parent_tool_use_id": parent})
}

// reply gives a reply as streamed text plus the full assistant message.
func reply(model, text string) {
	lastReply = text
	stream(nil, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_reply", "model": model}})
	stream(nil, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, part := range splitHalf(text) {
		stream(nil, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": part}})
	}
	emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_reply", "model": model, "content": []any{map[string]any{"type": "text", "text": text}}}})
	stream(nil, map[string]any{"type": "message_stop"})
}

func splitHalf(s string) []string {
	r := []rune(s)
	if len(r) < 2 {
		return []string{s}
	}
	return []string{string(r[:len(r)/2]), string(r[len(r)/2:])}
}

func result(fields map[string]any, usage map[string]any) {
	m := map[string]any{"type": "result", "num_turns": 1, "total_cost_usd": 0.01, "model_usage_since_last_result": usage, "session_id": "x"}
	for k, v := range fields {
		m[k] = v
	}
	emit(m)
}

// envReport lists the environment variables the platform cares about: directories, PATH, loopctl, and Anthropic/Claude
// variable names that must not be present.
func envReport() string {
	var forbidden []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "CLAUDE") {
			forbidden = append(forbidden, k)
		}
	}
	sort.Strings(forbidden)
	cwd, _ := os.Getwd()
	b, _ := json.Marshal(map[string]any{"HOME": os.Getenv("HOME"), "TMPDIR": os.Getenv("TMPDIR"), "PATH": os.Getenv("PATH"),
		"LOOPCTL_AGENT_URL": os.Getenv("LOOPCTL_AGENT_URL"), "hasToken": os.Getenv("LOOPCTL_AGENT_TOKEN") != "", "forbidden": forbidden, "cwd": cwd,
		"PI_CODING_AGENT_DIR": os.Getenv("PI_CODING_AGENT_DIR")})
	return string(b)
}

// settleStale reports background tasks of an earlier process as stopped, then gives the empty turn of its own that
// settles them; turnMu is held.
func settleStale(ids []string, sess *session, model string) {
	for _, id := range ids {
		emit(map[string]any{"type": "system", "subtype": "task_notification", "task_id": id, "tool_use_id": "toolu_" + id, "status": "stopped",
			"output_file": "", "summary": "Background command stopped", "session_id": sess.id})
	}
	emit(map[string]any{"type": "system", "subtype": "session_title_changed", "title": "Earlier work", "session_id": sess.id})
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sess.id, "model": model, "claude_code_version": version(), "tools": []string{}, "skills": []string{}, "mcp_servers": []any{}})
	result(map[string]any{"subtype": "success", "is_error": false, "result": "", "num_turns": 0, "terminal_reason": "completed"}, map[string]any{})
}

// backgroundList is the background_tasks_changed list of the running tasks; tasksMu is held.
func backgroundList() []any {
	list := []any{}
	for _, t := range tasks {
		list = append(list, map[string]any{"task_id": t.id, "task_type": "local_bash", "description": "Run it in the background", "tool_use_id": t.toolUse})
	}
	return list
}

// endTask ends a background task, completed (its time is up) or stopped (killed), as Claude Code reports it; when no
// turn is going on, Claude Code then replies by itself.
func endTask(id string, completed bool, sess *session, model string) {
	tasksMu.Lock()
	t := tasks[id]
	delete(tasks, id)
	list := backgroundList()
	tasksMu.Unlock()
	if t == nil {
		return
	}
	t.timer.Stop()
	patch, status := "completed", "completed"
	if !completed {
		patch, status = "killed", "stopped"
	}
	_ = os.WriteFile(t.outFile, []byte(strings.Repeat("noise\n", 1000)+"finished\n"), 0o600)
	emit(map[string]any{"type": "system", "subtype": "task_updated", "task_id": t.id, "patch": map[string]any{"status": patch, "end_time": time.Now().UnixMilli()}, "session_id": sess.id})
	emit(map[string]any{"type": "system", "subtype": "task_notification", "task_id": t.id, "tool_use_id": t.toolUse, "status": status, "output_file": t.outFile,
		"summary": "Background command \"Run it in the background\" " + status, "session_id": sess.id})
	emit(map[string]any{"type": "system", "subtype": "background_tasks_changed", "tasks": list, "session_id": sess.id})
	if !turnMu.TryLock() {
		return // the turn in progress takes the notification
	}
	defer turnMu.Unlock()
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sess.id, "model": model, "claude_code_version": version(), "tools": []string{}, "skills": []string{}, "mcp_servers": []any{}})
	reply(model, "background done")
	if os.Getenv("FAKECLAUDE_FOLLOWUP_NO_RESULT") == "1" {
		return // the reply's result is lost
	}
	result(map[string]any{"subtype": "success", "is_error": false, "result": "background done", "terminal_reason": "completed"},
		map[string]any{model: map[string]any{"inputTokens": 7, "outputTokens": 3, "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0, "thinkingTokens": 0}})
}
