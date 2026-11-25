package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/acpcli"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/acp"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// acpClientName is how the platform introduces itself to an ACP agent (initialize).
const acpClientName = "loop-now"

// acpFlavor is what differs between the Agent Client Protocol runtimes.
type acpFlavor struct {
	runtime string // registry id
	binary  string // executable name on the login shell's PATH
	login   string // the command that logs in, for messages
	// args are the arguments that start the agent on stdio.
	args func(snap agentconfig.Snapshot, d agentDirs) []string
	// modelInSession: the model is chosen with session/set_model; otherwise it is given when the process starts.
	modelInSession bool
	// pluginSkills: the Skills go in a plugin directory given when the process starts; otherwise the platform prompt
	// lists them with where their SKILL.md is.
	pluginSkills bool
	// killOnStop: a stop ends the process (the next message continues the session in a new one); otherwise the prompt
	// is cancelled (session/cancel) and the process stays.
	killOnStop bool
	// emptyLoadIsMissing: loading a session the agent does not have succeeds without history; otherwise it fails.
	emptyLoadIsMissing bool
	// command is set for a local ACP command a person added: its executable is command.Command (a name on the login
	// shell's PATH or an absolute path), its environment adds command.Env, and it has no version to check.
	command *agentruntime.LocalCommand
	// background: the agent goes on with commands after its prompt has returned and reports on them by itself, so a
	// turn waits for them (see acp.Background); holds says which open tools do that.
	background func(kind string, rawInput json.RawMessage) bool
	// writeArgs are the arguments that start the agent to write a text (see writeText): the model and effort when they
	// are given at start, and none of the flags that approve every tool; nil means args (a local command).
	writeArgs func(model, effort string) []string
	// forget removes what the CLI keeps under home for the session sessionID it opened in cwd (its history entry), after
	// a text run; nil when that is not known.
	forget func(home, cwd, sessionID string) error
	// inputCache is whether the input count the agent reports includes the cached part (see usageRecord).
	inputCache acpInputCache
}

// acpInputCache is whether an agent's input token count includes the cached part (cache reads and writes, which it
// reports beside it).
type acpInputCache int

const (
	// cacheByTotal: it does when the input and output alone make the reported total.
	cacheByTotal acpInputCache = iota
	// cacheApart: it never does (Antigravity: Gemini's input count leaves out cache reads).
	cacheApart
	// cacheInside: it always does (Grok: input plus output is its total, with the cache reads inside the input).
	cacheInside
)

// localCommandFlavor runs a local ACP command: its own arguments, the program's default model, Skills listed in the
// platform prompt, a stop by session/cancel (then the executor's kill after the timeout), and a session that loads
// without history counted as missing (an agent replays the history of a session it has).
func localCommandFlavor(rt agentruntime.Runtime) acpFlavor {
	cmd := *rt.Command
	return acpFlavor{runtime: rt.ID, binary: cmd.Command, emptyLoadIsMissing: true, command: &cmd,
		args: func(agentconfig.Snapshot, agentDirs) []string { return slices.Clone(cmd.Args) }}
}

// grokFlavor: `grok agent --always-approve --model M --reasoning-effort E --plugin-dir D stdio`. Grok's cancel is not
// relied on: a stop ends the process.
var grokFlavor = acpFlavor{runtime: agentruntime.Grok, binary: "grok", login: "grok login", pluginSkills: true, killOnStop: true,
	args: func(snap agentconfig.Snapshot, d agentDirs) []string {
		args := []string{"agent", "--always-approve", "--model", snap.ModelID}
		if effort := acpEffort(snap); effort != "" {
			args = append(args, "--reasoning-effort", effort)
		}
		return append(args, "--plugin-dir", d.acpPlugin, "stdio")
	},
	writeArgs: func(model, effort string) []string {
		args := []string{"--permission-mode", "default", "agent", "--model", model}
		if effort != "" {
			args = append(args, "--reasoning-effort", effort)
		}
		return append(args, "stdio")
	},
	forget:     forgetGrokSession,
	inputCache: cacheInside}

// antigravityFlavor: `agy acp --dangerously-skip-permissions`; the model is chosen in the session, by the id that carries
// the thinking level (see acpSessionModel).
var antigravityFlavor = acpFlavor{runtime: agentruntime.Antigravity, binary: "agy", login: "agy acp --login", modelInSession: true, emptyLoadIsMissing: true,
	args: func(agentconfig.Snapshot, agentDirs) []string {
		return []string{"acp", "--dangerously-skip-permissions"}
	},
	background: antigravityBackgroundCommand,
	writeArgs:  func(string, string) []string { return []string{"acp"} },
	forget:     forgetAntigravitySession,
	inputCache: cacheApart}

