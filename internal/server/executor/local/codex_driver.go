package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/codexcli"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/codexapp"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// codexClientName is how the platform introduces itself to Codex (initialize); Codex records it as the originator of
// the threads.
const codexClientName = "loop-now"

// codexDriver is the Codex CLI installed on the user's computer: an unmodified codex found on the login shell's PATH,
// run as `codex app-server` with the user's Codex home (CODEX_HOME, where its login and conversations live; the
// platform neither reads nor stores its credentials) and the Agent's own HOME. One process holds the Agent's one
// thread; the thread id is the session reference.
type codexDriver struct{ e *Executor }

// codexProcess is a Codex app-server process with the thread it runs.
type codexProcess struct {
	*codexapp.Client
	binary, version string
	// threadID is the thread this process runs; model and effort are sent with every turn.
	threadID, model, effort string
	// started reports whether a turn of this process has started (Codex then has the thread saved).
	started bool
}

func codexOf(p *proc) *codexProcess {
	cp, _ := p.rt.(*codexProcess)
	return cp
}

// Prompt sends one message as a turn of the process's thread.
func (cp *codexProcess) Prompt(ctx context.Context, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error) {
	r, err := cp.Turn(ctx, text, codexapp.TurnOptions{ThreadID: cp.threadID, Model: cp.model, Effort: cp.effort, Images: images}, sink)
	if err == nil && cp.LastTurnStarted() {
		cp.started = true
	}
	return r, err
}

// Abort stops the turn in progress; the process stays.
func (cp *codexProcess) Abort(context.Context) error { return cp.Interrupt() }

func (codexDriver) name() string { return runtimeName(agentruntime.Codex) }

func (codexDriver) runtime() string { return agentruntime.Codex }

// identity: model and effort go with every turn and the thread settings with every start, so nothing of Codex's own
// needs a restart beyond what the executor compares.
func (codexDriver) identity(agentconfig.Snapshot) string { return "" }

// codexEffort is the reasoning effort for the Agent's thinking level: the level itself when Codex names it the same
// way; off leaves the model's default.
func codexEffort(snap agentconfig.Snapshot) string {
	if snap.ThinkingLevel == nil || *snap.ThinkingLevel == "off" {
		return ""
	}
	return *snap.ThinkingLevel
}

func (codexDriver) refresh(agentconfig.Snapshot, agentDirs) error { return nil }

// syncAssets rebuilds the Skill directory the platform gives Codex (loopctl, the Agent's Skill copies and the granted
// plugins' Skills); ready hands it to Codex as an extra Skill root. MCP servers go with the thread settings.
func (cd codexDriver) syncAssets(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error) {
	if _, err := cd.e.syncSkills(ctx, snap, d.codexSkills); err != nil {
		return nil, err
	}
	for _, src := range snap.Plugins.Skills {
		target := filepath.Join(d.codexSkills, filepath.Base(src))
		if _, err := os.Lstat(target); err == nil {
			cd.e.snapshotLog(snap).Warn("a plugin Skill has the same name as an existing Skill; skipped", "skill", filepath.Base(src))
			continue
		}
		if err := copyTree(src, target); err != nil {
			return nil, fmt.Errorf("copy the plugin Skill %s: %w", filepath.Base(src), err)
		}
	}
	return nil, nil
}

func (codexDriver) clearAssets(d agentDirs) error { return os.RemoveAll(d.codexSkills) }

// modelCredentials: Codex uses its own local login; the platform writes no model credentials.
func (codexDriver) modelCredentials(agentconfig.Snapshot, agentDirs, string) error { return nil }

// sessionSaved: a thread id can only be checked by continuing it; ready reports a missing thread.
func (codexDriver) sessionSaved(string) bool { return true }

// codexBinary finds codex: tests and explicit configuration use Options.CodexBin; otherwise it is looked up on the PATH
// of the user's login shell.
func (e *Executor) codexBinary(ctx context.Context) (string, error) {
	if e.o.CodexBin != "" {
		if info, err := os.Stat(e.o.CodexBin); err != nil || !info.Mode().IsRegular() {
			return "", codexcli.ErrNotFound
		}
		return e.o.CodexBin, nil
	}
	var dirs []string
	if e.o.UserPath != nil {
		dirs = e.o.UserPath(ctx)
	}
	return codexcli.Find(dirs)
}

