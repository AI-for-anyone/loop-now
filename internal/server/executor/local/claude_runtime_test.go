package local

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/fakeclaudebin"
)

// claudeHarness 是用 fakeclaude 跑 Claude Code 运行时的执行器：真实 HOME 换成临时目录（里面先放好用户自己的文件），
// fakeclaude 的会话记录写在它的 .claude/projects 下，启动参数记在 argsFile。
type claudeHarness struct {
	*harness
	realHome string
	before   map[string]string
	argsFile string
	logs     *bytes.Buffer
}

func newClaudeHarness(t *testing.T, mutate func(o *Options)) *claudeHarness {
	t.Helper()
	realHome, before := fakeRealHome(t)
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	h := newHarness(t, func(o *Options) {
		o.ClaudeBin = fakeclaudebin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
		o.Passthrough = append(o.Passthrough, "FAKECLAUDE_ARGS_FILE", "FAKECLAUDE_EXIT_AT_START", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDECODE")
		if mutate != nil {
			mutate(o)
		}
	}, "")
	h.env["FAKECLAUDE_ARGS_FILE"] = argsFile
	logs := &bytes.Buffer{}
	h.e.log = slog.New(slog.NewTextHandler(logs, nil))
	return &claudeHarness{harness: h, realHome: realHome, before: before, argsFile: argsFile, logs: logs}
}

func (h *claudeHarness) claudeAgent(fn func(s *agentconfig.Snapshot)) uuid.UUID {
	return h.agent(func(s *agentconfig.Snapshot) {
		s.Provider, s.ModelID = "claude-code", "sonnet"
		level := "high"
		s.ThinkingLevel = &level
		if fn != nil {
			fn(s)
		}
	})
}

// launches 返回每次启动 fakeclaude 的参数。
func (h *claudeHarness) launches() [][]string {
	h.t.Helper()
	f, err := os.Open(h.argsFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var args []string
		if json.Unmarshal(sc.Bytes(), &args) == nil {
			out = append(out, args)
		}
	}
	return out
}

func (h *claudeHarness) claudePid(id uuid.UUID) int {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	if p := h.e.procs[id]; p != nil {
		return p.rt.Pid()
	}
	return 0
}

func hasFlag(args []string, flag string) bool { return slices.Contains(args, flag) }

// 一轮的参数、会话记录、用量、复用与重启后接续：
// 参数按平台的决定（不读用户的设置与 MCP、关自动记忆、bypassPermissions 且不卡在批准上、系统提示不冻结、平台的插件目录与工具白名单）；
// 会话记录是 claude 自己写在真实 HOME 的 .claude/projects 下的那个文件；配置变化重启后用 --resume 接续。
func TestClaudeRuntimeTurnSessionAndResume(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	res, sink := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "echo: hello" || sink.text.String() != "echo: hello" {
		t.Fatalf("第一轮：%+v %q", res, sink.text.String())
	}
	launches := h.launches()
	if len(launches) != 1 {
		t.Fatalf("应启动一次：%v", launches)
	}
	args, d := launches[0], h.e.dirs(id)
	want := map[string]string{"--model": "sonnet", "--effort": "high", "--setting-sources": "", "--settings": claudeSettings,
		"--permission-mode": "bypassPermissions", "--permission-prompts": "none", "--system-prompt-snapshot": "off",
		"--plugin-dir": d.claudePlugin, "--tools": strings.Join(ClaudeCodeTools, ","), "--input-format": "stream-json", "--output-format": "stream-json"}
	for flag, v := range want {
		if !hasFlag(args, flag) || argValue(args, flag) != v {
			t.Errorf("参数 %s = %q，期望 %q", flag, argValue(args, flag), v)
		}
	}
	for _, flag := range []string{"-p", "--verbose", "--include-partial-messages", "--strict-mcp-config"} {
		if !hasFlag(args, flag) {
			t.Errorf("缺少参数 %s", flag)
		}
	}
	if hasFlag(args, "--mcp-config") || hasFlag(args, "--resume") {
		t.Errorf("没有 MCP、没有旧会话：%v", args)
	}
	sid := argValue(args, "--session-id")
	if !claudecli.ValidSessionID(sid) {
		t.Fatalf("会话 ID：%q", sid)
	}
	transcript := filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(d.workspace), sid+".jsonl")
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if recorded != transcript {
		t.Fatalf("会话记录：%q，期望 %q", recorded, transcript)
	}
	h.obs.mu.Lock()
	var usage []executor.Usage
	for _, u := range h.obs.usage {
		usage = append(usage, u.u)
	}
	h.obs.mu.Unlock()
	if len(usage) != 1 || usage[0].Provider != "claude-code" || usage[0].Model != "claude-sonnet-fake" || usage[0].Input != 10 || usage[0].Kind != "assistant" {
		t.Fatalf("用量：%+v", usage)
	}
	h.obs.mu.Lock()
	startedRuntime := ""
	for _, ev := range h.obs.events {
		if ev.ev.Type == store.EventPiStarted {
			startedRuntime, _ = ev.ev.Data["runtime"].(string)
		}
	}
	h.obs.mu.Unlock()
	if startedRuntime != "claude-code" {
		t.Fatalf("进程启动事件应记下运行时：%q", startedRuntime)
	}
	pid := h.claudePid(id)
	if res, _ = h.exec(id, "fake:history"); res.Text != "history=1" || h.claudePid(id) != pid {
		t.Fatalf("复用同一进程：%+v", res)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	if res, _ = h.exec(id, "fake:history"); res.Text != "history=2" || h.claudePid(id) == pid {
		t.Fatalf("配置变化后重启并接续：%+v", res)
	}
	if args := h.launches()[1]; argValue(args, "--resume") != sid || hasFlag(args, "--session-id") {
		t.Fatalf("重启后应 --resume 原会话：%v", args)
	}
	if b, _ := os.ReadFile(d.prompt); !strings.Contains(string(b), "changed") {
		t.Fatalf("追加提示应是新的：%s", b)
	}
}

// 交给 claude 的环境：真实 HOME、Agent 的 TMPDIR、平台的 PATH；Anthropic／Claude 的变量不论来自透传、CLI 凭证还是插件都去掉，
// 也没有 Pi 的目录变量。
func TestClaudeEnvStripsAnthropicAndClaude(t *testing.T) {
	loopctlDir := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.LoopctlBinDir = loopctlDir })
	h.env["ANTHROPIC_API_KEY"] = "sk-from-passthrough"
	h.env["CLAUDE_CODE_USE_BEDROCK"] = "1"
	h.env["CLAUDECODE"] = "1"
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"mycli"}
		s.CLIPayloads["mycli"] = clitemplate.Payload{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://evil.example", "MYCLI_TOKEN": "kept"}}
		s.Plugins.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/evil", "ANTHROPIC_AUTH_TOKEN": "plugin-token", "PLUGIN_SETTING": "kept"}
	})
	res, _ := h.exec(id, "fake:env")
	var env struct {
		HOME, TMPDIR, PATH, PI_CODING_AGENT_DIR string
		Forbidden                               []string `json:"forbidden"`
	}
	if res.Outcome != executor.OutcomeCompleted || json.Unmarshal([]byte(res.Text), &env) != nil {
		t.Fatalf("环境：%+v", res)
	}
	d := h.e.dirs(id)
	if env.HOME != h.realHome || env.TMPDIR != d.tmp || env.PI_CODING_AGENT_DIR != "" || !strings.HasPrefix(env.PATH, loopctlDir+string(os.PathListSeparator)) {
		t.Fatalf("目录与 PATH：%+v", env)
	}
	if len(env.Forbidden) != 0 {
		t.Fatalf("不能把 Anthropic／Claude 的变量交给 claude：%v", env.Forbidden)
	}
	// claude 自己的短命令（auth status 等）与主进程同一套基础变量：USER／LOGNAME（claude 按它找登录）、透传的 LANG。
	h.env["LANG"] = "zh_CN.UTF-8"
	helper := strings.Join(h.e.ClaudeHelperEnv(context.Background(), h.realHome), "\n")
	for _, want := range []string{"USER=tester", "LOGNAME=tester", "LANG=zh_CN.UTF-8", "HOME=" + h.realHome} {
		if !strings.Contains(helper, want) {
			t.Errorf("辅助命令的环境缺少 %s：%s", want, helper)
		}
	}
	for _, kv := range strings.Split(helper, "\n") {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE") {
			t.Errorf("辅助命令的环境不带 Anthropic／Claude 的变量：%s", kv)
		}
	}
	all := claudeEnv([]string{"A=1", "ANTHROPIC_API_KEY=x", "CLAUDE_CONFIG_DIR=y", "CLAUDECODE=1", "CLAUDE_CODE_OAUTH_TOKEN=z", "PI_OFFLINE=1", "MYCLI_TOKEN=kept"})
	if strings.Join(all, ",") != "A=1,MYCLI_TOKEN=kept" {
		t.Fatalf("claudeEnv：%v", all)
	}
}

