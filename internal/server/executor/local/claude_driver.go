package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/ccstream"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/store"
)

// ClaudeCodeTools 是 Claude Code 运行时的 Agent 能用的内置工具（--tools 白名单），用户的决定集中在这一处。
// 只放作用在 Agent 自身的基础工具；会作用到 Agent 之外的一律不放：给用户本机其他 Claude 会话发消息或列出它们（SendMessage、
// ListAgents）、推送到用户手机（PushNotification）、建云端例行任务（RemoteTrigger）、建定时任务或自己定时唤醒
// （CronCreate/Delete/List、ScheduleWakeup）、在平台的一轮之外异步唤醒会话（Monitor）、多 Agent 编排（Workflow）、
// 切换 worktree、设计同步与审查报告。MCP 工具不受这个白名单限制。
var ClaudeCodeTools = []string{"Task", "TaskStop", "Bash", "Read", "Edit", "Write", "NotebookEdit", "Skill", "WebSearch", "ToolSearch"}

// ClaudeCodeHiddenSkills 是 disableBundledSkills 之后仍会出现的 Claude Code 自带技能（2.1.322 实测），经 skillOverrides 设为 off，
// 对模型隐藏。它们不归平台管，有的会引导模型去改用户 ~/.claude 下的设置。Agent 自己的同名 Skill 带插件命名空间（loop-now:<名>），不受影响。
// claude 升级后多出的自带技能会在启动时记警告日志（见 checkClaudeSkills），再补进这里。
var ClaudeCodeHiddenSkills = []string{"design", "doctor", "plugin-authoring"}

// claudePluginName 是平台给 Claude Code 的插件名：其中的技能以 loop-now:<名> 出现。
const claudePluginName = "loop-now"

// claudeSettings 经 --settings 传给 Claude Code：关掉它自己的自动记忆（Agent 的记忆以平台的上下文文件夹为准）；关掉自带的技能
// （disableBundledSkills，插件的技能不受影响），并隐藏关掉之后仍会出现的那几个。Agent 看到的技能只有平台给的。
var claudeSettings = func() string {
	overrides := map[string]string{}
	for _, name := range ClaudeCodeHiddenSkills {
		overrides[name] = "off"
	}
	b, _ := json.Marshal(map[string]any{"autoMemoryEnabled": false, "disableBundledSkills": true, "skillOverrides": overrides})
	return string(b)
}()

// claudeEfforts 是能作为 --effort 传的思考等级。
var claudeEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// claudeDriver 是用户本机安装的 Claude Code 运行时：从登录 shell 的 PATH 找到未经修改的 claude，以真实的 HOME 运行
// （用用户本人在本机的登录，平台不读、不存它的凭证），stream-json 模式的常驻进程。
type claudeDriver struct{ e *Executor }

func isClaude(snap agentconfig.Snapshot) bool {
	return agentruntime.ForProvider(snap.Provider) == agentruntime.ClaudeCode
}

func (claudeDriver) name() string { return "Claude Code" }

func (claudeDriver) runtime() string { return agentruntime.ClaudeCode }

// identity 是模型与 effort：两者都只能在启动时给出，变了就重启（--resume 接续会话）。
func (claudeDriver) identity(snap agentconfig.Snapshot) string {
	return snap.ModelID + "|" + claudeEffort(snap)
}

func claudeEffort(snap agentconfig.Snapshot) string {
	if snap.ThinkingLevel != nil && claudeEfforts[*snap.ThinkingLevel] {
		return *snap.ThinkingLevel
	}
	return ""
}

// refresh：Claude Code 没有平台写的模型缓存文件。
func (claudeDriver) refresh(agentconfig.Snapshot, agentDirs) error { return nil }

// claudeBinary 找到 claude：测试与显式配置用 Options.ClaudeBin，否则在用户登录 shell 的 PATH 里找（与“用本机的 CLI”开关无关）。
func (e *Executor) claudeBinary(ctx context.Context) (string, error) {
	if e.o.ClaudeBin != "" {
		if info, err := os.Stat(e.o.ClaudeBin); err != nil || !info.Mode().IsRegular() {
			return "", claudecli.ErrNotFound
		}
		return e.o.ClaudeBin, nil
	}
	var dirs []string
	if e.o.UserPath != nil {
		dirs = e.o.UserPath(ctx)
	}
	return claudecli.Find(dirs)
}

// ClaudeProjectsDir 返回本机 Claude Code 存放会话记录的目录（任务分叉复制基线时用）：以 claude auth status 的
// projectsDirectory 为准，找不到 claude 或问不到时按默认规则推；读不出服务端用户的主目录时为空。
func (e *Executor) ClaudeProjectsDir(ctx context.Context) string {
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	bin, err := e.claudeBinary(ctx)
	if err != nil {
		return claudecli.DefaultProjectsDir(home)
	}
	return e.claude.ProjectsDir(ctx, bin, e.claudeHelperEnv(ctx, home), home)
}