// codexHome is the user's Codex home (CODEX_HOME, else .codex in the real home); empty when the home cannot be read.
func (e *Executor) codexHome() string {
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return codexcli.Home(e.o.Getenv, home)
}

// CodexHelperEnv is the environment for running codex outside an Agent (--version, login status, a short-lived
// app-server for the model list, forks and archiving): the real HOME, USER, the base part of the run PATH plus the PATH
// of the user's login shell, the passed-through variables, and CODEX_HOME. GET /runtimes uses it too.
func (e *Executor) CodexHelperEnv(ctx context.Context) []string {
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return nil
	}
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
	return codexcli.Env(codexcli.EnvOptions{Home: home, CodexHome: codexcli.Home(e.o.Getenv, home), User: e.o.Username,
		Path: append(filepath.SplitList(e.o.ExecBasePath), dirs...), Extra: extra})
}

// codexVersion reads the version of bin and checks it is supported.
func (e *Executor) codexVersion(ctx context.Context, bin string) (string, error) {
	version, err := codexcli.Version(ctx, bin, e.CodexHelperEnv(ctx))
	if err != nil {
		return "", fail(executor.ErrRuntimeUnavailable, "Could not read the version of Codex on this computer (%s): %v", bin, err)
	}
	if min := codexMinVersion(); !codexcli.AtLeast(version, min) {
		return version, fail(executor.ErrRuntimeUnavailable, "Codex %s on this computer is older than %s, the oldest version Loop Now supports. Update Codex and try again.", version, min)
	}
	return version, nil
}

func codexMinVersion() string {
	rt, _ := agentruntime.ByID(agentruntime.Codex)
	return rt.MinVersion
}

// start finds codex, checks its version and starts its app-server in the Agent's workspace.
func (cd codexDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := cd.e, spec.snap, spec.d
	bin, err := e.codexBinary(ctx)
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "Codex was not found on this computer: codex is not on the login shell's PATH. Install Codex on this computer and log in first.")
	}
	codexHome := e.codexHome()
	if codexHome == "" {
		return started{}, fail(executor.ErrInternal, "Could not read the server user's home directory (Codex needs this computer's login).")
	}
	version, err := e.codexVersion(ctx, bin)
	if err != nil {
		return started{}, err
	}
	cmd := exec.Command(bin, "app-server")
	cmd.Env = codexEnv(spec.env, codexHome)
	cmd.Dir = d.workspace
	procgroup.Configure(cmd)
	if err := ctx.Err(); err != nil {
		return started{}, err
	}
	client, err := codexapp.Start(cmd, codexapp.Options{Kill: procgroup.Kill})
	if err != nil {
		return started{}, fail(executor.ErrRuntimeStartFailed, "Could not start Codex %s: %v", version, err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Codex process is not under group management; stopping can only end the process itself", "pid", client.Pid(), "err", err)
	}
	return started{rt: &codexProcess{Client: client, binary: bin, version: version, model: snap.ModelID, effort: codexEffort(snap)}}, nil
}

// codexEnv is the environment given to codex: the platform's common environment without Pi's directory variables and
// without OpenAI or Codex variables (passed through, from CLI credentials or from plugins), plus CODEX_HOME pointing at
// the user's Codex home: only the local login is used.
func codexEnv(env []string, codexHome string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PI_CODING_AGENT_DIR" || k == "PI_OFFLINE" {
			continue
		}
		out = append(out, kv)
	}
	return codexcli.WithHome(out, codexHome)
}

// attach hooks up usage and rate limit events.
func (cd codexDriver) attach(p *proc) {
	if cp := codexOf(p); cp != nil {
		cp.SetObserver(codexObserver{e: cd.e, p: p, model: cp.model})
	}
}

