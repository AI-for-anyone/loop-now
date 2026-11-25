package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/contexthandoff"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"

	"loop-now/internal/server/db"
)

// Source adapts task instances in front of the existing executor; regular Agents still go through the original Source.
type Source struct {
	// PluginAssets returns what the Agent's granted plugins hand to Pi; when nil, no plugins are mounted.
	PluginAssets               func(context.Context, uuid.UUID) (agentconfig.PluginAssets, error)
	ModelAuth                  *modelauth.Coordinator
	Normal                     local.Source
	Store                      *store.Store
	WorkRoot, GitBin, BasePath string
	MaxSessionBytes            int64
	CloneTimeout               time.Duration
	Templates                  *clitemplate.Catalog
	Installer                  *clitemplate.Installer
	// Installed only for schemas containing the task-switch module. Nil is an
	// explicit legacy-fixture composition, never a missing-table fallback.
	LarkOverlay   func(context.Context, store.TaskInstance) (*clicredential.LarkBinding, int64, error)
	PauseNormal   func(context.Context, uuid.UUID) (func(), error)
	FreezeNormal  func(context.Context, uuid.UUID) error
	CloseQuestion func(context.Context, *db.Tx, uuid.UUID, string) (string, error)
	ViewQuestion  func(context.Context, auth.Principal, taskmodel.Question) (taskmodel.Question, error)
	// Log records how the task workspace was copied and how long it took; when nil, nothing is recorded.
	Log *slog.Logger
	// LocalAccess returns, per base Agent, what local mode hands to Pi; when nil, local mode is not enabled.
	LocalAccess func(context.Context, uuid.UUID) (agentconfig.LocalAccess, error)
	// ProviderState gives the state of plugin provider subscription accounts (the version captured by the task
	// snapshot); paths that follow the account use the plugin's current version.
	ProviderState agentconfig.ProviderStateFunc
	// ForkBaseline reads the regular session of a base Agent for a task fork, through the driver of the Agent's runtime,
	// and returns what the instance's baseline file holds (at most maxBytes); when nil, task forks of a session are not
	// supported.
	ForkBaseline func(ctx context.Context, runtime string, agentID uuid.UUID, sessionFile string, maxBytes int64) ([]byte, error)
	// KeepsSessions reports whether a runtime keeps its sessions itself, named by a reference rather than a file (a
	// Codex thread): a retired instance's archive then records the reference. When nil, no runtime does.
	KeepsSessions func(runtime string) bool
	// ClaudeProjects returns the directory where the local Claude Code keeps its session records (a retired instance's
	// Claude Code session is archived from there); when nil, this is not supported.
	ClaudeProjects func(context.Context) string
	// HandoffBudget is the size limit of the conversation a task instance of a runtime that cannot fork the regular
	// session starts with (contexthandoff.ClampBudget; 0 means the default).
	HandoffBudget int
}

