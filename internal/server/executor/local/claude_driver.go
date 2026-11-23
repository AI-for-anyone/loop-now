package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// ClaudeCodeTools are the built-in tools an Agent on the Claude Code runtime may use (the --tools allowlist); the
// user's decisions are kept in this one place. Only basic tools that act on the Agent itself are included; anything
// that acts beyond the Agent is left out: sending messages to or listing the user's other local Claude sessions
// (SendMessage, ListAgents), pushing to the user's phone (PushNotification), creating cloud routines (RemoteTrigger),
// creating scheduled tasks or waking itself on a timer (CronCreate/Delete/List, ScheduleWakeup), waking the session
// asynchronously outside a platform turn (Monitor), multi-Agent orchestration (Workflow), switching worktrees, design
// sync and review reports. MCP tools are not limited by this allowlist.
var ClaudeCodeTools = []string{"Task", "TaskStop", "Bash", "Read", "Edit", "Write", "NotebookEdit", "Skill", "WebSearch", "ToolSearch"}

// ClaudeCodeHiddenSkills are Claude Code's bundled Skills that still show up after disableBundledSkills (measured on
// 2.1.322); they are set to off through skillOverrides and so hidden from the model. They are not managed by the
// platform, and some of them lead the model to change the user's settings under ~/.claude. An Agent's own Skill with
// the same name carries the plugin namespace (loop-now:<name>) and is not affected. Bundled Skills added by a claude
// upgrade are logged as a warning at startup (see checkClaudeSkills) and should then be added here.
var ClaudeCodeHiddenSkills = []string{"design", "doctor", "plugin-authoring"}

// claudePluginName is the plugin name the platform gives Claude Code: its Skills appear as loop-now:<name>.
const claudePluginName = "loop-now"

// claudeSettings is passed to Claude Code via --settings: it turns off Claude Code's own auto memory (an Agent's memory
// lives in the platform's context folders); it turns off the bundled Skills (disableBundledSkills, which does not
// affect plugin Skills) and hides the few that still show up after that. The only Skills an Agent sees are the ones the
// platform gives it.
var claudeSettings = func() string {
	overrides := map[string]string{}
	for _, name := range ClaudeCodeHiddenSkills {
		overrides[name] = "off"
	}
	b, _ := json.Marshal(map[string]any{"autoMemoryEnabled": false, "disableBundledSkills": true, "skillOverrides": overrides})
	return string(b)
}()

// claudeEfforts are the thinking levels that can be passed as --effort.
var claudeEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// claudeDriver is the Claude Code runtime installed on the user's machine: an unmodified claude found on the login
// shell's PATH, run with the real HOME (using the user's own local login; the platform neither reads nor stores its
// credentials), as a long-running process in stream-json mode.
type claudeDriver struct{ e *Executor }

// claudeProcess is a Claude Code process: the stream-json client plus what the driver keeps about this process.
type claudeProcess struct {
	*ccstream.Client
	// binary and version are the real path and the version of the claude executable; sessionID is the session ID the
	// platform gave this process.
	binary, version, sessionID string
	// initChecked: the Skill list claude reported has been checked (once per process).
	initChecked atomic.Bool
}

// claudeOf is the Claude Code process of p; nil when p runs another runtime.
func claudeOf(p *proc) *claudeProcess {
	cp, _ := p.rt.(*claudeProcess)
	return cp
}

// claudeVersion is the claude version of p's process; empty when unknown.
func claudeVersion(p *proc) string {
	if cp := claudeOf(p); cp != nil {
		return cp.version
	}
	return ""
}

func (claudeDriver) name() string { return runtimeName(agentruntime.ClaudeCode) }

func (claudeDriver) runtime() string { return agentruntime.ClaudeCode }

// identity is the model and effort: both can only be given at startup, so a change means restart (--resume continues
// the session).
func (claudeDriver) identity(snap agentconfig.Snapshot) string {
	return snap.ModelID + "|" + claudeEffort(snap)
}

func claudeEffort(snap agentconfig.Snapshot) string {
	if snap.ThinkingLevel != nil && claudeEfforts[*snap.ThinkingLevel] {
		return *snap.ThinkingLevel
	}
	return ""
}