// ready opens the connection, gives Codex the Agent's Skill directory and opens the thread: it continues the recorded
// thread, forks the regular thread on a task instance's first start, or starts a new one. A recorded thread that Codex
// no longer has is a missing session, as a missing session file is for Pi.
func (cd codexDriver) ready(ctx context.Context, p *proc, spec startSpec) error {
	e, cp, snap, d := cd.e, codexOf(p), spec.snap, spec.d
	ctx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	defer cancel()
	if _, err := cp.Initialize(ctx, codexClientName, "1"); err != nil {
		return cd.startFailure(p, err)
	}
	if err := cp.SetSkillRoots(ctx, []string{d.codexSkills}); err != nil {
		return cd.startFailure(p, err)
	}
	opts := codexapp.ThreadOptions{Cwd: d.workspace, Model: snap.ModelID, Config: codexMCPConfig(snap.Plugins.MCPServers)}
	if b, err := os.ReadFile(d.prompt); err == nil {
		opts.DeveloperInstructions = string(b)
	}
	switch {
	case spec.sessionFile != "":
		err := cp.ResumeThread(ctx, spec.sessionFile, opts)
		if codexapp.IsThreadNotFound(err) {
			if mErr := e.src.MarkSessionMissing(ctx, snap.AgentID, &spec.runID); mErr != nil {
				return mErr
			}
			return fail(executor.ErrSessionMissing, "The recorded Codex thread %s no longer exists. Confirm reopening the session in the console first.", spec.sessionFile)
		}
		if err != nil {
			return cd.startFailure(p, err)
		}
		cp.threadID, cp.started = spec.sessionFile, true
	case snap.ForkSessionFile != "":
		var base codexBaseline
		b, err := os.ReadFile(snap.ForkSessionFile)
		if err != nil || json.Unmarshal(b, &base) != nil || base.ThreadID == "" {
			return fail(executor.ErrRuntimeStartFailed, "The task's copy of the regular Codex conversation cannot be read.")
		}
		id, err := cp.ForkThread(ctx, base.ThreadID, base.LastTurnID, opts)
		if err != nil {
			return cd.startFailure(p, err)
		}
		cp.threadID = id
	default:
		id, err := cp.StartThread(ctx, opts)
		if err != nil {
			return cd.startFailure(p, err)
		}
		cp.threadID = id
	}
	return nil
}

func (codexDriver) startFailure(p *proc, err error) error {
	cp := codexOf(p)
	if errors.Is(err, runturn.ErrExited) {
		return fail(executor.ErrRuntimeStartFailed, "Codex %s exited while starting. %s", cp.version, strings.TrimSpace(cp.Stderr()))
	}
	return fail(executor.ErrRuntimeStartFailed, "Codex %s did not start: %v", cp.version, err)
}

// codexMCPConfig turns the granted plugins' MCP servers (mcp.json entries) into Codex thread settings
// (mcp_servers.<name>): a command with its arguments and environment, or a URL with its headers.
func codexMCPConfig(servers map[string]json.RawMessage) map[string]any {
	if len(servers) == 0 {
		return nil
	}
	out := map[string]any{}
	for name, raw := range servers {
		var s struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		entry := map[string]any{}
		switch {
		case s.Command != "":
			entry["command"] = s.Command
			if len(s.Args) > 0 {
				entry["args"] = s.Args
			}
			if len(s.Env) > 0 {
				entry["env"] = s.Env
			}
		case s.URL != "":
			entry["url"] = s.URL
			if len(s.Headers) > 0 {
				entry["http_headers"] = s.Headers
			}
		default:
			continue
		}
		out["mcp_servers."+name] = entry
	}
	return out
}

func (codexDriver) beforeTurn(context.Context, *proc, agentconfig.Snapshot) error { return nil }

// afterTurn records the thread once a turn of it has started (Codex saves a thread only then) and words a missing or
// revoked login.
func (cd codexDriver) afterTurn(p *proc, tr runturn.Result) runturn.Result {
	cp := codexOf(p)
	if cp == nil {
		return tr
	}
	if cp.started {
		p.sessMu.Lock()
		first := p.sessionFile != cp.threadID
		p.sessionFile = cp.threadID
		p.sessMu.Unlock()
		if first {
			cd.e.noteCodexThread(p.dirs, cp.threadID, p.log)
		}
	}
	if tr.ErrorCode == executor.ErrRuntimeLoginRequired {
		tr.ErrorMessage = "Codex on this computer is not logged in, or its login was revoked. Run codex login on this computer and try again."
	}
	return tr
}