// purgeClaude 在删除 Agent、任务实例退役（都经 Release）时清理 Claude Code 为这个工作区留下的数据：只在 claude 为它建过会话记录目录时做；
// 先把那个目录里的会话记录复制进 Agent 自己的 sessions/claude（平台的归档，同 Pi 的会话文件一样保留），再运行 claude purge <工作区> -y。
// 目标只能是平台工作根之下、属于这个 Agent 的工作区；任何一步失败只记日志。
func (e *Executor) purgeClaude(ctx context.Context, agentID uuid.UUID) {
	if e.o.RealHome == nil || e.claude == nil || e.log == nil {
		return // 不经 New 构造的执行器（测试用）：没有 Claude Code
	}
	d := e.dirs(agentID)
	log := e.log.With("agentId", agentID)
	if !within(e.o.WorkRoot, d.workspace) {
		log.Error("工作区不在平台的工作根之下，不清理 Claude Code 的数据", "workspace", d.workspace)
		return
	}
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return
	}
	bin, err := e.claudeBinary(ctx)
	if err != nil {
		log.Info("找不到 claude，跳过清理 Claude Code 的数据")
		return
	}
	env := e.claudeHelperEnv(ctx, home)
	projectDir := filepath.Join(e.claude.ProjectsDir(ctx, bin, env, home), claudecli.ProjectDirName(d.workspace))
	if info, err := os.Lstat(projectDir); err != nil || !info.IsDir() {
		return // 这个工作区没用过 Claude Code
	}
	if err := archiveClaudeTranscripts(projectDir, filepath.Join(d.sessions, "claude")); err != nil {
		log.Warn("归档 Claude Code 的会话记录失败，不清理它的数据", "err", err)
		return
	}
	if err := e.purgeClaudeWorkspace(ctx, bin, env, agentID, d.workspace); err != nil {
		log.Warn("claude purge 失败", "err", err)
	}
}

// purgeClaudeWorkspace 运行 claude purge <path> -y。path 必须正是这个 Agent（或任务实例）在平台工作根之下的工作区：
// 别的路径（包括绑定给它的本机目录）一律不运行，记日志并返回错误。
func (e *Executor) purgeClaudeWorkspace(ctx context.Context, bin string, env []string, agentID uuid.UUID, path string) error {
	if !filepath.IsAbs(path) || !within(e.o.WorkRoot, path) || filepath.Clean(path) != e.dirs(agentID).workspace {
		e.log.Error("拒绝清理不是该 Agent 工作区的路径", "agentId", agentID, "path", path)
		return fmt.Errorf("路径 %s 不是该 Agent 在平台工作根之下的工作区", path)
	}
	return claudecli.Purge(ctx, bin, env, path)
}