// 真实 HOME 里平台什么也不写：除了 claude 自己写的会话记录（.claude/projects 下），用户的文件原样，受管 CLI 的凭证文件在 Agent 的独立 HOME；
// 释放与启动清理只动 Agent 自己的目录。
func TestClaudeNeverTouchesRealHomeBesidesItsOwnTranscript(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"mycli"}
		s.CLIPayloads["mycli"] = clitemplate.Payload{Files: []clitemplate.PayloadFile{{Path: ".mycli/config.json", Content: []byte(`{"agent":true}`)}}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("执行：%+v", res)
	}
	d := h.e.dirs(id)
	if b, err := os.ReadFile(filepath.Join(d.home, ".mycli", "config.json")); err != nil || string(b) != `{"agent":true}` {
		t.Fatalf("凭证文件应在 Agent 的独立 HOME：%q %v", b, err)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.src.mu.Lock()
	h.src.deleted = append(h.src.deleted, id)
	h.src.mu.Unlock()
	if err := h.e.CleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, h.realHome)
	prefix := filepath.ToSlash(filepath.Join(".claude", "projects", claudecli.ProjectDirName(d.workspace))) + "/"
	claudeOwn := func(k string) bool { // claude 自己的会话记录、它为此建的上级目录，以及 fakeclaude 记下 purge 调用的文件
		return strings.HasPrefix(k, prefix) || k == ".claude/" || k == ".claude/projects/" || k == ".fakeclaude-purge.log"
	}
	for k := range after {
		if _, ok := h.before[k]; !ok && !claudeOwn(k) {
			t.Errorf("真实 HOME 里多出了 %s", k)
		}
	}
	for k, v := range h.before {
		if after[k] != v {
			t.Errorf("真实 HOME 里的 %s 被改动或删除", k)
		}
	}
}