// sessionIDPattern is what a session id must look like before it names a file or folder to remove.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// forgetGrokSession removes the folder Grok keeps for a working folder (.grok/sessions/<working folder, escaped>): the
// session's own folder and the prompt history, which holds what was sent. The working folder is a text run's own
// (see WriteText), so the whole folder is the run's; anything else is refused, so a mistake can never reach
// .grok/sessions itself or the sessions of another folder.
func forgetGrokSession(home, cwd, sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("unexpected session id %q", sessionID)
	}
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) || filepath.Base(cwd) != textWorkDir || !strings.HasPrefix(filepath.Base(filepath.Dir(cwd)), textDirPrefix) {
		return fmt.Errorf("not a text run's working folder: %q", cwd)
	}
	sessions := filepath.Join(home, ".grok", "sessions")
	name := url.PathEscape(cwd)
	target := filepath.Join(sessions, name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || filepath.Dir(target) != sessions || filepath.Base(target) != name {
		return fmt.Errorf("unexpected Grok session folder %q", name)
	}
	return os.RemoveAll(target)
}

// forgetAntigravitySession removes what agy keeps for a session under .gemini/antigravity-cli: its conversation
// database (conversations/<id>.db and the files beside it), brain/<id> and annotations/<id>.pbtxt.
func forgetAntigravitySession(home, _, sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("unexpected session id %q", sessionID)
	}
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	var errs []error
	for _, p := range []string{
		filepath.Join(dir, "conversations", sessionID+".db"), filepath.Join(dir, "conversations", sessionID+".db-wal"),
		filepath.Join(dir, "conversations", sessionID+".db-shm"), filepath.Join(dir, "conversations", sessionID+".db-journal"),
		filepath.Join(dir, "brain", sessionID), filepath.Join(dir, "annotations", sessionID+".pbtxt"),
	} {
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// antigravityBackgroundCommand reports whether an open tool is a command agy goes on running after the prompt: its
// run_command (kind execute) moves to the background when it outlasts WaitMsBeforeAsync, its end comes with no prompt
// open, and agy then replies by itself. A daemon (IsDaemon) may never end and does not hold the turn.
func antigravityBackgroundCommand(kind string, rawInput json.RawMessage) bool {
	var in struct {
		IsDaemon bool `json:"IsDaemon"`
	}
	_ = json.Unmarshal(rawInput, &in)
	return kind == "execute" && !in.IsDaemon
}

// Waiting for an agent's background commands (acp.Background): besides the background wait (Options.ACPBackgroundWait),
// a turn that only waits for the agent's own reply ends after acpReplyQuiet without a word, and one that waits for
// nothing after acpSettle (agy begins its reply about two seconds after a command ends).
const (
	defaultACPBackgroundWait = 30 * time.Minute
	acpReplyQuiet            = 2 * time.Minute
	acpSettle                = 3 * time.Second
)

// clientOptions are the ACP client's options for this flavor.
func (f acpFlavor) clientOptions(o Options) acp.Options {
	opts := acp.Options{Kill: procgroup.Kill}
	if f.background != nil {
		wait := o.ACPBackgroundWait
		if wait <= 0 {
			wait = defaultACPBackgroundWait
		}
		opts.Background = &acp.Background{Holds: f.background, QuietLimit: wait, ReplyQuietLimit: acpReplyQuiet, Settle: acpSettle}
	}
	return opts
}

func acpFlavorOf(runtime string) (acpFlavor, bool) {
	for _, f := range []acpFlavor{grokFlavor, antigravityFlavor} {
		if f.runtime == runtime {
			return f, true
		}
	}
	return acpFlavor{}, false
}

// acpSessionModel is the model id chosen in the session (Antigravity): agy lists a model once per thinking level, so
// the model runs by the id for the Agent's level (gemini-4-neon at xhigh is gemini-4-neon-xhigh). An id recorded before
// the levels were merged names its level already and comes with off or no level; it runs as it is.
func acpSessionModel(snap agentconfig.Snapshot) string {
	if snap.ThinkingLevel == nil {
		return snap.ModelID
	}
	return modelcatalog.LevelModelID(snap.ModelID, *snap.ThinkingLevel)
}

// acpEffort is the reasoning effort for the Agent's thinking level (the runtimes name them the same way); off or none
// leaves the model's default.
func acpEffort(snap agentconfig.Snapshot) string {
	if snap.ThinkingLevel == nil || *snap.ThinkingLevel == "off" {
		return ""
	}
	return *snap.ThinkingLevel
}

// acpDriver runs an Agent Client Protocol agent installed on the user's computer (Grok, Antigravity): an unmodified CLI
// found on the login shell's PATH, with this computer's login (the real HOME) and no API key. One process holds the
// Agent's one session; the session id is the session reference.
type acpDriver struct {
	e *Executor
	f acpFlavor
}

// acpProcess is an ACP agent process with the session it runs.
type acpProcess struct {
	*acp.Client
	f               acpFlavor
	binary, version string
	sessionID       string
	// started reports whether a prompt of this session has run (the agent then has the session saved).
	started bool
	// instructions is the platform prompt block that goes with the next message (empty when the session has it);
	// instructionsSum is its fingerprint, recorded in instructionsFile once a message carried it.
	instructions, instructionsSum, instructionsFile string
	// handoff is the regular conversation a task instance's new session starts with (sent with the first message);
	// handoffSent is set once a message carried it, until afterTurn records it.
	handoff     *executor.TaskStartHandoff
	handoffSent bool
	exiting     atomic.Bool
}

func acpOf(p *proc) *acpProcess {
	ap, _ := p.rt.(*acpProcess)
	return ap
}

// Prompt sends one message to the session, with the platform prompt block first when the session does not have it.
func (ap *acpProcess) Prompt(ctx context.Context, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error) {
	if ap.handoff != nil {
		text = ap.handoff.Text + "\n\n" + text
	}
	if ap.instructions != "" {
		text = ap.instructions + "\n\n" + text
	}
	r, err := ap.Client.Prompt(ctx, ap.sessionID, text, sink, images...)
	if err != nil || (r.Outcome == runturn.OutcomeFailed && r.Text == "") {
		return r, err
	}
	ap.started = true
	if ap.handoff != nil {
		ap.handoffSent = true
	}
	if ap.instructions != "" {
		b, _ := json.Marshal(acpInstructionsRecord{Session: ap.sessionID, Sum: ap.instructionsSum})
		if os.WriteFile(ap.instructionsFile, b, 0o600) == nil {
			ap.instructions = ""
		}
	}
	return r, nil
}

// Abort stops the prompt in progress: Grok's process is ended (after asking it to cancel, so the prompt ends as
// stopped), Antigravity's prompt is cancelled.
func (ap *acpProcess) Abort(context.Context) error {
	if !ap.f.killOnStop {
		return ap.Cancel()
	}
	if !ap.InPrompt() {
		return nil // not sent yet; the executor asks again
	}
	ap.exiting.Store(true)
	_ = ap.Cancel()
	ap.Kill()
	return nil
}

// Exiting reports whether the process is ending after a stop (see retiringProcess).
func (ap *acpProcess) Exiting() bool { return ap.exiting.Load() }

// acpInstructionsRecord is which platform prompt (by fingerprint) a session already has.
type acpInstructionsRecord struct {
	Session string `json:"session"`
	Sum     string `json:"sum"`
}

func (ad acpDriver) name() string    { return runtimeName(ad.f.runtime) }
func (ad acpDriver) runtime() string { return ad.f.runtime }

// identity: the model and effort are given when the process starts (Grok) or the session opens (Antigravity); a local
// command restarts when the command changes.
func (ad acpDriver) identity(snap agentconfig.Snapshot) string {
	id := snap.ModelID + "\x00" + acpEffort(snap)
	if c := ad.f.command; c != nil {
		b, _ := json.Marshal(c)
		sum := sha256.Sum256(b)
		id += "\x00" + hex.EncodeToString(sum[:])
	}
	return id
}

func (acpDriver) refresh(agentconfig.Snapshot, agentDirs) error { return nil }

// syncAssets puts the Skills (loopctl, the Agent's Skill copies, the granted plugins' Skills) where the runtime finds
// them: Grok's plugin directory, or a Skill directory the platform prompt lists for Antigravity. MCP servers go with
// the session.
func (ad acpDriver) syncAssets(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error) {
	if ad.f.pluginSkills {
		return nil, ad.e.writeSkillPlugin(ctx, snap, d.acpPlugin)
	}
	if err := os.RemoveAll(d.acpSkills); err != nil {
		return nil, err
	}
	return nil, ad.e.syncSkillDir(ctx, snap, d.acpSkills)
}

func (acpDriver) clearAssets(d agentDirs) error {
	if err := os.RemoveAll(d.acpPlugin); err != nil {
		return err
	}
	return os.RemoveAll(d.acpSkills)
}

// modelCredentials: the runtime uses its own local login; the platform writes no model credentials.
func (acpDriver) modelCredentials(agentconfig.Snapshot, agentDirs, string) error { return nil }

// sessionSaved: a session id can only be checked by loading it; ready reports a missing session.
func (acpDriver) sessionSaved(string) bool { return true }

// keepsSessions: the runtime keeps its sessions itself, under the real HOME.
func (acpDriver) keepsSessions() bool { return true }

// acpBinary finds the runtime's executable: tests and explicit configuration use Options.GrokBin or AntigravityBin;
// otherwise it is looked up on the PATH of the user's login shell.
func (e *Executor) acpBinary(ctx context.Context, f acpFlavor) (string, error) {
	explicit := e.o.GrokBin
	switch {
	case f.command != nil && filepath.IsAbs(f.binary):
		explicit = f.binary
	case f.command != nil:
		explicit = ""
	case f.runtime == agentruntime.Antigravity:
		explicit = e.o.AntigravityBin
	}
	if explicit != "" {
		if info, err := os.Stat(explicit); err != nil || !info.Mode().IsRegular() {
			return "", acpcli.ErrNotFound
		}
		return explicit, nil
	}
	var dirs []string
	if e.o.UserPath != nil {
		dirs = e.o.UserPath(ctx)
	}
	return acpcli.Find(f.binary, dirs)
}

// ACPHelperEnv is the environment for running grok or agy outside an Agent (--version, the login state, the model
// list): the real HOME, USER, the base part of the run PATH plus the PATH of the user's login shell and the passed-through
// variables, without API keys. GET /runtimes uses it too; nil when the real home cannot be read.
func (e *Executor) ACPHelperEnv(ctx context.Context) []string {
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
	return acpcli.Env(acpcli.EnvOptions{Home: home, User: e.o.Username, Path: append(filepath.SplitList(e.o.ExecBasePath), dirs...), Extra: extra})
}

// acpVersion reads the version of bin and checks it is supported.
func (ad acpDriver) acpVersion(ctx context.Context, bin string) (string, error) {
	version, err := acpcli.Version(ctx, bin, ad.e.ACPHelperEnv(ctx))
	if err != nil {
		return "", fail(executor.ErrRuntimeUnavailable, "Could not read the version of %s on this computer (%s): %v", ad.name(), bin, err)
	}
	if rt, _ := agentruntime.ByID(ad.f.runtime); !acpcli.AtLeast(version, rt.MinVersion) {
		return version, fail(executor.ErrRuntimeUnavailable, "%s %s on this computer is older than %s, the oldest version Loop Now supports. Update it and try again.", ad.name(), version, rt.MinVersion)
	}
	return version, nil
}

// start finds the CLI, checks its version and starts the agent in the Agent's workspace.
func (ad acpDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := ad.e, spec.snap, spec.d
	bin, err := e.acpBinary(ctx, ad.f)
	if err != nil {
		return started{}, fail(executor.ErrRuntimeUnavailable, "%s was not found on this computer: %s is not on the login shell's PATH. Install it on this computer and log in first.", ad.name(), ad.f.binary)
	}
	if home, err := e.o.RealHome(); err != nil || !filepath.IsAbs(home) {
		return started{}, fail(executor.ErrInternal, "Could not read the server user's home directory (%s needs this computer's login).", ad.name())
	}
	version := ""
	if ad.f.command == nil {
		if version, err = ad.acpVersion(ctx, bin); err != nil {
			return started{}, err
		}
	}
	cmd := exec.Command(bin, ad.f.args(snap, d)...)
	cmd.Env = acpEnv(spec.env)
	if c := ad.f.command; c != nil {
		// The environment a person gave the command is added as is (it may hold a key the program needs).
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			cmd.Env = append(cmd.Env, k+"="+c.Env[k])
		}
	}
	cmd.Dir = d.workspace
	procgroup.Configure(cmd)
	if err := ctx.Err(); err != nil {
		return started{}, err
	}
	client, err := acp.Start(cmd, ad.f.clientOptions(e.o))
	if err != nil {
		return started{}, fail(executor.ErrRuntimeStartFailed, "Could not start %s %s: %v", ad.name(), version, err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn(ad.name()+" process is not under group management; stopping can only end the process itself", "pid", client.Pid(), "err", err)
	}
	return started{rt: &acpProcess{Client: client, f: ad.f, binary: bin, version: version, instructionsFile: d.acpInstructions}}, nil
}

// acpEnv is the environment given to the agent: the platform's common environment without Pi's directory variables and
// without API keys, so only this computer's login is used.
func acpEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range acpcli.StripEnv(env) {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PI_CODING_AGENT_DIR" || k == "PI_OFFLINE" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// attach passes on the usage the agent reports for each prompt.
func (ad acpDriver) attach(p *proc) {
	if ap := acpOf(p); ap != nil {
		ap.SetObserver(acpObserver{e: ad.e, p: p, f: ad.f})
	}
}

// acpObserver records the usage an ACP agent reports with each prompt's result as one model call of the turn. Only
// what the agent sends is used: Antigravity's own replies after its background commands end with usage_update, which
// has the context's size and no token counts, so they are not counted.
type acpObserver struct {
	e *Executor
	p *proc
	f acpFlavor
}

func (o acpObserver) Usage(u acp.Usage) {
	o.e.obs.Usage(o.p.agentID, o.p.curRun.Load(), o.f.usageRecord(u, o.p.provider, o.p.model))
}

// usageRecord is the usage record for what an ACP agent reports for a prompt: the input leaves out the cached part,
// which is recorded as cache reads and writes (see acpInputCache); reasoning is part of output; the response model is
// the model the agent says it used, else the Agent's model.
func (f acpFlavor) usageRecord(u acp.Usage, provider, model string) executor.Usage {
	cache := u.CacheRead + u.CacheWrite
	inside := f.inputCache == cacheInside ||
		f.inputCache == cacheByTotal && cache > 0 && u.Total != nil && *u.Total == u.Input+u.Output
	input := u.Input
	if inside {
		input = max(u.Input-cache, 0)
	}
	var reasoning *int64
	if u.Thought != nil {
		r := min(*u.Thought, u.Output)
		reasoning = &r
	}
	response := u.Model
	if response == "" {
		response = model
	}
	return executor.Usage{Kind: "assistant", Provider: provider, Model: model, ResponseModel: response, StopReason: u.StopReason,
		Input: input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: reasoning}
}

// ready opens the connection and the session: it loads the recorded session (its replayed history is not passed on) or
// opens a new one, chooses the model when the runtime takes it in the session, and decides whether the platform prompt
// goes with the next message. A recorded session the agent no longer has is a missing session.
func (ad acpDriver) ready(ctx context.Context, p *proc, spec startSpec) error {
	e, ap, snap, d := ad.e, acpOf(p), spec.snap, spec.d
	ctx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	defer cancel()
	agent, err := ap.Initialize(ctx, acpClientName, "1")
	if err != nil {
		return ad.startFailure(p, err)
	}
	mcp := acpMCPServers(snap.Plugins.MCPServers)
	if spec.sessionFile != "" {
		lost := !agent.LoadSession
		if !lost {
			s, err := ap.LoadSession(ctx, spec.sessionFile, d.workspace, mcp)
			lost = acpSessionNotFound(err) || (err == nil && ad.f.emptyLoadIsMissing && s.Replayed == 0)
			if err != nil && !lost {
				return ad.startFailure(p, err)
			}
		}
		if lost {
			if mErr := e.src.MarkSessionMissing(ctx, snap.AgentID, &spec.runID); mErr != nil {
				return mErr
			}
			return fail(executor.ErrSessionMissing, "The recorded %s session %s no longer exists; the next message starts a new session with the saved conversation.", ad.name(), spec.sessionFile)
		}
		ap.sessionID, ap.started = spec.sessionFile, true
	} else {
		s, err := ap.NewSession(ctx, d.workspace, mcp)
		if err != nil {
			return ad.startFailure(p, err)
		}
		ap.sessionID = s.ID
		// A task instance's first session starts with the regular conversation (the runtime cannot copy the session).
		if snap.ForkSessionFile != "" {
			var h executor.TaskStartHandoff
			b, err := os.ReadFile(snap.ForkSessionFile)
			if err != nil || json.Unmarshal(b, &h) != nil || h.Text == "" {
				return fail(executor.ErrRuntimeStartFailed, "The task's copy of the regular conversation cannot be read.")
			}
			ap.handoff = &h
		}
	}
	if ad.f.modelInSession && snap.ModelID != modelcatalog.LocalCommandDefaultModel {
		if err := ap.SetModel(ctx, ap.sessionID, acpSessionModel(snap)); err != nil {
			return ad.startFailure(p, err)
		}
	}
	text, sum := ad.instructions(d)
	var rec acpInstructionsRecord
	if b, err := os.ReadFile(d.acpInstructions); err == nil {
		_ = json.Unmarshal(b, &rec)
	}
	if rec.Session != ap.sessionID || rec.Sum != sum {
		ap.instructions, ap.instructionsSum = text, sum
	}
	return nil
}

// acpSessionNotFound reports whether a session/load failure says the session does not exist (Grok: "Path not found.",
// data code FS_NOT_FOUND).
func acpSessionNotFound(err error) bool {
	var rpc *acp.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	return strings.Contains(string(rpc.Data), "NOT_FOUND") || strings.Contains(strings.ToLower(rpc.Message), "not found")
}

func (ad acpDriver) startFailure(p *proc, err error) error {
	ap := acpOf(p)
	switch {
	case acp.IsAuthRequired(err):
		return fail(executor.ErrRuntimeLoginRequired, "%s", ad.loginMessage())
	case errors.Is(err, runturn.ErrExited):
		return fail(executor.ErrRuntimeStartFailed, "%s %s exited while starting. %s", ad.name(), ap.version, strings.TrimSpace(ap.Stderr()))
	}
	return fail(executor.ErrRuntimeStartFailed, "%s %s did not start: %v", ad.name(), ap.version, err)
}

func (ad acpDriver) loginMessage() string {
	if ad.f.login == "" {
		return fmt.Sprintf("%s on this computer is not logged in. Log in with the program itself on this computer and try again.", ad.name())
	}
	return fmt.Sprintf("%s on this computer is not logged in. Run %s on this computer and try again.", ad.name(), ad.f.login)
}

// instructions is the block that gives the session the platform prompt (ACP has no system prompt of its own) and its
// fingerprint. For a runtime without a Skill directory it also lists the Skills and where their SKILL.md is.
func (ad acpDriver) instructions(d agentDirs) (string, string) {
	prompt, _ := os.ReadFile(d.prompt)
	var b strings.Builder
	b.WriteString("<loop-now-instructions>\nThese are your standing instructions from Loop Now, the platform that runs you. Follow them for the rest of this conversation; when they change, the new ones come with a later message.\n\n")
	b.WriteString(strings.TrimSpace(string(prompt)))
	if !ad.f.pluginSkills {
		b.WriteString(skillGuide(d.acpSkills))
	}
	b.WriteString("\n</loop-now-instructions>")
	sum := sha256.Sum256([]byte(b.String()))
	return b.String(), hex.EncodeToString(sum[:])
}

// skillGuide lists the Skills in dir (each a folder with a SKILL.md) with their description and where to read them.
func skillGuide(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Skills\n\nEach Skill is a folder with a SKILL.md that explains how to do one kind of work. Before you do that kind of work, read its SKILL.md and follow it.\n")
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if !entry.IsDir() || err != nil {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s (%s)\n", entry.Name(), skillDescription(string(data)), path)
	}
	return strings.TrimRight(b.String(), "\n")
}

// skillDescription is the description in a SKILL.md's front matter; empty when it has none.
func skillDescription(skill string) string {
	front, ok := strings.CutPrefix(strings.TrimSpace(skill), "---")
	if !ok {
		return ""
	}
	front, _, _ = strings.Cut(front, "\n---")
	for line := range strings.Lines(front) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "description:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// acpMCPServers turns the granted plugins' MCP servers (mcp.json entries) into the stdio servers a session takes; a
// server reached by URL is left out (not every agent takes those).
func acpMCPServers(servers map[string]json.RawMessage) []acp.MCPServer {
	var out []acp.MCPServer
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range servers {
			if !yield(k) {
				return
			}
		}
	}) {
		var s struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}
		if json.Unmarshal(servers[name], &s) != nil || s.Command == "" {
			continue
		}
		out = append(out, acp.MCPServer{Name: name, Command: s.Command, Args: s.Args, Env: s.Env})
	}
	return out
}

