package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	authhelper "loop-now/auth-helper"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/preflight"
)

// piDriver is the bundled Pi runtime: a long-running process in RPC mode, with model credentials provided through the
// Agent's Pi directory (auth.json, models.json) and the auth refresh channel.
type piDriver struct{ e *Executor }

func (piDriver) name() string { return runtimeName(agentruntime.Pi) }

func (piDriver) runtime() string { return agentruntime.Pi }

func (piDriver) identity(agentconfig.Snapshot) string { return "" }

// piClient is the Pi RPC client of a Pi process.
func piClient(p *proc) *pirpc.Client {
	c, _ := p.rt.(*pirpc.Client)
	return c
}

// piMCPFile is the file Pi's built-in MCP extension reads the plugin MCP servers from.
func piMCPFile(d agentDirs) string { return filepath.Join(d.pi, "mcp.json") }

// syncAssets rebuilds the Agent's Skill copies and loopctl in its skills directory (passed with --skill at start) and
// writes the plugin MCP servers into mcp.json in the Agent's Pi directory. Plugin Skills and extensions are passed as
// arguments at start (pluginArgs).
func (pd piDriver) syncAssets(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error) {
	dirs, err := pd.e.syncSkills(ctx, snap, d.skills)
	if err != nil {
		return nil, err
	}
	return dirs, writeMCPConfig(piMCPFile(d), snap.Plugins)
}

func (piDriver) clearAssets(d agentDirs) error {
	if err := os.RemoveAll(d.skills); err != nil {
		return err
	}
	return removeIfExists(piMCPFile(d))
}

// modelCredentials writes Pi's model credentials: a plugin provider's subscription account is linked to the account's
// state directory, any other credential goes into auth.json; then models.json.
func (pd piDriver) modelCredentials(snap agentconfig.Snapshot, d agentDirs, loginHome string) error {
	if snap.ProviderState != nil {
		if err := pd.e.linkProviderState(snap, d, loginHome); err != nil {
			return err
		}
	} else if err := seedModelAuth(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "Could not write the model credential cache.")
	}
	if err := syncModelsJSON(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "Could not write the model configuration cache.")
	}
	return nil
}

// refresh rewrites Pi's model credentials and the models.json cache. For a plugin provider's subscription account,
// auth.json is a link to the account's state directory (created by prepare; the process identity includes
// stateIdentity, so on reuse it is necessarily still the same one) and must not be rewritten like an ordinary
// credential, or the link would be replaced by a file or the write would simply fail.
func (piDriver) refresh(snap agentconfig.Snapshot, d agentDirs) error {
	if snap.ProviderState == nil {
		if err := seedModelAuth(snap, d); err != nil {
			return fail(executor.ErrCredentialUnavailable, "Could not rebuild the model credential cache.")
		}
	}
	if err := syncModelsJSON(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "Could not rebuild the model configuration cache.")
	}
	return nil
}