// 平台给 Claude Code 的东西：插件目录里有 loopctl、Agent 的 Skill 与插件的 Skill；插件的 MCP 服务器经 --mcp-config；
// 追加提示里有 Agent 的 Prompt、自己文件夹的入口原文与项目入口（说明在 Claude Code 启动时读）；不写 Pi 的模型文件。
func TestClaudeSkillsMCPAndPrompt(t *testing.T) {
	contextDir := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.ContextDir = contextDir })
	pluginSkill := filepath.Join(t.TempDir(), "plugin-skill")
	if err := os.MkdirAll(pluginSkill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginSkill, "SKILL.md"), []byte("---\nname: plugin-skill\ndescription: d\n---\nplugin body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := h.src.blob("---\nname: review\ndescription: d\n---\nreview body\n")
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.Prompt = "You are the reviewer."
		s.Skills = []agentconfig.SkillSnapshot{{Name: "review", Files: []store.SkillFile{{Path: "SKILL.md", SHA256: skill}}}}
		s.Plugins = agentconfig.PluginAssets{Skills: []string{pluginSkill}, MCPServers: map[string]json.RawMessage{"browser": json.RawMessage(`{"command":"/bin/true"}`)}}
	})
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.workspace, "AGENTS.md"), []byte("OWN-ENTRY-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("执行：%+v", res)
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "skills/loopctl/SKILL.md", "skills/review/SKILL.md", "skills/plugin-skill/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(d.claudePlugin, filepath.FromSlash(rel))); err != nil {
			t.Errorf("插件目录缺少 %s", rel)
		}
	}
	args := h.launches()[0]
	if argValue(args, "--mcp-config") != d.claudeMCP {
		t.Fatalf("MCP：%v", args)
	}
	if b, _ := os.ReadFile(d.claudeMCP); !strings.Contains(string(b), `"browser"`) {
		t.Fatalf("MCP 文件：%s", b)
	}
	if _, err := os.Stat(filepath.Join(d.pi, "mcp.json")); !os.IsNotExist(err) {
		t.Fatal("不写 Pi 的 mcp.json")
	}
	prompt, _ := os.ReadFile(d.prompt)
	for _, want := range []string{"You are the reviewer.", "OWN-ENTRY-MARKER", `<context_entry path="` + filepath.Join(d.workspace, "AGENTS.md") + `">`, "its content is included below if it exists", "loaded only when Claude Code starts"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("追加提示缺少 %q：\n%s", want, prompt)
		}
	}
	if argValue(args, "--append-system-prompt-file") != d.prompt {
		t.Fatalf("追加提示文件：%v", args)
	}
	for _, f := range []string{"auth.json", "models.json"} {
		if _, err := os.Stat(filepath.Join(d.pi, f)); !os.IsNotExist(err) {
			t.Errorf("Claude Code 运行时不写 Pi 的 %s", f)
		}
	}
}