func (s *Source) ExecutionSnapshot(ctx context.Context, id uuid.UUID) (agentconfig.Snapshot, error) {
	inst, err := s.Store.TaskInstance(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.ExecutionSnapshot(ctx, id)
	}
	if err != nil {
		return agentconfig.Snapshot{}, err
	}
	active, err := s.Store.TaskRuntimeActive(ctx, id)
	if err != nil {
		return agentconfig.Snapshot{}, err
	}
	if !active {
		return agentconfig.Snapshot{}, errors.New("the task has ended; the instance cannot run")
	}
	if inst.Retired || !inst.Prepared {
		return agentconfig.Snapshot{}, errors.New("the task instance is not prepared yet or has been retired")
	}
	snap, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return agentconfig.Snapshot{}, err
	}
	if snap.Scope().AgentID() != inst.AgentID || snap.Scope().TaskID() != inst.TaskID {
		return agentconfig.Snapshot{}, errors.New("the task snapshot scope does not match")
	}
	cfg := snap.Configuration()
	creds, err := snap.Credentials()
	if err != nil {
		return agentconfig.Snapshot{}, agentconfig.ErrCredentialUnavailable
	}
	var larkRevision int64
	if s.LarkOverlay != nil {
		binding, revision, e := s.LarkOverlay(ctx, inst)
		if e != nil {
			return agentconfig.Snapshot{}, e
		}
		if binding != nil {
			if !binding.Valid() || binding.AgentID != inst.AgentID || !slices.Contains(cfg.CLIs, "lark-cli") || (cfg.Prompts != nil && cfg.Prompts.ProjectID != nil && binding.ProjectID != *cfg.Prompts.ProjectID) {
				return agentconfig.Snapshot{}, agentconfig.ErrCredentialUnavailable
			}
			creds.LarkBinding = binding
			delete(creds.CLI, "lark-cli")
			larkRevision = revision
		}
	}
	var model modelcredential.Credential
	if creds.ModelAuth != nil {
		if s.ModelAuth == nil {
			return agentconfig.Snapshot{}, agentconfig.ErrCredentialUnavailable
		}
		model, err = s.ModelAuth.Resolve(ctx, *creds.ModelAuth)
	} else {
		model, err = modelcredential.LegacySingleKey(cfg.Provider, creds.Model)
	}
	if err != nil {
		return agentconfig.Snapshot{}, agentconfig.ErrCredentialUnavailable
	}
	out := agentconfig.Snapshot{LarkBinding: creds.LarkBinding, GoogleBinding: creds.GoogleBinding, ModelBinding: creds.ModelAuth, ModelCredential: model, ConfigurationAgentID: inst.AgentID, TaskID: inst.TaskID, AgentID: id, ConfigVersion: cfg.ConfigVersion, CredentialIdentity: cfg.CredentialIdentity, Provider: cfg.Provider, ModelID: cfg.ModelID, Prompt: cfg.Prompt, PromptLayers: prompts.Clone(cfg.Prompts), CLIs: cfg.CLIs, ModelEnv: creds.Model, CLIPayloads: map[string]clitemplate.Payload{}, Skills: []agentconfig.SkillSnapshot{}, Sources: []store.FolderSource{}, Session: store.AgentSession{State: inst.SessionState, Epoch: 1}}
	out.ThinkingLevel = cfg.ThinkingLevel
	out.PermissionMode = cfg.PermissionMode
	out.ServiceTier = cfg.ServiceTier
	out.ModelsJSON = cfg.ModelsJSON
	out.ModelConfigIdentity = modelcatalog.ConfigIdentity(cfg.ModelsJSON)
	if larkRevision > 0 {
		out.CredentialIdentity = fmt.Sprintf("%s;lark-overlay:%d:%s:%s", out.CredentialIdentity, larkRevision, out.LarkBinding.RuntimeVariantVersion, out.LarkBinding.VersionID)
	}
	if cfg.Prompts != nil {
		out.Prompt, err = cfg.Prompts.Compose()
		if err != nil {
			return agentconfig.Snapshot{}, err
		}
	}
	if inst.SessionFile != nil {
		out.Session.SessionFile = *inst.SessionFile
	}
	if inst.ForkSource != nil {
		out.ForkSessionFile = *inst.ForkSource
	}
	for _, skill := range cfg.Skills {
		files := []store.SkillFile{}
		for _, f := range skill.Files {
			files = append(files, store.SkillFile{Path: f.Path, Size: int64(len(f.Content)), SHA256: content.SHA256Hex(f.Content)})
		}
		out.Skills = append(out.Skills, agentconfig.SkillSnapshot{Name: skill.Name, Files: files})
	}
	for name, p := range creds.CLI {
		files := []clitemplate.PayloadFile{}
		for _, f := range p.Files {
			files = append(files, clitemplate.PayloadFile{Path: f.Path, Content: f.Content})
		}
		out.CLIPayloads[name] = clitemplate.Payload{Env: p.Env, Files: files}
	}
	for _, src := range inst.Sources {
		out.Sources = append(out.Sources, src.Source)
	}
	// The visible context layers follow the base Agent: its project for a project Agent, global for a platform Agent.
	if err = s.Store.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id=$1`, inst.AgentID).Scan(&out.ContextProjectID); err != nil {
		return agentconfig.Snapshot{}, err
	}
	// Plugins are mounted according to the base Agent's current grants when Pi starts; a Pi that has already started
	// keeps using the files it started with.
	if s.PluginAssets != nil {
		if out.Plugins, err = s.PluginAssets(ctx, inst.AgentID); err != nil {
			return agentconfig.Snapshot{}, err
		}
	}
	// Local directories and PATH likewise follow the current settings of the base Agent and its project and are not
	// part of the task snapshot; changes a task makes to local directories land directly on the real files.
	if s.LocalAccess != nil {
		if out.Local, err = s.LocalAccess(ctx, inst.AgentID); err != nil {
			return agentconfig.Snapshot{}, err
		}
	}
	// Plugin provider subscription accounts: the snapshot captures the version (state directory), and the links are
	// likewise created when the instance starts; the workspace copy carries no tokens.
	if out.ProviderState, err = agentconfig.ResolveProviderState(ctx, s.ProviderState, out); err != nil {
		return agentconfig.Snapshot{}, err
	}
	return out, nil
}
func (s *Source) ReadBlob(ctx context.Context, sha string) ([]byte, error) {
	return s.Normal.ReadBlob(ctx, sha)
}
func (s *Source) SourceFilesForSync(ctx context.Context, id uuid.UUID) ([]store.SyncFile, error) {
	src, _, err := s.Store.FrozenTaskSource(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.SourceFilesForSync(ctx, id)
	}
	return src.Files, err
}
func (s *Source) MarkSourceSynced(ctx context.Context, id uuid.UUID, files []store.SyncFile) error {
	_, runtime, err := s.Store.FrozenTaskSource(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.MarkSourceSynced(ctx, id, files)
	}
	if err != nil {
		return err
	}
	return s.Store.MarkTaskSourceSynced(ctx, runtime, id, files)
}
func (s *Source) RecordSessionFile(ctx context.Context, id uuid.UUID, runtime, file string) error {
	_, err := s.Store.TaskInstance(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.RecordSessionFile(ctx, id, runtime, file)
	}
	if err != nil {
		return err
	}
	return s.Store.RecordTaskSession(ctx, id, file)
}
func (s *Source) MarkSessionMissing(ctx context.Context, id uuid.UUID, runID *uuid.UUID) error {
	_, err := s.Store.TaskInstance(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.MarkSessionMissing(ctx, id, runID)
	}
	if err != nil {
		return err
	}
	return s.Store.MarkTaskSessionMissing(ctx, id)
}
func (s *Source) DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := s.Normal.DeletedAgentIDs(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT id FROM task_instances WHERE retired`)
	if err != nil {
		return nil, err
	}
	more, err := db.CollectRows(rows, db.RowTo[uuid.UUID])
	return append(ids, more...), err
}