func (acpDriver) beforeTurn(context.Context, *proc, agentconfig.Snapshot) error { return nil }

// afterTurn records the session once a prompt of it has run, records the handoff a task instance's first message
// carried (context.handoff, reason task_start), and words a missing login.
func (ad acpDriver) afterTurn(p *proc, tr runturn.Result) runturn.Result {
	ap := acpOf(p)
	if ap == nil {
		return tr
	}
	if h := ap.handoff; h != nil && ap.handoffSent {
		ap.handoff, ap.handoffSent = nil, false
		ad.e.event(p.agentID, p.curRun.Load(), store.EventContextHandoff, map[string]any{"id": uuid.Must(uuid.NewV7()).String(), "reason": store.HandoffTaskStart,
			"fromRuntime": h.FromRuntime, "toRuntime": h.ToRuntime, "includedMessages": h.IncludedMessages, "omittedMessages": h.OmittedMessages,
			"images": h.Images, "bytes": h.Bytes, "budgetBytes": h.BudgetBytes})
	}
	if ap.started {
		p.sessMu.Lock()
		p.sessionFile = ap.sessionID
		p.sessMu.Unlock()
	}
	switch {
	case tr.ErrorCode == executor.ErrRuntimeLoginRequired:
		tr.ErrorMessage = ad.loginMessage()
		ad.e.noteACPLogin(ad.f.runtime, acpcli.LoggedOut)
	case tr.Outcome == runturn.OutcomeCompleted:
		ad.e.noteACPLogin(ad.f.runtime, acpcli.LoggedIn)
	}
	return tr
}