// 失败：找不到 claude、启动即退出（带版本号与错误输出）、未登录（由 claude auth status 判断，它输出里的邮箱与组织不进消息、事件与日志）、
// 已登录时的 API 错误仍是 runtime_error、一轮中进程崩溃。
func TestClaudeFailures(t *testing.T) {
	missing := newClaudeHarness(t, func(o *Options) { o.ClaudeBin = filepath.Join(t.TempDir(), "no-claude") })
	if res, _ := missing.exec(missing.claudeAgent(nil), "hi"); res.ErrorCode != executor.ErrRuntimeUnavailable {
		t.Fatalf("找不到 claude：%+v", res)
	}

	h := newClaudeHarness(t, nil)
	h.env["FAKECLAUDE_EXIT_AT_START"] = "1"
	id := h.claudeAgent(nil)
	res, _ := h.exec(id, "hi")
	if res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(res.ErrorMessage, "9.9.9") || !strings.Contains(res.ErrorMessage, "unknown option") {
		t.Fatalf("启动即退出：%+v", res)
	}
	delete(h.env, "FAKECLAUDE_EXIT_AT_START")

	if res, _ = h.exec(id, "fake:notloggedin"); res.ErrorCode != executor.ErrRuntimeError {
		t.Fatalf("auth status 报已登录时仍是 runtime_error：%+v", res)
	}
	if err := os.WriteFile(filepath.Join(h.realHome, ".fakeclaude-logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ = h.exec(id, "fake:notloggedin"); res.ErrorCode != executor.ErrRuntimeLoginRequired || !strings.Contains(res.ErrorMessage, "没有登录") {
		t.Fatalf("未登录：%+v", res)
	}
	res, _ = h.exec(id, "fake:exit")
	if res.ErrorCode != executor.ErrRuntimeExited || !strings.Contains(res.ErrorMessage, "Claude Code 9.9.9 进程异常退出") || !strings.Contains(res.ErrorMessage, "crashing on purpose") {
		t.Fatalf("崩溃：%+v", res)
	}
	if res, _ = h.exec(id, "after crash"); res.Text != "echo: after crash" {
		t.Fatalf("崩溃后下一条起新进程：%+v", res)
	}
	h.obs.mu.Lock()
	events, _ := json.Marshal(h.obs.events)
	h.obs.mu.Unlock()
	for _, secret := range []string{"private@example.com", "Private Org", "org-private-id"} {
		if strings.Contains(string(events), secret) || strings.Contains(h.logs.String(), secret) || strings.Contains(res.ErrorMessage, secret) {
			t.Fatalf("auth status 输出里的 %q 不能进事件、日志或错误消息", secret)
		}
	}
}

// 打断：控制消息得到确认时进程留着，下一条复用它；等不到确认改发 SIGINT 时那个进程随后会退出，紧跟的下一条消息走重启（--resume），
// 不报错、不丢，也不记成意外退出。
func TestClaudeInterruptThenNextMessage(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	interrupt := func(msg string) executor.Result {
		t.Helper()
		run := uuid.Must(uuid.NewV7())
		sink := newSink()
		done := make(chan executor.Result, 1)
		go func() {
			res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: msg}, sink)
			if err != nil {
				t.Error(err)
			}
			done <- res
		}()
		select {
		case <-sink.gotText:
		case <-time.After(15 * time.Second):
			t.Fatal("没有输出")
		}
		if err := h.e.Interrupt(context.Background(), id, run); err != nil {
			t.Fatal(err)
		}
		return <-done
	}
	if res := interrupt("fake:slow"); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("控制消息打断：%+v", res)
	}
	pid := h.claudePid(id)
	if res, _ := h.exec(id, "fake:history"); res.Text != "history=1" || h.claudePid(id) != pid {
		t.Fatalf("确认打断后复用同一进程：%+v", res)
	}
	if res := interrupt("fake:noack"); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("SIGINT 打断：%+v", res)
	}
	res, _ := h.exec(id, "fake:history")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "history=3" {
		t.Fatalf("打断后紧跟的下一条应在新进程里接续：%+v", res)
	}
	if args := h.launches()[len(h.launches())-1]; !hasFlag(args, "--resume") {
		t.Fatalf("新进程应 --resume：%v", args)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	for _, ev := range h.obs.events {
		if ev.ev.Type == store.EventPiExited {
			t.Fatalf("SIGINT 之后的退出是平台的意思，不记成意外退出：%+v", ev.ev.Data)
		}
	}
}