// start builds Pi's arguments (built-in extensions, auth refresh channel, plugins) and starts it in RPC mode.
func (pd piDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := pd.e, spec.snap, spec.d
	env := spec.env
	args := piArgs(d, snap, spec.prep.skillDirs, spec.sessionFile)
	if snap.ServiceTier != nil {
		resolved, tierErr := agentconfig.ResolveServiceTier(snap.Provider, snap.ServiceTier)
		if tierErr != nil || resolved == nil || !authhelper.ValidateServiceTierEntry(e.o.CodexServiceTierExtension) {
			return started{}, fail(executor.ErrPiStartFailed, "The Codex speed setting or its built-in extension is not available.")
		}
		args = append(args, "-e", e.o.CodexServiceTierExtension)
		env = append(env, "LOOP_NOW_CODEX_SERVICE_TIER="+*resolved)
	}
	var bridge *modelauth.RuntimeBridge
	if snap.ModelCredential.Kind() == "oauth" {
		if snap.ModelBinding == nil || !modelauth.ValidateEntry(e.o.AuthExtension) {
			return started{}, fail(executor.ErrCredentialUnavailable, "The task authorization refresh channel is not configured.")
		}
		var err error
		bridge, err = modelauth.NewRuntimeBridge(*snap.ModelBinding, e.o.AuthResolver, e.o.AuthIPCTimeout)
		if err != nil {
			return started{}, fail(executor.ErrCredentialUnavailable, "The task authorization refresh channel is not available.")
		}
		args = append(args, "-e", e.o.AuthExtension)
		ipcTimeout := e.o.AuthIPCTimeout
		if ipcTimeout <= 0 {
			ipcTimeout = 30 * time.Second
		}
		env = append(slices.Clone(env), "LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS="+strconv.FormatInt(max(int64(1), ipcTimeout.Milliseconds()), 10))
	}
	closeBridge := func() {
		if bridge != nil {
			bridge.Close()
		}
	}
	if snap.ModelCredential.NeedsLegacyAnthropic() {
		if snap.Provider != "anthropic" || !modelauth.ValidateEntry(e.o.LegacyAuthExtension) || !filepath.IsAbs(e.o.PiPackageRoot) || !filepath.IsAbs(e.o.PiAIDir) {
			closeBridge()
			return started{}, fail(executor.ErrCredentialUnavailable, "The legacy authentication adapter is not configured.")
		}
		args = append(args, "-e", e.o.LegacyAuthExtension)
		env = append(slices.Clone(env), "LOOP_NOW_PI_PACKAGE_ROOT="+e.o.PiPackageRoot, "LOOP_NOW_PI_AI_DIR="+e.o.PiAIDir)
	}
	args = append(args, pluginArgs(snap.Plugins)...)
	cmd := preflight.PiCommand(e.o.NodeBin, e.o.PiBin, args...)
	if bridge != nil {
		cmd.ExtraFiles = bridge.ChildFiles()
	}
	cmd.Env = env
	cmd.Dir = d.workspace
	procgroup.Configure(cmd)
	if err := ctx.Err(); err != nil {
		closeBridge()
		return started{}, err
	}
	client, err := pirpc.Start(cmd)
	if err != nil {
		closeBridge()
		return started{}, fail(executor.ErrPiStartFailed, "Could not start Pi: %v", err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Pi process is not under group management; stopping can only end the process itself", "pid", client.Pid(), "err", err)
	}
	if bridge != nil {
		bridge.ChildStarted()
	}
	return started{rt: client, authBridge: bridge}, nil
}

// attach hooks up the usage and event observers, and extension dialogs.
func (pd piDriver) attach(p *proc) {
	e, client := pd.e, piClient(p)
	client.SetObserver(procObserver{e: e, p: p})
	if e.o.Dialogs != nil {
		client.SetDialogs(func(ctx context.Context, req runturn.DialogRequest) runturn.DialogResponse {
			return e.dialog(ctx, p, req)
		})
	}
}

// ready reads Pi's state (the session file).
func (pd piDriver) ready(ctx context.Context, p *proc, _ startSpec) error {
	e := pd.e
	stateCtx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	st, err := piClient(p).GetState(stateCtx)
	cancel()
	if err != nil {
		return fail(executor.ErrPiStartFailed, "Pi started but did not become ready: %v %s", err, tail(p.rt.Stderr(), e.o.StderrTailBytes))
	}
	p.sessionFile = st.SessionFile
	return nil
}

// beforeTurn applies this turn's thinking level.
func (pd piDriver) beforeTurn(ctx context.Context, p *proc, snap agentconfig.Snapshot) error {
	if snap.ThinkingLevel == nil {
		return nil
	}
	thinkingCtx, cancel := context.WithTimeout(ctx, pd.e.o.StartTimeout)
	err := piClient(p).SetThinkingLevel(thinkingCtx, *snap.ThinkingLevel)
	cancel()
	if err != nil {
		return fail(executor.ErrPiError, "Could not apply the thinking level for this turn.")
	}
	return nil
}

func (piDriver) afterTurn(_ *proc, tr runturn.Result) runturn.Result { return tr }

func (piDriver) exited() (string, string) { return executor.ErrPiExited, "The Pi process exited." }

func (piDriver) crashMessage(_ *proc, stderrTail string) string {
	return "The Pi process exited unexpectedly. " + stderrTail
}

// compact runs Pi's manual compaction on the current session.
func (piDriver) compact(ctx context.Context, p *proc, instructions string) (executor.CompactionResult, error) {
	result, err := piClient(p).Compact(ctx, instructions)
	if errors.Is(err, runturn.ErrExited) {
		return executor.CompactionResult{}, err
	}
	if err != nil {
		return executor.CompactionResult{}, fail(executor.ErrPiError, "%s", err.Error())
	}
	return executor.CompactionResult{TokensBefore: result.TokensBefore, EstimatedTokensAfter: result.EstimatedTokensAfter, Skipped: result.Skipped}, nil
}

// commands reads the command list of the Pi that just started (RPC get_commands).
func (piDriver) commands(ctx context.Context, p *proc) ([]executor.PiCommand, error) {
	data, err := piClient(p).Call(ctx, "get_commands", nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Commands []struct {
			Name        string  `json:"name"`
			Description *string `json:"description"`
			Source      string  `json:"source"`
			SourceInfo  struct {
				Path string `json:"path"`
			} `json:"sourceInfo"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse the command list: %w", err)
	}
	out := []executor.PiCommand{}
	for _, c := range raw.Commands {
		if c.Name == "" || len(c.Name) > 200 {
			continue
		}
		if c.Description != nil && len(*c.Description) > 1000 {
			d := (*c.Description)[:1000]
			c.Description = &d
		}
		out = append(out, executor.PiCommand{Name: c.Name, Description: c.Description, Source: c.Source, Path: c.SourceInfo.Path})
	}
	return out, nil
}

// forkBaseline reads the regular Pi session for a task fork: a plain file inside the Agent's sessions directory
// (through os.Root, not following symlinks out of it), at most maxBytes, whose lines are all complete JSON and whose
// first line is the session header.
func (pd piDriver) forkBaseline(_ context.Context, agentID uuid.UUID, source string, maxBytes int64) ([]byte, error) {
	rootPath := pd.e.dirs(agentID).sessions
	rel, err := filepath.Rel(rootPath, source)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, errors.New("the regular session is not in the Agent's session directory")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, errors.New("the regular session directory cannot be read")
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
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("the regular session has no valid records")
	}
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			return nil, errors.New("the regular session contains incomplete records")
		}
		if i == 0 && entry["type"] != "session" {
			return nil, errors.New("the regular session header is invalid")
		}
	}
	return data, nil
}