// refresh: Claude Code has no model cache file written by the platform.
func (claudeDriver) refresh(agentconfig.Snapshot, agentDirs) error { return nil }

// syncAssets rebuilds the plugin directory the platform gives Claude Code (--plugin-dir: loopctl, the Agent's Skill
// copies and the granted plugins' Skills) and writes the plugin MCP servers into claude-mcp.json (--mcp-config, the same
// format as Pi's mcp.json). Claude Code finds the Skills itself, so no directories are returned.
func (cd claudeDriver) syncAssets(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error) {
	if err := cd.e.syncClaudePlugin(ctx, snap, d); err != nil {
		return nil, err
	}
	return nil, writeMCPConfig(d.claudeMCP, snap.Plugins)
}

func (claudeDriver) clearAssets(d agentDirs) error {
	if err := os.RemoveAll(d.claudePlugin); err != nil {
		return err
	}
	return removeIfExists(d.claudeMCP)
}

// modelCredentials: Claude Code uses its own local login; the platform writes no model credentials and no models.json.
func (claudeDriver) modelCredentials(agentconfig.Snapshot, agentDirs, string) error { return nil }

// purge archives and removes the session records Claude Code keeps under ~/.claude for this workspace (purgeClaude).
func (cd claudeDriver) purge(ctx context.Context, agentID uuid.UUID) { cd.e.purgeClaude(ctx, agentID) }

// forkBaseline reads the regular Claude Code session for a task fork: only the regular session's one
// <session ID>.jsonl, which must be exactly one level below claude's session record directory (through os.Root); it
// scans and reads nothing else under ~/.claude. At most maxBytes, and every line must be complete JSON.
func (cd claudeDriver) forkBaseline(ctx context.Context, _ uuid.UUID, source string, maxBytes int64) ([]byte, error) {
	projects := cd.e.ClaudeProjectsDir(ctx)
	if !claudecli.InProjects(projects, source) {
		return nil, errors.New("the regular session is not in the Claude Code session record directory")
	}
	rel, _ := filepath.Rel(projects, source)
	root, err := os.OpenRoot(projects)
	if err != nil {
		return nil, errors.New("the Claude Code session record directory cannot be read")
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("the regular session must be a plain file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, errors.New("the regular session cannot be read")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("the regular session exceeds the size limit or cannot be read")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("the regular session has no valid records")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, errors.New("the regular session contains incomplete records")
		}
	}
	return data, nil
}

// claudeBinary finds claude: tests and explicit configuration use Options.ClaudeBin; otherwise it is looked up on the
// PATH of the user's login shell (regardless of the “用本机的 CLI” ("Use local CLIs") switch).
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

// ClaudeProjectsDir returns the directory where the local Claude Code keeps session records (used when a task fork
// copies the baseline): projectsDirectory from claude auth status is authoritative; when claude is not found or cannot
// be asked, it is derived by the default rule. It is empty when the server user's home directory cannot be read.
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

// purgeClaude cleans up the data Claude Code left for this workspace when an Agent is deleted or a task instance is
// retired (both via Release). It only acts when claude created a session record directory for it: it first copies the
// session records in that directory into the Agent's own sessions/claude (the platform's archive, kept just like Pi's
// session files), then runs claude purge <workspace> -y. The target can only be a workspace under the platform's work
// root that belongs to this Agent; any failing step is only logged.
func (e *Executor) purgeClaude(ctx context.Context, agentID uuid.UUID) {
	if e.o.RealHome == nil || e.claude == nil || e.log == nil {
		return // executor not built by New (for tests): no Claude Code
	}
	d := e.dirs(agentID)
	log := e.log.With("agentId", agentID)
	if !within(e.o.WorkRoot, d.workspace) {
		log.Error("workspace is not under the platform's work root; not cleaning up Claude Code's data", "workspace", d.workspace)
		return
	}
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return
	}
	bin, err := e.claudeBinary(ctx)
	if err != nil {
		log.Info("claude not found; skipping cleanup of Claude Code's data")
		return
	}
	env := e.claudeHelperEnv(ctx, home)
	projects := e.claude.ProjectsDir(ctx, bin, env, home)
	found := false
	for _, name := range claudecli.ProjectDirNames(d.workspace) {
		projectDir := filepath.Join(projects, name)
		if info, err := os.Lstat(projectDir); err != nil || !info.IsDir() {
			continue
		}
		found = true
		if err := archiveClaudeTranscripts(projectDir, filepath.Join(d.sessions, "claude")); err != nil {
			log.Warn("failed to archive the Claude Code session transcript; not cleaning up its data", "err", err)
			return
		}
	}
	if !found {
		return // this workspace never used Claude Code
	}
	if err := e.purgeClaudeWorkspace(ctx, bin, env, agentID, d.workspace); err != nil {
		log.Warn("claude purge failed", "err", err)
	}
}