func (s *Source) Prepare(ctx context.Context, inst store.TaskInstance) error {
	if inst.Prepared {
		return nil
	}
	if s.PauseNormal == nil || s.FreezeNormal == nil || s.MaxSessionBytes <= 0 {
		return errors.New("the task fork preparer is not configured")
	}
	snapshot, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return err
	}
	if snapshot.Scope().AgentID() != inst.AgentID || snapshot.Scope().TaskID() != inst.TaskID {
		return errors.New("the task snapshot scope does not match")
	}
	creds, err := snapshot.Credentials()
	if err != nil {
		return agentconfig.ErrCredentialUnavailable
	}
	prepared, revision, err := s.taskPreparationRevision(ctx, inst)
	if err != nil || prepared {
		return err
	}
	release, err := s.PauseNormal(ctx, inst.AgentID)
	if err != nil {
		return err
	}
	var base *string
	err = func() error {
		defer release()
		if err := s.FreezeNormal(ctx, inst.AgentID); err != nil {
			return err
		}
		session, err := s.Store.GetAgentSession(ctx, inst.AgentID)
		if err != nil {
			return err
		}
		if session.State == "missing" {
			return errors.New("the regular session file is missing; cannot create the task fork")
		}
		rt := agentruntime.ForProvider(snapshot.Configuration().Provider)
		if !rt.Capabilities.TaskFork {
			// The runtime cannot copy the regular session: the instance starts a new session whose first message
			// carries the regular conversation as it is now.
			data, err := s.taskStartHandoff(ctx, inst.AgentID, rt)
			if err != nil || data == nil {
				return err
			}
			path, err := s.writeBaseline(inst, data)
			if err != nil {
				return err
			}
			base = &path
		} else if session.State == "active" && session.SessionFile != "" {
			if s.ForkBaseline == nil {
				return errors.New("task forks of a session are not configured")
			}
			runtime := agentruntime.ForProvider(snapshot.Configuration().Provider).ID
			data, err := s.ForkBaseline(ctx, runtime, inst.AgentID, session.SessionFile, s.MaxSessionBytes)
			if err != nil {
				return err
			}
			path, err := s.writeBaseline(inst, data)
			if err != nil {
				return err
			}
			base = &path
		}
		// From snapshot v9 on: copy the base Agent's whole folder within the same window (at that point its Pi has
		// stopped and new dispatches are paused), so the folder and the session fork are taken at the same moment.
		if snapshot.CopiesAgentFolder() {
			return s.copyWorkspace(inst)
		}
		return nil
	}()
	if err != nil {
		return err
	}
	for i := range inst.Sources {
		src := &inst.Sources[i].Source
		if src.Kind == "git" && src.GitCommit == "" {
			commit, err := s.resolveCommit(ctx, inst, *src, creds)
			if err != nil {
				return err
			}
			src.GitCommit = commit
		}
	}
	sources, err := json.Marshal(inst.Sources)
	if err != nil {
		return err
	}
	return s.commitTaskPreparation(ctx, inst, revision, base, sources)
}