// 任务实例首次启动：从平台稳定复制的基线分叉（--resume <基线文件> --fork-session，新会话 ID 由平台给）。
func TestClaudeTaskFork(t *testing.T) {
	h := newClaudeHarness(t, nil)
	baseline := filepath.Join(t.TempDir(), "baseline.jsonl")
	if err := os.WriteFile(baseline, []byte("{\"type\":\"user\",\"text\":\"a\"}\n{\"type\":\"user\",\"text\":\"b\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.ForkSessionFile = baseline })
	res, _ := h.exec(id, "fake:history")
	if res.Text != "history=2" {
		t.Fatalf("分叉应带上基线里的历史：%+v", res)
	}
	args := h.launches()[0]
	if argValue(args, "--resume") != baseline || !hasFlag(args, "--fork-session") || !claudecli.ValidSessionID(argValue(args, "--session-id")) {
		t.Fatalf("分叉参数：%v", args)
	}
}

// 从 Pi 换到 Claude Code：旧的 Pi 进程停止时不把它的会话文件记回去（数据库里的会话已重置）。
func TestClaudeRuntimeSwitchDoesNotRecordOldSession(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.agent(nil)
	if res, _ := h.exec(id, "pi turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Pi：%+v", res)
	}
	h.src.mu.Lock()
	piSession := h.src.recorded[id]
	h.src.recorded[id] = "" // 切换运行时时数据库里的会话已重置
	h.src.recordLog[id] = nil
	h.src.mu.Unlock()
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	p.sessMu.Lock()
	p.recorded = "" // 让旧进程停止时“有东西可记”：若不拦住就会把 Pi 的会话记回去
	p.sessMu.Unlock()
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Provider, s.ModelID = "claude-code", "haiku" })
	if res, _ := h.exec(id, "claude turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Claude Code：%+v", res)
	}
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	if slices.Contains(h.src.recordLog[id], piSession) || !strings.HasSuffix(h.src.recorded[id], ".jsonl") || h.src.recorded[id] == piSession {
		t.Fatalf("记下的会话：%v（Pi 的是 %s）", h.src.recordLog[id], piSession)
	}
}