// archiveClaudeTranscripts 把 claude 为这个工作区留下的会话记录（目录里第一层的 <会话 ID>.jsonl，普通文件）复制到 dst。
func archiveClaudeTranscripts(projectDir, dst string) error {
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !claudecli.ValidSessionID(strings.TrimSuffix(name, ".jsonl")) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// ClaudeHelperEnv 是运行 claude 自己的短命令（--version、auth status、purge）时的环境：与 Agent 的 Claude Code 进程同一套基础变量
// （真实 HOME、USER／LOGNAME、透传的 LANG 等），PATH 为执行 PATH 的基础部分加用户登录 shell 的 PATH。GET /runtimes 也用它。
func (e *Executor) ClaudeHelperEnv(ctx context.Context, home string) []string {
	var dirs []string
	if e.o.UserPath != nil {
		dirs = e.o.UserPath(ctx)
	}
	var extra []string
	for _, name := range e.o.Passthrough {
		if v := e.o.Getenv(name); v != "" && !executor.IsReservedEnv(name) && !e.modelSecretEnv(name) {
			extra = append(extra, name+"="+v)
		}
	}
	return claudecli.Env(claudecli.EnvOptions{Home: home, User: e.o.Username, Path: append(filepath.SplitList(e.o.ExecBasePath), dirs...), Extra: extra})
}

func (e *Executor) claudeHelperEnv(ctx context.Context, home string) []string {
	return e.ClaudeHelperEnv(ctx, home)
}

// start 找到 claude、读出版本，按会话状态组装参数后启动。
func (cd claudeDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := cd.e, spec.snap, spec.d
	bin, err := e.claudeBinary(ctx)
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "本机没有找到 Claude Code：在登录 shell 的 PATH 里找不到 claude，请先在这台机器上安装并登录")
	}
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return started{}, fail(executor.ErrInternal, "读取服务端用户的主目录失败（Claude Code 需要用本机的登录）")
	}
	version, err := e.claude.Version(ctx, bin, e.claudeHelperEnv(ctx, home))
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "无法读取本机 Claude Code（%s）的版本：%v", bin, err)
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--model", snap.ModelID}
	if effort := claudeEffort(snap); effort != "" {
		args = append(args, "--effort", effort)
	}
	args = append(args, "--setting-sources", "", "--settings", claudeSettings, "--strict-mcp-config")
	if _, err := os.Stat(d.claudeMCP); err == nil {
		args = append(args, "--mcp-config", d.claudeMCP)
	}
	args = append(args, "--permission-mode", "bypassPermissions", "--permission-prompts", "none", "--system-prompt-snapshot", "off")
	if _, err := os.Stat(d.prompt); err == nil {
		args = append(args, "--append-system-prompt-file", d.prompt)
	}
	args = append(args, "--plugin-dir", d.claudePlugin, "--tools", strings.Join(ClaudeCodeTools, ","))
	sessionID, sessionArgs := claudeSessionArgs(spec)
	args = append(args, sessionArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Env = claudeEnv(spec.env)
	cmd.Dir = d.workspace
	procgroup.Configure(cmd)
	if err := ctx.Err(); err != nil {
		return started{}, err
	}
	client, err := ccstream.Start(cmd, ccstream.Options{InitTimeout: e.o.StartTimeout, Kill: procgroup.Kill})
	if err != nil {
		return started{}, fail(executor.ErrRuntimeStartFailed, "启动 Claude Code %s 失败：%v", version, err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Claude Code 进程没有纳入整组管理，停止时只能结束它本身", "pid", client.Pid(), "err", err)
	}
	return started{rt: client, binary: bin, version: version, sessionID: sessionID}, nil
}

// claudeSessionArgs 返回这次启动的会话 ID 与参数：有会话记录时接续（--resume <会话 ID>）；任务实例首次启动时从平台稳定复制的
// 基线分叉（--resume <基线文件> --fork-session，新会话 ID 由平台给）；否则以平台给的新会话 ID 开始。
func claudeSessionArgs(spec startSpec) (string, []string) {
	if spec.sessionFile != "" {
		if id := strings.TrimSuffix(filepath.Base(spec.sessionFile), ".jsonl"); claudecli.ValidSessionID(id) {
			return id, []string{"--resume", id}
		}
	}
	id := uuid.NewString()
	if spec.snap.ForkSessionFile != "" {
		return id, []string{"--resume", spec.snap.ForkSessionFile, "--fork-session", "--session-id", id}
	}
	return id, []string{"--session-id", id}
}

// claudeEnv 是交给 claude 的环境：平台公共的环境去掉 Pi 的目录变量，以及一切 Anthropic／Claude Code 的变量
// （不论来自透传、CLI 凭证还是插件）：只用本机登录，不让 API key、令牌或配置目录改变它的认证与配置。
func claudeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range claudecli.StripEnv(env) {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PI_CODING_AGENT_DIR" || k == "PI_OFFLINE" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// attach 接上用量与额度事件。
func (cd claudeDriver) attach(p *proc) {
	if cc, ok := p.rt.(*ccstream.Client); ok {
		cc.SetObserver(claudeObserver{e: cd.e, p: p})
	}
}

// ready：claude 收到第一条消息才输出 init，启动成功即进程起来了；第一轮之前退出或超时由那一轮报成启动失败。
func (claudeDriver) ready(context.Context, *proc, startSpec) error { return nil }

func (claudeDriver) beforeTurn(context.Context, *proc, agentconfig.Snapshot) error { return nil }

// afterTurn 记下会话记录的位置，并把需要进一步判断的失败说清楚：api_error 且没有 HTTP 状态时问 claude auth status 是不是没登录；
// 启动失败带上版本号与错误输出末尾。
func (cd claudeDriver) afterTurn(p *proc, tr runturn.Result) runturn.Result {
	e := cd.e
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home, _ := e.o.RealHome()
	env := e.claudeHelperEnv(ctx, home)
	if path := claudecli.TranscriptPath(e.claude.ProjectsDir(ctx, p.runtimeBinary, env, home), p.dirs.workspace, p.sessionID); path != "" {
		p.sessMu.Lock()
		p.sessionFile = path
		p.sessMu.Unlock()
	}
	cc, _ := p.rt.(*ccstream.Client)
	if cc != nil {
		if init := cc.Init(); init != nil && !p.initChecked.Swap(true) {
			checkClaudeSkills(p, init)
		}
	}
	switch tr.ErrorCode {
	case executor.ErrRuntimeError:
		if cc == nil {
			break
		}
		if last := cc.LastResult(); last.TerminalReason == "api_error" && last.APIErrorStatus == nil {
			if auth, err := claudecli.AuthStatus(ctx, p.runtimeBinary, env); err == nil && !auth.LoggedIn {
				tr.ErrorCode = executor.ErrRuntimeLoginRequired
				tr.ErrorMessage = "本机的 Claude Code 没有登录：请在这台机器上运行 claude 完成登录后再试"
			}
		}
	case executor.ErrRuntimeStartFailed:
		select {
		case <-p.rt.Done():
		case <-time.After(e.o.StopGrace):
		}
		tr.ErrorMessage = strings.TrimSpace(fmt.Sprintf("Claude Code %s：%s %s", p.runtimeVersion, tr.ErrorMessage, tail(p.rt.Stderr(), e.o.StderrTailBytes)))
	}
	return tr
}

// checkClaudeSkills 核对进程报告的技能（每个进程一次）：Agent 应该只看到平台给的（loop-now:<名>）。多出来的多半是 claude 升级后
// 新增的自带技能，记一条警告日志（带版本号与技能名），不阻断执行；据此补进 ClaudeCodeHiddenSkills。
func checkClaudeSkills(p *proc, init *ccstream.Init) {
	var extra []string
	for _, s := range init.Skills {
		if !strings.HasPrefix(s, claudePluginName+":") {
			extra = append(extra, s)
		}
	}
	if len(extra) > 0 {
		p.log.Warn("Claude Code 报告了平台以外的技能，Agent 能看到它们；请把它们补进 ClaudeCodeHiddenSkills", "version", init.Version, "skills", extra)
	}
}

func (claudeDriver) exited() (string, string) {
	return executor.ErrRuntimeExited, "Claude Code 进程已退出"
}

func (claudeDriver) crashMessage(p *proc, stderrTail string) string {
	return "Claude Code " + p.runtimeVersion + " 进程异常退出 " + stderrTail
}

// claudeObserver 把 Claude Code 报告的用量（每轮每个模型一条）与额度事件交给执行器的观察者。
type claudeObserver struct {
	e *Executor
	p *proc
}

func (o claudeObserver) runID(inTurn bool) *uuid.UUID {
	if !inTurn {
		return nil
	}
	return o.p.curRun.Load()
}

func (o claudeObserver) Usage(u ccstream.Usage, inTurn bool) {
	o.e.obs.Usage(o.p.agentID, o.runID(inTurn), executor.Usage{Kind: "assistant", Provider: agentruntime.ClaudeCodeProvider, Model: u.Model, ResponseModel: u.Model,
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning})
}

func (o claudeObserver) RateLimit(info map[string]any, inTurn bool) {
	o.e.event(o.p.agentID, o.runID(inTurn), store.EventModelRateLimit, map[string]any{"info": info})
}

// syncClaudePlugin 重建平台给 Claude Code 的插件目录（--plugin-dir）：内置 Skill loopctl、Agent 的 Skill 副本与已授权插件的 Skill，
// 每次启动整体重建。返回的技能名带插件名作命名空间（如 loop-now:loopctl）。
func (e *Executor) syncClaudePlugin(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) error {
	if err := os.RemoveAll(d.claudePlugin); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(d.claudePlugin, ".claude-plugin"), 0o700); err != nil {
		return err
	}
	manifest, _ := json.Marshal(map[string]string{"name": claudePluginName, "description": "Loop Now platform Skills"})
	if err := os.WriteFile(filepath.Join(d.claudePlugin, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
		return err
	}
	dir := filepath.Join(d.claudePlugin, "skills")
	if _, err := e.syncSkills(ctx, snap, dir); err != nil {
		return err
	}
	for _, src := range snap.Plugins.Skills {
		target := filepath.Join(dir, filepath.Base(src))
		if _, err := os.Lstat(target); err == nil {
			e.snapshotLog(snap).Warn("插件的 Skill 与已有的 Skill 同名，已跳过", "skill", filepath.Base(src))
			continue
		}
		if err := copyTree(src, target); err != nil {
			return fmt.Errorf("复制插件的 Skill %s 失败：%w", filepath.Base(src), err)
		}
	}
	return nil
}

// copyTree 复制目录里的普通文件与子目录（不跟随软链接，跳过特殊文件）。
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case de.IsDir():
			return os.MkdirAll(target, 0o700)
		case de.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, fileMode(p))
		}
		return nil
	})
}