// purgeClaudeWorkspace runs claude purge <path> -y. path must be exactly this Agent's (or task instance's) workspace
// under the platform's work root: for any other path (including local directories bound to it) nothing is run; it logs
// and returns an error.
func (e *Executor) purgeClaudeWorkspace(ctx context.Context, bin string, env []string, agentID uuid.UUID, path string) error {
	if !filepath.IsAbs(path) || !within(e.o.WorkRoot, path) || filepath.Clean(path) != e.dirs(agentID).workspace {
		e.log.Error("refusing to clean up a path that is not this Agent's workspace", "agentId", agentID, "path", path)
		return fmt.Errorf("path %s is not this Agent's workspace under the platform work root", path)
	}
	return claudecli.Purge(ctx, bin, env, path)
}

// archiveClaudeTranscripts copies the session records claude left for this workspace (the <session ID>.jsonl regular
// files at the top level of the directory) to dst.
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

// ClaudeHelperEnv is the environment for running claude's own short commands (--version, auth status, purge): the same
// base variables as the Agent's Claude Code process (real HOME, USER/LOGNAME, passed-through LANG, etc.), with PATH set
// to the base part of the run PATH plus the PATH of the user's login shell. GET /runtimes uses it too.
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

// start finds claude, reads its version, builds the arguments from the session state and starts it.
func (cd claudeDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := cd.e, spec.snap, spec.d
	bin, err := e.claudeBinary(ctx)
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "Claude Code was not found on this computer: claude is not on the login shell's PATH. Install Claude Code on this computer and log in first.")
	}
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return started{}, fail(executor.ErrInternal, "Could not read the server user's home directory (Claude Code needs this computer's login).")
	}
	version, err := e.claude.Version(ctx, bin, e.claudeHelperEnv(ctx, home))
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "Could not read the version of Claude Code on this computer (%s): %v", bin, err)
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
		return started{}, fail(executor.ErrRuntimeStartFailed, "Could not start Claude Code %s: %v", version, err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Claude Code process is not under group management; stopping can only end the process itself", "pid", client.Pid(), "err", err)
	}
	return started{rt: &claudeProcess{Client: client, binary: bin, version: version, sessionID: sessionID}}, nil
}

// claudeSessionArgs returns the session ID and arguments for this start: with a session record it continues that
// session (--resume <session ID>); on a task instance's first start it forks from the baseline the platform copied in a
// stable state (--resume <baseline file> --fork-session, with the new session ID given by the platform); otherwise it
// starts with a new session ID given by the platform.
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

// claudeEnv is the environment given to claude: the platform's common environment without Pi's directory variables and
// without any Anthropic/Claude Code variables (whether passed through, from CLI credentials or from plugins): only the
// local login is used, so no API key, token or config directory can change its authentication and configuration.
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

// attach hooks up usage and rate limit events.
func (cd claudeDriver) attach(p *proc) {
	if cp := claudeOf(p); cp != nil {
		cp.SetObserver(claudeObserver{e: cd.e, p: p})
	}
}

// ready: claude only prints init after it receives the first message, so a successful start means the process is up; an
// exit or timeout before the first turn is reported by that turn as a start failure.
func (claudeDriver) ready(context.Context, *proc, startSpec) error { return nil }

func (claudeDriver) beforeTurn(context.Context, *proc, agentconfig.Snapshot) error { return nil }