// purgeLog 返回 fakeclaude 记下的 purge 调用。
func (h *claudeHarness) purgeLog() []string {
	b, _ := os.ReadFile(filepath.Join(h.realHome, ".fakeclaude-purge.log"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// 释放（删除 Agent、任务实例退役）时清理 Claude Code 为工作区留下的数据：先把会话记录归档进 Agent 的 sessions/claude，
// 再 claude purge <工作区> -y。目标只能正是这个 Agent 的工作区：绑定的本机目录、别的 Agent 的工作区、工作根之外的路径一律不运行；
// 没用过 Claude Code 的 Agent 不起 claude；找不到 claude 时跳过并记日志；归档失败就不 purge。
func TestClaudePurgeOnRelease(t *testing.T) {
	h := newClaudeHarness(t, nil)
	localDir := t.TempDir()
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: localDir, Note: "user files"}}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("执行：%+v", res)
	}
	d := h.e.dirs(id)
	h.src.mu.Lock()
	transcript := h.src.recorded[id]
	h.src.mu.Unlock()
	pi := h.agent(nil)
	if res, _ := h.exec(pi, "pi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Pi：%+v", res)
	}
	if err := h.e.Release(context.Background(), pi); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 0 {
		t.Fatalf("没用过 Claude Code 的 Agent 不清理：%v", got)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 1 || got[0] != "purge "+d.workspace+" -y" {
		t.Fatalf("purge 调用：%v（工作区 %s）", got, d.workspace)
	}
	want, _ := os.ReadFile(transcript)
	if got, err := os.ReadFile(filepath.Join(d.sessions, "claude", filepath.Base(transcript))); err != nil || !bytes.Equal(got, want) || len(want) == 0 {
		t.Fatalf("purge 之前应归档会话记录：%v", err)
	}
	bin := fakeclaudebin.Path(t)
	env := h.e.claudeHelperEnv(context.Background(), h.realHome)
	for name, path := range map[string]string{"绑定的本机目录": localDir, "别的 Agent 的工作区": h.e.dirs(pi).workspace, "工作根之外": filepath.Join(t.TempDir(), "agents", id.String(), "workspace"), "相对路径": "workspace"} {
		if err := h.e.purgeClaudeWorkspace(context.Background(), bin, env, id, path); err == nil {
			t.Errorf("%s 不能成为 purge 的目标", name)
		}
	}
	if got := h.purgeLog(); len(got) != 1 {
		t.Fatalf("被拒绝的路径不应调用 claude：%v", got)
	}

	// 找不到 claude：跳过并记日志。
	missing := newClaudeHarness(t, nil)
	other := missing.claudeAgent(nil)
	missing.e.o.ClaudeBin = filepath.Join(t.TempDir(), "no-claude")
	if err := missing.e.Release(context.Background(), other); err != nil || !strings.Contains(missing.logs.String(), "找不到 claude") {
		t.Fatalf("找不到 claude 时跳过并记日志：%v %s", err, missing.logs.String())
	}
}

// 归档失败（会话记录读不出来）就不 purge：宁可留着也不丢记录。
func TestClaudePurgeSkippedWhenArchiveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 读得出无权限的文件")
	}
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("执行：%+v", res)
	}
	h.src.mu.Lock()
	transcript := h.src.recorded[id]
	h.src.mu.Unlock()
	if err := os.Chmod(transcript, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(transcript, 0o600) })
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 0 {
		t.Fatalf("归档失败时不应 purge：%v", got)
	}
	if !strings.Contains(h.logs.String(), "归档 Claude Code 的会话记录失败") {
		t.Fatalf("应记日志：%s", h.logs.String())
	}
}
