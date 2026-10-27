// fakepi 是测试替身，模拟平台用到的 Pi 1.1.0 CLI/RPC 子集。
// 回复行为由 prompt 文本中的 '!' 指令控制，见 README.md。
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultVersion = "1.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

type options struct {
	thinking           string
	version            bool
	mode               string
	session            string
	fork               string
	sessionDir         string
	noSession          bool
	provider           string
	model              string
	skills             []string
	appendSystemPrompt []string
}

// parseArgs 解析参数；未知参数忽略（与 Pi 一样，未知的 --flag 会吞掉随后的非选项值）。
func parseArgs(args []string) options {
	var o options
	next := func(i *int) (string, bool) {
		if *i+1 < len(args) {
			*i++
			return args[*i], true
		}
		return "", false
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--version", "-v":
			o.version = true
		case "--mode":
			o.mode, _ = next(&i)
		case "--session":
			o.session, _ = next(&i)
		case "--fork":
			o.fork, _ = next(&i)
		case "--session-dir":
			o.sessionDir, _ = next(&i)
		case "--no-session":
			o.noSession = true
		case "--provider":
			o.provider, _ = next(&i)
		case "--model":
			o.model, _ = next(&i)
		case "--thinking":
			o.thinking, _ = next(&i)
		case "--skill":
			if v, ok := next(&i); ok {
				o.skills = append(o.skills, v)
			}
		case "--append-system-prompt":
			if v, ok := next(&i); ok {
				o.appendSystemPrompt = append(o.appendSystemPrompt, v)
			}
		case "--no-approve", "--offline", "--no-skills":
			// 接受但不处理
		default:
			if strings.HasPrefix(a, "--") && !strings.Contains(a, "=") &&
				i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		}
	}
	return o
}

func run(args []string) int {
	o := parseArgs(args)
	if o.version {
		v := os.Getenv("FAKEPI_VERSION")
		if v == "" {
			v = defaultVersion
		}
		fmt.Println(v)
		return 0
	}
	if o.mode != "rpc" {
		fmt.Fprintln(os.Stderr, "fakepi: 只支持 --mode rpc")
		return 2
	}
	cwd, _ := os.Getwd()
	if p := os.Getenv("FAKEPI_DUMP"); p != "" {
		if err := dump(p, args, cwd); err != nil {
			fmt.Fprintf(os.Stderr, "fakepi: 写入 FAKEPI_DUMP 失败：%v\n", err)
			return 1
		}
	}
	sess, err := openSession(o, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if o.thinking == "" {
		o.thinking = "off"
	}
	s := &server{opts: o, sess: sess, uiResp: make(chan map[string]any, 4)}
	br := bufio.NewReader(os.Stdin)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.handle(line)
		}
		if err != nil {
			return 0 // stdin 结束即退出
		}
	}
}