func (ad acpDriver) exited() (string, string) {
	return executor.ErrRuntimeExited, "The " + ad.name() + " process exited."
}

func (ad acpDriver) crashMessage(p *proc, stderrTail string) string {
	version := ""
	if ap := acpOf(p); ap != nil {
		version = ap.version
	}
	return strings.TrimSpace(ad.name() + " " + version + " process exited unexpectedly. " + stderrTail)
}

// ACPLoginState is the last login conclusion for an ACP runtime (acpcli.LoggedIn, LoggedOut or Unknown), kept by the
// background read of its model list (ACPModels) and corrected by turns; empty when there is none yet. The runtime
// status reads it instead of running grok models or agy models, which go online.
func (e *Executor) ACPLoginState(runtime string) string {
	v, _ := e.acpLogins.Load(runtime)
	s, _ := v.(string)
	return s
}

// noteACPLogin keeps a login conclusion; an unknown one does not replace a conclusion already kept.
func (e *Executor) noteACPLogin(runtime, state string) {
	if state == acpcli.Unknown {
		e.acpLogins.LoadOrStore(runtime, state)
		return
	}
	e.acpLogins.Store(runtime, state)
}

// ACPModels returns the models the runtime's CLI on this computer lists for its login, for the model catalog: Grok's
// from a short-lived agent's initialize (with each model's reasoning efforts), Antigravity's from agy models (which
// lists a model once per thinking level; see modelcatalog.MergeLevelModels). It also keeps the runtime's login
// conclusion (ACPLoginState): Antigravity's from the same agy models, Grok's from grok models run alongside.
func (e *Executor) ACPModels(ctx context.Context, runtime string) ([]modelcatalog.LocalModel, error) {
	f, ok := acpFlavorOf(runtime)
	if !ok {
		return nil, fmt.Errorf("%w: %s", executor.ErrUnsupportedRuntime, runtimeName(runtime))
	}
	bin, err := e.acpBinary(ctx, f)
	if err != nil {
		return nil, err
	}
	env := e.ACPHelperEnv(ctx)
	if env == nil {
		return nil, errors.New("the real home directory cannot be read")
	}
	if f.runtime == agentruntime.Antigravity {
		list, err := acpcli.AntigravityModels(ctx, bin, env)
		e.noteACPLogin(f.runtime, acpcli.AntigravityLoginOf(list, err))
		if err != nil {
			return nil, err
		}
		out := make([]modelcatalog.LocalModel, 0, len(list))
		for _, m := range list {
			out = append(out, modelcatalog.LocalModel{ID: m.ID, Name: m.Name, Images: true})
		}
		return modelcatalog.MergeLevelModels(out), nil
	}
	login := make(chan string, 1)
	go func() { login <- acpcli.GrokLogin(ctx, bin, env) }()
	models, err := e.grokModels(ctx, bin, env)
	e.noteACPLogin(f.runtime, <-login)
	return models, err
}