// copyWorkspace copies the base Agent's folder as the task instance's workspace. The instance is not prepared yet, so a
// workspace or partial copy left by an earlier failure is deleted first and the copy is redone.
func (s *Source) copyWorkspace(inst store.TaskInstance) error {
	dst := folders.AgentWorkspace(s.WorkRoot, inst.ID)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if leftovers, err := filepath.Glob(dst + ".copying-*"); err == nil {
		for _, p := range leftovers {
			_ = os.RemoveAll(p)
		}
	}
	res, err := folders.CopyTree(folders.AgentWorkspace(s.WorkRoot, inst.AgentID), dst)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("failed to copy the task working directory", "taskId", inst.TaskID, "runtimeId", inst.ID, "agentId", inst.AgentID, "err", err)
		}
		return fmt.Errorf("copy the task working directory: %w", err)
	}
	if s.Log != nil {
		s.Log.Info("copied the task working directory", "taskId", inst.TaskID, "runtimeId", inst.ID, "agentId", inst.AgentID, "method", res.Method, "durationMs", res.Duration.Milliseconds())
	}
	return nil
}

// taskStartHandoff is the baseline of a task instance whose runtime cannot fork the regular session: the regular
// conversation as a handoff block (executor.TaskStartHandoff); nil when there is no conversation yet.
func (s *Source) taskStartHandoff(ctx context.Context, agentID uuid.UUID, rt agentruntime.Runtime) ([]byte, error) {
	entries, err := s.Store.HandoffConversation(ctx, agentID, uuid.Nil)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	budget := contexthandoff.ClampBudget(s.HandoffBudget)
	block := contexthandoff.BuildFor(contexthandoff.TaskStart, store.HandoffMessages(entries), rt.Name, rt.Name, budget)
	return json.Marshal(executor.TaskStartHandoff{Text: block.Text, FromRuntime: rt.ID, ToRuntime: rt.ID, IncludedMessages: block.Included,
		OmittedMessages: block.Omitted, Images: block.Images, Bytes: block.Bytes, BudgetBytes: budget})
}

// writeBaseline writes the baseline to the platform's task-baselines/<task>/<Agent>.jsonl (writing a temporary file
// first and then renaming it).
func (s *Source) writeBaseline(inst store.TaskInstance, data []byte) (string, error) {
	dir := filepath.Join(s.WorkRoot, "task-baselines", inst.TaskID.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, inst.AgentID.String()+".jsonl")
	tmp, err := os.CreateTemp(dir, ".baseline-")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(append(bytes.TrimRight(data, "\n"), '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(name, target); err != nil {
		return "", err
	}
	return target, nil
}

func (s *Source) resolveCommit(ctx context.Context, inst store.TaskInstance, src store.FolderSource, creds taskruntime.Credentials) (string, error) {
	if s.GitBin == "" || s.CloneTimeout <= 0 {
		return "", errors.New("the repository resolver is not configured")
	}
	home := filepath.Join(s.WorkRoot, "agents", inst.ID.String(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	env := append(execenv.Base(home, ""), "HOME="+home, "PATH="+s.BasePath, "GIT_TERMINAL_PROMPT=0")
	args := []string{"-c", "credential.helper="}
	if p, ok := creds.CLI["gh"]; ok {
		if s.Templates == nil || s.Installer == nil {
			return "", errors.New("the repository CLI templates are not configured")
		}
		template, ok := s.Templates.Get("gh")
		if !ok {
			return "", errors.New("the repository CLI template is not available")
		}
		dir, err := s.Installer.Ensure(ctx, template)
		if err != nil {
			return "", errors.New("installing the repository CLI failed")
		}
		args = append(args, "-c", "credential.helper=!'"+strings.ReplaceAll(filepath.Join(dir, template.Binary), "'", "'\\''")+"' auth git-credential")
		for k, v := range p.Env {
			if executor.IsReservedEnv(k) || k == "GH_CONFIG_DIR" {
				return "", errors.New("the repository credential sets a reserved environment variable")
			}
			env = append(env, k+"="+v)
		}
	}
	env = append(env, "GH_CONFIG_DIR="+filepath.Join(home, ".config", "gh"))
	args = append(args, "ls-remote", "--heads", "--", src.GitURL, src.GitBranch)
	ctx, cancel := context.WithTimeout(ctx, s.CloneTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.GitBin, args...)
	cmd.Env = env
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = s.CloneTimeout
	out, err := procgroup.Output(cmd)
	if err != nil {
		return "", errors.New("the repository branch cannot be resolved")
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == "refs/heads/"+src.GitBranch && (len(f[0]) == 40 || len(f[0]) == 64) {
			good := true
			for _, c := range f[0] {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					good = false
				}
			}
			if good {
				return f[0], nil
			}
		}
	}
	return "", errors.New("the repository branch does not exist")
}