func (codexDriver) exited() (string, string) {
	return executor.ErrRuntimeExited, "The Codex process exited."
}

func (codexDriver) crashMessage(p *proc, stderrTail string) string {
	version := ""
	if cp := codexOf(p); cp != nil {
		version = cp.version
	}
	return strings.TrimSpace("Codex " + version + " process exited unexpectedly. " + stderrTail)
}

// compact compacts the thread's context (/compact). Codex takes no instructions for it and reports no token counts.
func (codexDriver) compact(ctx context.Context, p *proc, _ string) (executor.CompactionResult, error) {
	cp := codexOf(p)
	r, err := cp.Compact(ctx, cp.threadID)
	if err != nil {
		return executor.CompactionResult{}, err
	}
	switch r.Outcome {
	case runturn.OutcomeCompleted, runturn.OutcomeAborted:
		return executor.CompactionResult{NoCounts: true}, nil
	}
	if r.ErrorCode == executor.ErrRuntimeExited {
		return executor.CompactionResult{}, runturn.ErrExited
	}
	return executor.CompactionResult{}, fail(r.ErrorCode, "%s", r.ErrorMessage)
}

// codexBaseline is what a task instance starts from: the regular thread and its latest turn when the task started.
// The instance forks it on its first start, so turns the regular conversation has after that are not copied.
type codexBaseline struct {
	ThreadID   string `json:"threadId"`
	LastTurnID string `json:"lastTurnId,omitempty"`
}

// forkBaseline records the regular thread and its latest turn for a task fork, asking a short-lived app-server.
func (cd codexDriver) forkBaseline(ctx context.Context, _ uuid.UUID, threadID string, _ int64) ([]byte, error) {
	var last string
	err := cd.e.withCodexApp(ctx, func(c *codexapp.Client) error {
		var err error
		last, err = c.LastTurnID(ctx, threadID)
		return err
	})
	if codexapp.IsThreadNotFound(err) {
		return nil, errors.New("the regular Codex conversation no longer exists")
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the regular Codex conversation: %w", err)
	}
	return json.Marshal(codexBaseline{ThreadID: threadID, LastTurnID: last})
}

// keepsSessions: Codex keeps its threads in its own store; a retired task instance's thread is archived there by purge.
func (codexDriver) keepsSessions() bool { return true }

// codexThreadsFile lists the threads Codex keeps for this Agent (or task instance), so deleting it can archive them.
func codexThreadsFile(d agentDirs) string { return filepath.Join(d.root, "codex-threads.json") }

// noteCodexThread adds a thread to the Agent's list; failures are only logged (the thread is then not archived).
func (e *Executor) noteCodexThread(d agentDirs, threadID string, log interface{ Warn(string, ...any) }) {
	var threads []string
	if b, err := os.ReadFile(codexThreadsFile(d)); err == nil {
		_ = json.Unmarshal(b, &threads)
	}
	if slices.Contains(threads, threadID) {
		return
	}
	b, _ := json.Marshal(append(threads, threadID))
	if err := os.WriteFile(codexThreadsFile(d), b, 0o600); err != nil {
		log.Warn("failed to note the Codex thread for archiving later", "err", err)
	}
}