// grokModels reads the models Grok lists in its initialize (_meta.modelState) from a short-lived agent.
func (e *Executor) grokModels(ctx context.Context, bin string, env []string) ([]modelcatalog.LocalModel, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.Command(bin, "agent", "stdio")
	cmd.Env = env
	cmd.Dir = os.TempDir()
	procgroup.Configure(cmd)
	c, err := acp.Start(cmd, acp.Options{Kill: procgroup.Kill})
	if err != nil {
		return nil, err
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
	agent, err := c.Initialize(ctx, acpClientName, "1")
	if err != nil {
		return nil, err
	}
	var meta struct {
		ModelState struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
				Name    string `json:"name"`
				Meta    struct {
					ReasoningEfforts []struct {
						ID      string `json:"id"`
						Default bool   `json:"default"`
					} `json:"reasoningEfforts"`
				} `json:"_meta"`
			} `json:"availableModels"`
		} `json:"modelState"`
	}
	if err := json.Unmarshal(agent.Meta, &meta); err != nil {
		return nil, fmt.Errorf("Grok's initialize lists no models: %w", err)
	}
	var out []modelcatalog.LocalModel
	for _, m := range meta.ModelState.AvailableModels {
		model := modelcatalog.LocalModel{ID: m.ModelID, Name: m.Name, Images: agent.Images}
		for _, effort := range m.Meta.ReasoningEfforts {
			model.Efforts = append(model.Efforts, effort.ID)
			if effort.Default {
				model.DefaultEffort = effort.ID
			}
		}
		out = append(out, model)
	}
	return out, nil
}