// afterTurn records where the session record is and makes failures that need a closer look clear: for an api_error
// without an HTTP status it asks claude auth status whether the user is logged out; a start failure gets the version
// and the end of the error output.
func (cd claudeDriver) afterTurn(p *proc, tr runturn.Result) runturn.Result {
	e, cp := cd.e, claudeOf(p)
	if cp == nil {
		return tr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home, _ := e.o.RealHome()
	env := e.claudeHelperEnv(ctx, home)
	if path := claudecli.TranscriptPath(e.claude.ProjectsDir(ctx, cp.binary, env, home), p.dirs.workspace, cp.sessionID); path != "" {
		p.sessMu.Lock()
		p.sessionFile = path
		p.sessMu.Unlock()
	} else if !p.sessionUntracked.Swap(true) {
		// The session record written by claude was not found (for example on Windows, where it encodes the working
		// directory differently from us): the turn ends normally and this is not treated as a save failure; the
		// conversation continues as usual in this process, and the next start opens a new session.
		p.log.Warn("Claude Code session transcript not found; the next start opens a new session", "sessionId", cp.sessionID, "workspace", p.dirs.workspace, "version", cp.version)
	}
	if init := cp.Init(); init != nil && !cp.initChecked.Swap(true) {
		checkClaudeSkills(p, init)
	}
	switch tr.ErrorCode {
	case executor.ErrRuntimeError:
		if last := cp.LastResult(); last.TerminalReason == "api_error" && last.APIErrorStatus == nil {
			if auth, err := claudecli.AuthStatus(ctx, cp.binary, env); err == nil && !auth.LoggedIn {
				tr.ErrorCode = executor.ErrRuntimeLoginRequired
				tr.ErrorMessage = "Claude Code is not logged in on this computer. Run claude on this computer, log in, and try again."
			}
		}
	case executor.ErrRuntimeStartFailed:
		select {
		case <-p.rt.Done():
		case <-time.After(e.o.StopGrace):
		}
		tr.ErrorMessage = strings.TrimSpace(fmt.Sprintf("Claude Code %s: %s %s", cp.version, tr.ErrorMessage, tail(p.rt.Stderr(), e.o.StderrTailBytes)))
	}
	return tr
}

// checkClaudeSkills checks the Skills the process reports (once per process): an Agent should only see the ones the
// platform gives (loop-now:<name>). Extra ones are most likely bundled Skills added by a claude upgrade; it logs one
// warning (with the version and Skill names) and does not block the run; add them to ClaudeCodeHiddenSkills
// accordingly.
func checkClaudeSkills(p *proc, init *ccstream.Init) {
	var extra []string
	for _, s := range init.Skills {
		if !strings.HasPrefix(s, claudePluginName+":") {
			extra = append(extra, s)
		}
	}
	if len(extra) > 0 {
		p.log.Warn("Claude Code reported Skills from outside the platform, and the Agent can see them; add them to ClaudeCodeHiddenSkills", "version", init.Version, "skills", extra)
	}
}

func (claudeDriver) exited() (string, string) {
	return executor.ErrRuntimeExited, "The Claude Code process exited."
}

func (claudeDriver) crashMessage(p *proc, stderrTail string) string {
	return "Claude Code " + claudeVersion(p) + " process exited unexpectedly. " + stderrTail
}

// claudeObserver passes the usage Claude Code reports (one per model per turn) and rate limit events to the executor's
// observer.
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

// syncClaudePlugin rebuilds the plugin directory the platform gives Claude Code (--plugin-dir): the built-in Skill
// loopctl, the Agent's Skill copies and the Skills of granted plugins, rebuilt as a whole on every start. Claude Code
// shows these Skills with the plugin name as their namespace (such as loop-now:loopctl).
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
			e.snapshotLog(snap).Warn("a plugin Skill has the same name as an existing Skill; skipped", "skill", filepath.Base(src))
			continue
		}
		if err := copyTree(src, target); err != nil {
			return fmt.Errorf("copy the plugin Skill %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

// copyTree copies the regular files and subdirectories in a directory (not following symlinks, skipping special files).
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