// purge archives the Codex threads of a deleted Agent or retired task instance (moved to Codex's archived threads, so
// the user can still restore them), using a short-lived app-server. Threads Codex no longer has are skipped; any
// other failure is only logged.
func (cd codexDriver) purge(ctx context.Context, agentID uuid.UUID) {
	e := cd.e
	if e.log == nil {
		return
	}
	d := e.dirs(agentID)
	b, err := os.ReadFile(codexThreadsFile(d))
	if err != nil {
		return
	}
	var threads []string
	if json.Unmarshal(b, &threads) != nil || len(threads) == 0 {
		return
	}
	log := e.log.With("agentId", agentID)
	err = e.withCodexApp(ctx, func(c *codexapp.Client) error {
		for _, id := range threads {
			if err := c.Archive(ctx, id); err != nil && !codexapp.IsThreadNotFound(err) {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Warn("failed to archive the Agent's Codex threads", "err", err)
		return
	}
	_ = os.Remove(codexThreadsFile(d))
}

// withCodexApp runs fn against a short-lived app-server started outside any Agent (in the server's temporary
// directory, with the helper environment).
func (e *Executor) withCodexApp(ctx context.Context, fn func(*codexapp.Client) error) error {
	bin, err := e.codexBinary(ctx)
	if err != nil {
		return err
	}
	if _, err := e.codexVersion(ctx, bin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.Command(bin, "app-server")
	cmd.Env = e.CodexHelperEnv(ctx)
	cmd.Dir = os.TempDir()
	procgroup.Configure(cmd)
	c, err := codexapp.Start(cmd, codexapp.Options{Kill: procgroup.Kill})
	if err != nil {
		return err
	}
	defer func() {
		_ = c.CloseStdin()
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			c.Kill()
			<-c.Done()
		}
	}()
	if _, err := c.Initialize(ctx, codexClientName, "1"); err != nil {
		return err
	}
	return fn(c)
}

// CodexModelList is what the codex on this computer lists for its login.
type CodexModelList struct {
	Models []modelcatalog.CodexModel
	// Cached means Codex could not refresh its list online and answered from its own cache, which may be out of date;
	// LoginRejected means that refresh was rejected because the login is no longer valid (HTTP 401, as for a revoked
	// login). The protocol does not say either: both are read from Codex's log, whose line codex 0.167.0 writes as
	// "failed to refresh available models: unexpected status 401 Unauthorized ...".
	Cached, LoginRejected bool
}

// CodexModels returns the models the codex on this computer lists for its login, for the model catalog.
func (e *Executor) CodexModels(ctx context.Context) (CodexModelList, error) {
	var models []codexapp.Model
	var client *codexapp.Client
	err := e.withCodexApp(ctx, func(c *codexapp.Client) error {
		client = c
		var err error
		models, err = c.ListModels(ctx)
		return err
	})
	if err != nil {
		return CodexModelList{}, err
	}
	out := CodexModelList{Models: make([]modelcatalog.CodexModel, 0, len(models))}
	for _, m := range models {
		out.Models = append(out.Models, modelcatalog.CodexModel{ID: m.ID, Name: m.DisplayName, Efforts: m.Efforts, DefaultEffort: m.DefaultEffort,
			Images: slices.Contains(m.InputModalities, "image"), Hidden: m.Hidden})
	}
	// The app-server has exited by now, so its whole log has been read.
	for line := range strings.Lines(client.Stderr()) {
		if strings.Contains(line, "failed to refresh available models") {
			out.Cached = true
			out.LoginRejected = out.LoginRejected || strings.Contains(line, "401")
		}
	}
	return out, nil
}

// codexObserver passes usage per model call and rate limits to the executor's observer.
type codexObserver struct {
	e     *Executor
	p     *proc
	model string
}

func (o codexObserver) runID(inTurn bool) *uuid.UUID {
	if !inTurn {
		return nil
	}
	return o.p.curRun.Load()
}

// Usage records one model call: Codex's input count includes the cached part, which is recorded as cache reads.
func (o codexObserver) Usage(u codexapp.Usage, inTurn bool) {
	reasoning := u.Reasoning
	o.e.obs.Usage(o.p.agentID, o.runID(inTurn), executor.Usage{Kind: "assistant", Provider: agentruntime.CodexProvider, Model: o.model, ResponseModel: o.model,
		Input: max(u.Input-u.CachedInput, 0), Output: u.Output, CacheRead: u.CachedInput, Reasoning: &reasoning})
}

func (o codexObserver) RateLimit(info map[string]any, inTurn bool) {
	o.e.event(o.p.agentID, o.runID(inTurn), store.EventModelRateLimit, map[string]any{"info": info})
}