// acpTextLimit is the most text a text run collects before it is stopped.
const acpTextLimit = 1 << 20

// writeText runs the agent once in dir with this computer's login: started without the flags that approve every tool,
// a new session with no MCP servers, the model and level chosen as for an Agent, one prompt (the instructions first),
// every permission request turned down. A tool it starts (other than thinking or fetching a web page) ends the run at
// once, as a failure. Afterwards the process ends and the session's record in the CLI's own history is removed.
func (ad acpDriver) writeText(ctx context.Context, dir, _ string, req executor.TextRequest) (string, error) {
	e, f := ad.e, ad.f
	bin, err := e.acpBinary(ctx, f)
	if err != nil {
		return "", textUnavailable("%s was not found on this computer: %s is not on the login shell's PATH. Install it and log in first.", ad.name(), f.binary)
	}
	home, err := e.o.RealHome()
	if err != nil || !filepath.IsAbs(home) {
		return "", textUnavailable("Could not read the server user's home directory (%s needs this computer's login).", ad.name())
	}
	if f.command == nil {
		if _, err := ad.acpVersion(ctx, bin); err != nil {
			var fl *failure
			if errors.As(err, &fl) {
				return "", textUnavailable("%s", fl.msg)
			}
			return "", textUnavailable("%v", err)
		}
	}
	effort := ""
	if req.ThinkingLevel != nil && *req.ThinkingLevel != "off" {
		effort = *req.ThinkingLevel
	}
	args := f.args(agentconfig.Snapshot{}, agentDirs{})
	if f.writeArgs != nil {
		args = f.writeArgs(req.Model, effort)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = acpEnv(e.ACPHelperEnv(ctx))
	if c := f.command; c != nil {
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			cmd.Env = append(cmd.Env, k+"="+c.Env[k])
		}
	}
	cmd.Dir = dir
	procgroup.Configure(cmd)
	client, err := acp.Start(cmd, acp.Options{Kill: procgroup.Kill, RejectPermissions: true})
	if err != nil {
		return "", textFailed("Could not start %s: %v", ad.name(), err)
	}
	_ = procgroup.Started(cmd)
	sessionID := ""
	defer func() {
		_ = client.CloseStdin()
		client.Kill()
		select {
		case <-client.Done():
		case <-time.After(5 * time.Second):
		}
		if sessionID != "" && f.forget != nil {
			if err := f.forget(home, dir, sessionID); err != nil && e.log != nil {
				e.log.Warn("could not remove the session a text run left in the CLI's history", "runtime", f.runtime, "err", err)
			}
		}
	}()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			client.Kill()
		case <-stopped:
		}
	}()
	if _, err := client.Initialize(ctx, acpClientName, "1"); err != nil {
		return "", ad.textStartFailure(ctx, client, err)
	}
	s, err := client.NewSession(ctx, dir, nil)
	if err != nil {
		return "", ad.textStartFailure(ctx, client, err)
	}
	sessionID = s.ID
	if f.modelInSession && req.Model != modelcatalog.LocalCommandDefaultModel {
		model := req.Model
		if req.ThinkingLevel != nil {
			model = modelcatalog.LevelModelID(model, *req.ThinkingLevel)
		}
		if err := client.SetModel(ctx, s.ID, model); err != nil {
			return "", ad.textStartFailure(ctx, client, err)
		}
	}
	sink := &textSink{kill: client.Kill}
	r, err := client.Prompt(ctx, s.ID, textPrompt(req), sink)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	text, tool, over := sink.result()
	switch {
	case tool != "":
		return "", textFailed("%s tried to use a tool while writing (%s), so it was stopped. Try again, or choose another writer.", ad.name(), tool)
	case over:
		return "", textFailed("%s wrote more than Loop Now takes for a draft, so it was stopped.", ad.name())
	case err != nil:
		return "", textFailed("%s could not write it: %v", ad.name(), err)
	case r.Outcome == runturn.OutcomeFailed && r.ErrorCode == executor.ErrRuntimeLoginRequired:
		return "", textFailed("%s", ad.loginMessage())
	case r.Outcome == runturn.OutcomeFailed:
		reason := r.ErrorMessage
		if reason == "" {
			reason = lastLines(client.Stderr(), 3)
		}
		return "", textFailed("%s could not write it: %s", ad.name(), reason)
	case r.Outcome == runturn.OutcomeAborted:
		return "", textFailed("%s stopped before it finished writing.", ad.name())
	}
	return text, nil
}

