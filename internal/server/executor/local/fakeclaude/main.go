// Command fakeclaude 是测试用的假 Claude Code：说与 claude -p --input-format/--output-format stream-json 相同的协议，
// 不联网、不读任何凭证。它还回答 --version、auth status --json（带上 email 等字段，用来核对平台丢弃它们）与 purge。
//
// 每条 user 消息的文字决定回复：
//
//	fake:history     回复 history=<这个会话里此前的 user 消息数>（接续或分叉的会话也算）
//	fake:env         回复平台关心的环境变量（JSON）
//	fake:args        回复启动参数（JSON）
//	fake:images      回复 images=<图片数>
//	fake:tool <命令> 调用一次 Bash 工具再回复 ran
//	fake:tool2 <命令> 同上，但工具调用只出现在完整的 assistant 消息里，结果为错误，回复 ran2
//	fake:bigline     输出很长的一行后不再输出
//	fake:subagent    先输出一段子 Agent 的文字（parent_tool_use_id 非空），再回复 main
//	fake:error <msg> 这一轮以错误结束
//	fake:notloggedin 像未登录时那样以 api_error 结束
//	fake:slow        每 50ms 输出一个点，直到被打断（控制消息或 SIGINT）
//	fake:noack       同上，但不回应打断控制消息，只能靠 SIGINT
//	fake:exit        输出一点文字后以退出码 3 退出
//	fake:ratelimit   先输出 rate_limit_event 再回复
//	fake:usage2      result 里有两个模型的用量
//	其他             回复 echo: <文字>
//
// 环境变量：FAKECLAUDE_VERSION 改版本号；FAKECLAUDE_EXIT_AT_START=1 启动即报不认识的选项并退出；FAKECLAUDE_NO_INIT=1 收到消息也不输出；
// FAKECLAUDE_LOGGED_IN=0 或 HOME 下有 .fakeclaude-logged-out 让 auth status 报未登录；FAKECLAUDE_AUTH_FAIL=1 让 auth status 失败；
// FAKECLAUDE_ARGS_FILE 每次启动追加一行参数（JSON）；purge 调用记在 FAKECLAUDE_LOG 与 HOME 下的 .fakeclaude-purge.log（每次一行：purge <路径> -y）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	outMu sync.Mutex
	out   = bufio.NewWriter(os.Stdout)
)

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
	run(args)
}

func authStatus() {
	if os.Getenv("FAKECLAUDE_AUTH_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "auth status failed for private@example.com")
		os.Exit(2)
	}
	home, _ := os.UserHomeDir()
	// auth status 由平台以最小的环境运行，测试改用 HOME 下的标记文件控制。
	_, loggedOut := os.Stat(filepath.Join(home, ".fakeclaude-logged-out"))
	loggedIn := os.Getenv("FAKECLAUDE_LOGGED_IN") != "0" && loggedOut != nil
	method := "claude.ai"
	if !loggedIn {
		method = "none"
	}
	b, _ := json.Marshal(map[string]any{"loggedIn": loggedIn, "authMethod": method, "apiProvider": "firstParty", "subscriptionType": "max",
		"planTier": "max_20x", "loginExpiresAt": "2099-01-01T00:00:00Z", "email": "private@example.com", "orgId": "org-private-id", "orgName": "Private Org",
		"configDirectory": filepath.Join(home, ".claude"), "projectsDirectory": filepath.Join(home, ".claude", "projects"), "analyticsDisabled": false, "storedClaudeAiLogin": true})
	fmt.Println(string(b))
}

func purge(args []string) {
	home, _ := os.UserHomeDir()
	// purge 也由平台以最小的环境运行：除 FAKECLAUDE_LOG 外，也记在 HOME 下。
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

// flags 是假 claude 关心的启动参数。
type flags struct {
	model, sessionID, resume string
	fork                     bool
	tools                    []string
	mcp                      []string
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
		case "--fork-session":
			f.fork = true
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
	return f
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

func projectsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// transcript 是会话记录：与真的 claude 一样放在 ~/.claude/projects/<cwd 中非字母数字换成 -> 下。
func transcript(id string) string {
	cwd, _ := os.Getwd()
	return filepath.Join(projectsDir(), nonAlnum.ReplaceAllString(cwd, "-"), id+".jsonl")
}

// history 读出要接续的会话里已有的 user 消息数：resume 是 .jsonl 路径时读它，否则按会话 ID 在 projects 下找。
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

// inbound 是 stdin 上的一行。
type inbound struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype string `json:"subtype"`
	} `json:"request"`
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
			case "user":
				if os.Getenv("FAKECLAUDE_NO_INIT") == "1" {
					continue
				}
				text, images := content(in.Message.Content)
				prior := sess.count
				sess.record(text)
				turn(f, sess, model, text, images, prior, lines, sigint)
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
		"tools": f.tools, "mcp_servers": servers, "permissionMode": "bypassPermissions", "apiKeySource": "none"})
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
		// 工具调用只出现在完整的 assistant 消息里（没有 content_block_start）。
		id := "toolu_fake_2"
		emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"id": "msg_tool2", "model": model, "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": arg}}}}})
		emit(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok", "is_error": true}}}})
		reply(model, "ran2")
	case "fake:bigline":
		// 一行很长的输出，之后不再输出（读方若停读，这里会卡住）。
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

// reply 以流式文字加完整的 assistant 消息给出一段回复。
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

// envReport 是平台关心的环境变量：目录、PATH、loopctl，以及不该出现的 Anthropic／Claude 变量名。
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