func dump(path string, args []string, cwd string) error {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if args == nil {
		args = []string{}
	}
	var systemPrompt strings.Builder
	for _, path := range parseArgs(args).appendSystemPrompt {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		systemPrompt.Write(content)
	}
	b, err := json.MarshalIndent(map[string]any{"args": args, "env": env, "cwd": cwd, "systemPrompt": systemPrompt.String()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ---- 会话文件 ----

type session struct {
	file     string // 空表示不持久化
	id       string
	cwd      string
	flushed  bool // 文件是否已在磁盘上
	lastID   string
	messages int
}

func openSession(o options, cwd string) (*session, error) {
	s := &session{cwd: cwd}
	switch {
	case o.noSession:
		s.id = newUUIDv7()
	case o.session != "":
		p, err := filepath.Abs(o.session)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("会话文件不存在：%s", p)
		}
		s.file, s.flushed = p, true
		if err := s.load(); err != nil {
			return nil, err
		}
	default:
		dir := o.sessionDir
		if dir == "" {
			dir = defaultSessionDir(cwd)
		}
		dir, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		s.id = newUUIDv7()
		now := time.Now().UTC()
		ts := now.Format("2006-01-02T15-04-05") + fmt.Sprintf("-%03dZ", now.Nanosecond()/1e6)
		s.file = filepath.Join(dir, ts+"_"+s.id+".jsonl")
	}
	if o.fork != "" && !o.noSession && o.session == "" {
		data, err := os.ReadFile(o.fork)
		if err != nil {
			return nil, err
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
		if len(lines) == 0 {
			return nil, fmt.Errorf("分叉源为空")
		}
		var head map[string]any
		if json.Unmarshal(lines[0], &head) != nil || head["type"] != "session" {
			return nil, fmt.Errorf("分叉源无效")
		}
		head["id"] = s.id
		head["cwd"] = cwd
		head["parentSession"] = o.fork
		lines[0], err = json.Marshal(head)
		if err != nil {
			return nil, err
		}
		copy := append(bytes.Join(lines, []byte{'\n'}), '\n')
		if err = os.WriteFile(s.file, copy, 0o600); err != nil {
			return nil, err
		}
		s.flushed = true
		if err = s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// load 读取已有会话文件的头部 id、最后一条 entry id 与消息数。
func (s *session) load() error {
	b, err := os.ReadFile(s.file)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		var e struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.Type == "session" {
			s.id = e.ID
			continue
		}
		s.lastID = e.ID
		if e.Type == "message" {
			s.messages++
		}
	}
	if s.id == "" {
		base := strings.TrimSuffix(filepath.Base(s.file), ".jsonl")
		if _, after, ok := strings.Cut(base, "_"); ok {
			base = after
		}
		s.id = base
	}
	return nil
}

func defaultSessionDir(cwd string) string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" {
		home, _ := os.UserHomeDir()
		agentDir = filepath.Join(home, ".pi", "agent")
	}
	safe := strings.TrimPrefix(cwd, "/")
	safe = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(safe)
	return filepath.Join(agentDir, "sessions", "--"+safe+"--")
}

// append 追加一条消息 entry；首次写入时创建文件并写入会话头。
func (s *session) append(msg map[string]any) error {
	s.messages++
	if s.file == "" {
		return nil
	}
	if !s.flushed {
		f, err := os.OpenFile(s.file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		hdr, _ := json.Marshal(map[string]any{
			"type": "session", "version": 3, "id": s.id, "timestamp": isoNow(), "cwd": s.cwd,
		})
		_, err = f.Write(append(hdr, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		s.flushed = true
	}
	id := shortID()
	var parent any
	if s.lastID != "" {
		parent = s.lastID
	}
	line, _ := json.Marshal(map[string]any{
		"type": "message", "id": id, "parentId": parent, "timestamp": isoNow(), "message": msg,
	})
	f, err := os.OpenFile(s.file, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	s.lastID = id
	return err
}

// ---- RPC ----

type server struct {
	opts  options
	outMu sync.Mutex

	mu   sync.Mutex
	sess *session
	cur  *turnRun

	// 本进程报告过的全部用量之和，供 get_session_stats。
	statsMu sync.Mutex
	stats   sessionStats

	// uiResp 收到的 extension_ui_response（!ask 等待它）。
	uiResp chan map[string]any
}

type sessionStats struct {
	input, output, cacheRead, cacheWrite int64
	userMessages, assistantMessages      int
	toolCalls, toolResults               int
}

// addUsage 把一份用量计入会话统计。
func (s *server) addUsage(u usageSpec) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats.input += u.input
	s.stats.output += u.output
	s.stats.cacheRead += u.cacheRead
	s.stats.cacheWrite += u.cacheWrite
}

func (s *server) count(f func(*sessionStats)) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	f(&s.stats)
}

// sessionStatsData 按 Pi 1.1.0 的 SessionStats 结构返回统计（dist/core/agent-session.d.ts）。
func (s *server) sessionStatsData() map[string]any {
	s.statsMu.Lock()
	st := s.stats
	s.statsMu.Unlock()
	s.mu.Lock()
	file, id := s.sess.file, s.sess.id
	s.mu.Unlock()
	out := map[string]any{
		"sessionId": id, "userMessages": st.userMessages, "assistantMessages": st.assistantMessages,
		"toolCalls": st.toolCalls, "toolResults": st.toolResults,
		"totalMessages": st.userMessages + st.assistantMessages + st.toolResults,
		"tokens": map[string]any{"input": st.input, "output": st.output, "cacheRead": st.cacheRead, "cacheWrite": st.cacheWrite,
			"total": st.input + st.output + st.cacheRead + st.cacheWrite},
		"cost": 0,
	}
	if file != "" {
		out["sessionFile"] = file
	}
	return out
}

type turnRun struct {
	abortCh     chan struct{}
	abortOnce   sync.Once
	done        chan struct{}
	ignoreAbort bool
	dir         directives
}

func (r *turnRun) aborted() bool {
	select {
	case <-r.abortCh:
		return true
	default:
		return false
	}
}

// sleep 可被 abort 打断；返回 true 表示被打断。
func (r *turnRun) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return false
	case <-r.abortCh:
		return true
	}
}

type response struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    any             `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func (s *server) emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakepi: 编码输出失败：%v\n", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func (s *server) ok(id json.RawMessage, cmd string, data any) {
	s.emit(response{ID: id, Type: "response", Command: cmd, Success: true, Data: data})
}

func (s *server) fail(id json.RawMessage, cmd, msg string) {
	s.emit(response{ID: id, Type: "response", Command: cmd, Success: false, Error: msg})
}

func (s *server) handle(line []byte) {
	var cmd struct {
		ID      json.RawMessage   `json:"id"`
		Type    string            `json:"type"`
		Message string            `json:"message"`
		Images  []json.RawMessage `json:"images"`
		Level   string            `json:"level"`
	}
	if err := json.Unmarshal(line, &cmd); err != nil {
		s.fail(nil, "parse", "Failed to parse command: "+err.Error())
		return
	}
	switch cmd.Type {
	case "set_thinking_level":
		if barrier := os.Getenv("FAKEPI_THINKING_BARRIER"); barrier != "" && cmd.Level == "high" {
			_ = os.WriteFile(barrier+".ready", []byte("ready"), 0600)
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, e := os.Stat(barrier + ".release"); e == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		s.mu.Lock()
		s.opts.thinking = cmd.Level
		s.mu.Unlock()
		s.ok(cmd.ID, "set_thinking_level", nil)
	case "get_state":
		s.ok(cmd.ID, "get_state", s.state())
	case "prompt":
		s.prompt(cmd.ID, cmd.Message, cmd.Images)
	case "abort":
		s.abort(cmd.ID)
	case "clear_queue":
		s.ok(cmd.ID, "clear_queue", map[string]any{"steering": []string{}, "followUp": []string{}})
	case "get_session_stats":
		s.ok(cmd.ID, "get_session_stats", s.sessionStatsData())
	case "extension_ui_response":
		var resp map[string]any
		_ = json.Unmarshal(line, &resp)
		select {
		case s.uiResp <- resp:
		default:
		}
	default:
		s.fail(cmd.ID, cmd.Type, "Unknown command: "+cmd.Type)
	}
}

func (s *server) state() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := map[string]any{
		"thinkingLevel":         s.opts.thinking,
		"isStreaming":           s.cur != nil,
		"isCompacting":          false,
		"steeringMode":          "one-at-a-time",
		"followUpMode":          "one-at-a-time",
		"sessionId":             s.sess.id,
		"autoCompactionEnabled": true,
		"messageCount":          s.sess.messages,
		"pendingMessageCount":   0,
	}
	if s.sess.file != "" {
		st["sessionFile"] = s.sess.file
	}
	if s.opts.model != "" {
		st["model"] = map[string]any{"id": s.opts.model, "provider": s.provider(), "api": "fakepi"}
	}
	return st
}

func (s *server) provider() string {
	if s.opts.provider != "" {
		return s.opts.provider
	}
	return "fakepi"
}

func (s *server) modelID() string {
	if s.opts.model != "" {
		return s.opts.model
	}
	return "fakepi-model"
}

func (s *server) abort(id json.RawMessage) {
	s.mu.Lock()
	r := s.cur
	s.mu.Unlock()
	if r == nil {
		s.ok(id, "abort", nil)
		return
	}
	if r.ignoreAbort {
		return // !noabort：永不响应
	}
	r.abortOnce.Do(func() { close(r.abortCh) })
	go func() {
		<-r.done // agent_settled 已发出，Pi 回到空闲
		s.ok(id, "abort", nil)
	}()
}

func (s *server) prompt(id json.RawMessage, msg string, images []json.RawMessage) {
	d := parseDirectives(msg)
	// Explicit offline fixture control: hold ordinary reminder prompts as well as
	// directive-bearing prompts. Production Pi never reads this variable.
	if d.sleep == 0 {
		if delay, err := time.ParseDuration(os.Getenv("FAKEPI_PROMPT_DELAY")); err == nil && delay > 0 {
			d.sleep = delay
		}
	}
	s.mu.Lock()
	if s.cur != nil {
		s.mu.Unlock()
		s.fail(id, "prompt", "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
		return
	}
	if d.rejectPrompt {
		s.mu.Unlock()
		s.fail(id, "prompt", "rejected")
		return
	}
	if d.handled {
		s.mu.Unlock()
		s.ok(id, "prompt", map[string]any{"disposition": "handled"})
		return
	}
	r := &turnRun{abortCh: make(chan struct{}), done: make(chan struct{}), ignoreAbort: d.noAbort, dir: d}
	s.cur = r
	s.mu.Unlock()
	// 与 Pi 一致：响应先于本轮任何事件发出。
	s.ok(id, "prompt", map[string]any{"disposition": "started"})
	go s.runTurn(r, msg, d, images)
}

func (s *server) persist(d directives, msg map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.noSession {
		s.sess.messages++
		return
	}
	if err := s.sess.append(msg); err != nil {
		fmt.Fprintf(os.Stderr, "fakepi: 写入会话文件失败：%v\n", err)
	}
}

func (s *server) runTurn(r *turnRun, msg string, d directives, images []json.RawMessage) {
	aborted := false
	var produced []any

	s.emit(map[string]any{"type": "agent_start"})
	s.emit(map[string]any{"type": "turn_start"})
	content := textContent(msg)
	for _, image := range images {
		var v map[string]any
		if json.Unmarshal(image, &v) == nil {
			content = append(content, v)
		}
	}
	user := map[string]any{"role": "user", "content": content, "timestamp": nowMs()}
	s.emit(map[string]any{"type": "message_start", "message": user})
	s.emit(map[string]any{"type": "message_end", "message": user})
	s.persist(d, user)
	s.count(func(st *sessionStats) { st.userMessages++ })

	// endAssistant 结束一条助手消息并记录。
	endAssistant := func(m map[string]any, toolResults []any) {
		s.countAssistant(m)
		s.emit(map[string]any{"type": "message_end", "message": m})
		s.persist(d, m)
		produced = append(produced, m)
		s.emit(map[string]any{"type": "turn_end", "message": m, "toolResults": nonNil(toolResults)})
	}

	if d.retry {
		s.emit(map[string]any{"type": "message_start", "message": s.assistant(nil, "pending", "")})
		sent, ab := s.streamText(r, "retry-attempt-1 failed output", d, false)
		if ab {
			aborted = true
			endAssistant(s.assistant(textContent(sent), "aborted", "Request was aborted"), nil)
		} else {
			const retryErr = "fakepi: 模拟可重试错误（529 overloaded）"
			endAssistant(s.assistant(textContent(sent), "error", retryErr), nil)
			s.emit(map[string]any{"type": "agent_end", "messages": produced, "willRetry": true})
			s.emit(map[string]any{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 0, "errorMessage": retryErr})
			produced = nil
			s.emit(map[string]any{"type": "agent_start"})
			s.emit(map[string]any{"type": "turn_start"})
		}
	}

	if len(d.tools) > 0 && !aborted {
		var calls []any
		s.emit(map[string]any{"type": "message_start", "message": s.assistant(nil, "pending", "")})
		for i, tl := range d.tools {
			call := map[string]any{"type": "toolCall", "id": fmt.Sprintf("call_%d", i+1), "name": tl.name, "arguments": map[string]any{}}
			calls = append(calls, call)
			s.emit(s.update(map[string]any{"type": "toolcall_start", "contentIndex": i, "id": call["id"], "toolName": tl.name}))
			s.emit(s.update(map[string]any{"type": "toolcall_end", "contentIndex": i, "toolCall": call}))
		}
		m := s.assistant(calls, "toolUse", "")
		s.countAssistant(m)
		s.count(func(st *sessionStats) { st.toolCalls += len(calls) })
		s.emit(map[string]any{"type": "message_end", "message": m})
		s.persist(d, m)
		produced = append(produced, m)
		var results []any
		for i, tl := range d.tools {
			callID := fmt.Sprintf("call_%d", i+1)
			out := "ok"
			if tl.fail {
				out = "failed"
			}
			result := map[string]any{"content": textContent(out), "details": map[string]any{}}
			s.emit(map[string]any{"type": "tool_execution_start", "toolCallId": callID, "toolName": tl.name, "args": map[string]any{}})
			s.emit(map[string]any{"type": "tool_execution_end", "toolCallId": callID, "toolName": tl.name, "result": result, "isError": tl.fail, "durationMs": 1})
			tr := map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": tl.name, "content": textContent(out), "details": map[string]any{}, "isError": tl.fail, "timestamp": nowMs(), "durationMs": 1}
			if d.toolUsage != nil {
				tr["usage"] = d.toolUsage.wire()
				s.addUsage(*d.toolUsage)
			}
			s.count(func(st *sessionStats) { st.toolResults++ })
			s.emit(map[string]any{"type": "message_start", "message": tr})
			s.emit(map[string]any{"type": "message_end", "message": tr})
			s.persist(d, tr)
			produced = append(produced, tr)
			results = append(results, tr)
		}
		s.emit(map[string]any{"type": "turn_end", "message": m, "toolResults": results})
		s.emit(map[string]any{"type": "turn_start"})
	}

	if !aborted && d.ask != "" {
		// 扩展对话：发 extension_ui_request，等回应后把它写进回复文本；abort 时放弃等待。
		req := map[string]any{"type": "extension_ui_request", "id": "ask-" + shortID(), "method": d.ask, "title": "fakepi 问"}
		if d.ask == "select" {
			req["options"] = []string{"staging", "production"}
		}
		s.emit(req)
		select {
		case resp := <-s.uiResp:
			answer := "answer cancelled"
			switch {
			case resp["cancelled"] == true:
			case resp["confirmed"] != nil:
				answer = fmt.Sprintf("answer confirmed=%v", resp["confirmed"])
			case resp["value"] != nil:
				answer = fmt.Sprintf("answer value=%v", resp["value"])
			}
			d.text, d.hasText = answer, true
		case <-r.abortCh:
			aborted = true
		}
	}

	if !aborted && d.compact != nil {
		s.emit(map[string]any{"type": "compaction_start", "reason": "threshold"})
		s.addUsage(*d.compact)
		s.emit(map[string]any{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false,
			"result": map[string]any{"summary": "fakepi 摘要", "firstKeptEntryId": shortID(), "tokensBefore": 1000, "estimatedTokensAfter": 200,
				"usage": d.compact.wire(), "details": map[string]any{}}})
	}
	if !aborted && d.warm != nil {
		s.emitWarm(*d.warm)
	}

	if !aborted {
		s.emit(map[string]any{"type": "message_start", "message": s.assistant(nil, "pending", "")})
		reply := d.reply(msg)
		if d.errorMsg != "" && !d.hasText {
			reply = "" // 出错且未指定文本时不输出内容
		}
		sent, ab := s.streamText(r, reply, d, d.crash)
		switch {
		case ab:
			aborted = true
			endAssistant(s.assistant(textContent(sent), "aborted", "Request was aborted"), nil)
		case d.errorMsg != "":
			endAssistant(s.assistant(textContent(sent), "error", d.errorMsg), nil)
		default:
			endAssistant(s.assistant(textContent(sent), "stop", ""), nil)
			if d.retry {
				s.emit(map[string]any{"type": "auto_retry_end", "success": true, "attempt": 1})
			}
		}
	}

	s.emit(map[string]any{"type": "agent_end", "messages": nonNil(produced), "willRetry": false})
	settledAborted := aborted || (!r.ignoreAbort && r.aborted())
	// 与 Pi 一致：先回到空闲再发 agent_settled，客户端收到后即可发送下一条 prompt。
	s.mu.Lock()
	s.cur = nil
	s.mu.Unlock()
	s.emit(map[string]any{"type": "agent_settled", "aborted": settledAborted})
	close(r.done)
	if d.warmAfter != nil {
		time.Sleep(50 * time.Millisecond)
		s.emitWarm(*d.warmAfter)
	}
}

// emitWarm 输出一条缓存预热的用量条目（Pi 1.1.0 以 entry_appended 推送 type 为 usage 的会话条目）。
func (s *server) emitWarm(u usageSpec) {
	s.addUsage(u)
	s.emit(map[string]any{"type": "entry_appended", "entry": map[string]any{
		"type": "usage", "id": shortID(), "parentId": nil, "timestamp": isoNow(), "kind": "cache_warm",
		"provider": s.provider(), "model": s.modelID(), "usage": u.wire()}})
}

// countAssistant 计入一条已结束的助手消息及其用量。
func (s *server) countAssistant(m map[string]any) {
	s.count(func(st *sessionStats) { st.assistantMessages++ })
	if u, ok := m["usageSpec"].(usageSpec); ok {
		s.addUsage(u)
	}
	delete(m, "usageSpec")
}

// streamText 分 3 段流式输出文本；返回已输出的文本与是否被 abort。
func (s *server) streamText(r *turnRun, text string, d directives, crash bool) (string, bool) {
	chunks := split3(text)
	if crash && len(chunks) == 0 {
		fmt.Fprintln(os.Stderr, "fakepi: 模拟崩溃")
		os.Exit(3)
	}
	var sent strings.Builder
	if len(chunks) > 0 {
		s.emit(s.update(map[string]any{"type": "text_start", "contentIndex": 0}))
	}
	for i, c := range chunks {
		if i > 0 && d.sleep > 0 && r.sleep(d.sleep) {
			return sent.String(), true
		}
		if r.aborted() {
			return sent.String(), true
		}
		s.emit(s.update(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": c}))
		sent.WriteString(c)
		if crash && i == 0 {
			fmt.Fprintln(os.Stderr, "fakepi: 模拟崩溃")
			os.Exit(3)
		}
	}
	if len(chunks) > 0 {
		s.emit(s.update(map[string]any{"type": "text_end", "contentIndex": 0, "content": text}))
	}
	return sent.String(), false
}

func (s *server) update(ev map[string]any) map[string]any {
	return map[string]any{"type": "message_update", "usage": zeroUsage(), "assistantMessageEvent": ev}
}

func (s *server) assistant(content []any, stop, errMsg string) map[string]any {
	m := map[string]any{
		"role":       "assistant",
		"content":    nonNil(content),
		"api":        "fakepi",
		"provider":   s.provider(),
		"model":      s.modelID(),
		"usage":      zeroUsage(),
		"stopReason": stop,
		"timestamp":  nowMs(),
	}
	if errMsg != "" {
		m["errorMessage"] = errMsg
	}
	if stop != "pending" {
		m["durationMs"] = 1
		s.mu.Lock()
		r := s.cur
		s.mu.Unlock()
		if r != nil && r.dir.usage != nil {
			m["usage"] = r.dir.usage.wire()
			m["usageSpec"] = *r.dir.usage // 只在进程内使用，输出前删除
		}
		if r != nil && r.dir.respModel != "" {
			m["responseModel"] = r.dir.respModel
		}
	}
	return m
}

// usageSpec 是指令中给出的一份用量。
type usageSpec struct {
	input, output, cacheRead, cacheWrite int64
	reasoning                            *int64
}

// wire 按 Pi 1.1.0 的 Usage 结构输出（费用恒为 0）。
func (u usageSpec) wire() map[string]any {
	m := map[string]any{
		"input": u.input, "output": u.output, "cacheRead": u.cacheRead, "cacheWrite": u.cacheWrite,
		"totalTokens": u.input + u.output + u.cacheRead + u.cacheWrite,
		"cost":        map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
	if u.reasoning != nil {
		m["reasoning"] = *u.reasoning
	}
	return m
}

// parseUsage 解析 "输入,输出[,缓存读[,缓存写[,推理]]]"。
func parseUsage(v string) *usageSpec {
	var n [5]int64
	parts := strings.Split(v, ",")
	for i := 0; i < len(parts) && i < 5; i++ {
		_, err := fmt.Sscanf(parts[i], "%d", &n[i])
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakepi: 无效的用量：%s\n", v)
			return nil
		}
	}
	u := &usageSpec{input: n[0], output: n[1], cacheRead: n[2], cacheWrite: n[3]}
	if len(parts) >= 5 {
		u.reasoning = &n[4]
	}
	return u
}

// ---- 指令 ----

type toolSpec struct {
	name string
	fail bool
}

type directives struct {
	text         string
	hasText      bool
	tools        []toolSpec
	sleep        time.Duration
	errorMsg     string
	retry        bool
	crash        bool
	noAbort      bool
	rejectPrompt bool
	handled      bool
	noSession    bool
	usage        *usageSpec // 每条已结束的助手消息带的用量
	respModel    string
	toolUsage    *usageSpec
	compact      *usageSpec
	warm         *usageSpec
	warmAfter    *usageSpec
	ask          string   // !ask：回复前发一个扩展对话（confirm、select、input）并等回应
	found        bool     // 是否出现过任何指令
	rest         []string // 非指令词
}

func parseDirectives(msg string) directives {
	var d directives
	for _, tok := range strings.Fields(msg) {
		key, val, hasVal := strings.Cut(tok, "=")
		known := true
		switch {
		case key == "!text" && hasVal:
			d.text, d.hasText = underscores(val), true
		case key == "!tool" && hasVal:
			d.tools = append(d.tools, toolSpec{name: val})
		case key == "!toolfail" && hasVal:
			d.tools = append(d.tools, toolSpec{name: val, fail: true})
		case key == "!sleep" && hasVal:
			dur, err := time.ParseDuration(val)
			if err != nil {
				fmt.Fprintf(os.Stderr, "fakepi: 无效的 !sleep：%s\n", val)
			}
			d.sleep = dur
		case key == "!error" && hasVal:
			d.errorMsg = underscores(val)
		case tok == "!retry":
			d.retry = true
		case tok == "!crash":
			d.crash = true
		case tok == "!noabort":
			d.noAbort = true
		case tok == "!rejectprompt":
			d.rejectPrompt = true
		case tok == "!handled":
			d.handled = true
		case tok == "!nosession":
			d.noSession = true
		case key == "!ask" && hasVal:
			d.ask = val
		case key == "!usage" && hasVal:
			d.usage = parseUsage(val)
		case key == "!respmodel" && hasVal:
			d.respModel = val
		case key == "!toolusage" && hasVal:
			d.toolUsage = parseUsage(val)
		case key == "!compact" && hasVal:
			d.compact = parseUsage(val)
		case key == "!warm" && hasVal:
			d.warm = parseUsage(val)
		case key == "!warmafter" && hasVal:
			d.warmAfter = parseUsage(val)
		default:
			known = false
		}
		if known {
			d.found = true
		} else {
			d.rest = append(d.rest, tok)
		}
	}
	return d
}

// reply 返回本轮回复文本：!text 优先；否则回显消息（有指令时去掉指令词）。
func (d directives) reply(msg string) string {
	if d.hasText {
		return d.text
	}
	if !d.found {
		return "echo: " + msg
	}
	return "echo: " + strings.Join(d.rest, " ")
}

func underscores(s string) string { return strings.ReplaceAll(s, "_", " ") }

// split3 按字符把文本尽量均分为至多 3 段。
func split3(s string) []string {
	rs := []rune(s)
	if len(rs) == 0 {
		return nil
	}
	size := (len(rs) + 2) / 3
	var out []string
	for i := 0; i < len(rs); i += size {
		out = append(out, string(rs[i:min(i+size, len(rs))]))
	}
	return out
}

// ---- 工具函数 ----

func textContent(s string) []any {
	if s == "" {
		return []any{}
	}
	return []any{map[string]any{"type": "text", "text": s}}
}

func nonNil(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

func zeroUsage() map[string]any {
	return map[string]any{
		"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }

func isoNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func newUUIDv7() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(time.Now().UnixMilli()))
	copy(b[0:6], ms[2:8])
	b[6] = 0x70 | (b[6] & 0x0f)
	b[8] = 0x80 | (b[8] & 0x3f)
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func shortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