// textStartFailure is a failure before the prompt: a login the agent asks for, or the process's own words.
func (ad acpDriver) textStartFailure(ctx context.Context, client *acp.Client, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if acp.IsAuthRequired(err) {
		return textFailed("%s", ad.loginMessage())
	}
	if errors.Is(err, runturn.ErrExited) {
		return textFailed("%s exited while starting. %s", ad.name(), lastLines(client.Stderr(), 3))
	}
	return textFailed("%s did not start: %v", ad.name(), err)
}

// textSink collects a text run's reply; a tool other than thinking or fetching a web page, or too long a reply, ends
// the process at once.
type textSink struct {
	kill func()
	mu   sync.Mutex
	text strings.Builder
	tool string
	over bool
}

func (s *textSink) TextDelta(delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.text.Len()+len(delta) > acpTextLimit {
		if !s.over {
			s.over = true
			go s.kill()
		}
		return
	}
	s.text.WriteString(delta)
}

func (s *textSink) ToolStarted(_, name string) {
	if name == "think" || name == "fetch" {
		return
	}
	s.mu.Lock()
	first := s.tool == ""
	if first {
		s.tool = name
	}
	s.mu.Unlock()
	if first {
		go s.kill()
	}
}

func (s *textSink) ToolEnded(string, string, bool) {}

func (s *textSink) result() (text, tool string, over bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String(), s.tool, s.over
}
