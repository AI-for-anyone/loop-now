package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
)

// Source 在现有执行器前适配任务实例；正常Agent仍走原Source。
type Source struct {
	// PluginAssets 返回 Agent 已授权插件交给 Pi 的东西；为空时不挂载插件。
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
	CloseQuestion func(context.Context, pgx.Tx, uuid.UUID, string) (string, error)
	ViewQuestion  func(context.Context, auth.Principal, taskmodel.Question) (taskmodel.Question, error)
	// Log 记录任务工作区复制的方式与耗时；为空时不记录。
	Log *slog.Logger
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
		return agentconfig.Snapshot{}, errors.New("任务已结束，不能执行实例")
	}
	if inst.Retired || !inst.Prepared {
		return agentconfig.Snapshot{}, errors.New("任务实例尚未准备或已经回收")
	}
	snap, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return agentconfig.Snapshot{}, err
	}
	if snap.Scope().AgentID() != inst.AgentID || snap.Scope().TaskID() != inst.TaskID {
		return agentconfig.Snapshot{}, errors.New("任务快照作用域不匹配")
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
	out.ServiceTier = cfg.ServiceTier
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
	// 可见的上下文层随基础 Agent：项目 Agent 为其项目，平台 Agent 为全局。
	if err = s.Store.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id=$1`, inst.AgentID).Scan(&out.ContextProjectID); err != nil {
		return agentconfig.Snapshot{}, err
	}
	// 插件按基础 Agent 当前的授权在启动 Pi 时挂载；已经启动的 Pi 继续用它启动时的文件。
	if s.PluginAssets != nil {
		if out.Plugins, err = s.PluginAssets(ctx, inst.AgentID); err != nil {
			return agentconfig.Snapshot{}, err
		}
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
func (s *Source) RecordSessionFile(ctx context.Context, id uuid.UUID, file string) error {
	_, err := s.Store.TaskInstance(ctx, id)
	if store.IsNotFound(err) {
		return s.Normal.RecordSessionFile(ctx, id, file)
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
	more, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	return append(ids, more...), err
}

func (s *Source) Prepare(ctx context.Context, inst store.TaskInstance) error {
	if inst.Prepared {
		return nil
	}
	if s.PauseNormal == nil || s.FreezeNormal == nil || s.MaxSessionBytes <= 0 {
		return errors.New("任务分叉准备器未配置")
	}
	snapshot, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return err
	}
	if snapshot.Scope().AgentID() != inst.AgentID || snapshot.Scope().TaskID() != inst.TaskID {
		return errors.New("任务快照作用域不匹配")
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
			return errors.New("常态会话文件丢失，不能建立任务分叉")
		}
		if session.State == "active" && session.SessionFile != "" {
			path, err := s.copyBaseline(inst, session.SessionFile)
			if err != nil {
				return err
			}
			base = &path
		}
		// 快照 v9 起：在同一窗口里复制基础 Agent 的整个文件夹（此时它的 Pi 已停、新派发已暂停），
		// 文件夹与会话分叉取自同一时刻。
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

// copyWorkspace 把基础 Agent 的文件夹复制为任务实例的工作区。实例尚未准备完成，之前失败留下的工作区或半成品都先删除再重做。
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
			s.Log.Error("任务工作目录复制失败", "taskId", inst.TaskID, "runtimeId", inst.ID, "agentId", inst.AgentID, "err", err)
		}
		return fmt.Errorf("任务工作目录复制失败：%w", err)
	}
	if s.Log != nil {
		s.Log.Info("任务工作目录已复制", "taskId", inst.TaskID, "runtimeId", inst.ID, "agentId", inst.AgentID, "method", res.Method, "durationMs", res.Duration.Milliseconds())
	}
	return nil
}

func (s *Source) copyBaseline(inst store.TaskInstance, source string) (string, error) {
	rootPath := filepath.Join(s.WorkRoot, "agents", inst.AgentID.String(), "sessions")
	rel, err := filepath.Rel(rootPath, source)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("常态会话不在该Agent的会话目录")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", errors.New("常态会话目录不可读")
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("常态会话必须是普通文件")
	}
	f, err := root.Open(rel)
	if err != nil {
		return "", errors.New("常态会话不可读")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.MaxSessionBytes+1))
	if err != nil || int64(len(data)) > s.MaxSessionBytes {
		return "", errors.New("常态会话超限或读取失败")
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(bytes.TrimSpace(data)) == 0 {
		return "", errors.New("常态会话无有效记录")
	}
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			return "", errors.New("常态会话包含不完整记录")
		}
		if i == 0 && entry["type"] != "session" {
			return "", errors.New("常态会话头无效")
		}
	}
	dir := filepath.Join(s.WorkRoot, "task-baselines", inst.TaskID.String())
	if err = os.MkdirAll(dir, 0o700); err != nil {
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
		return "", errors.New("仓库解析器未配置")
	}
	home := filepath.Join(s.WorkRoot, "agents", inst.ID.String(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	env := []string{"HOME=" + home, "PATH=" + s.BasePath, "GIT_TERMINAL_PROMPT=0"}
	args := []string{"-c", "credential.helper="}
	if p, ok := creds.CLI["gh"]; ok {
		if s.Templates == nil || s.Installer == nil {
			return "", errors.New("仓库CLI模板未配置")
		}
		template, ok := s.Templates.Get("gh")
		if !ok {
			return "", errors.New("仓库CLI模板不可用")
		}
		dir, err := s.Installer.Ensure(ctx, template)
		if err != nil {
			return "", errors.New("仓库CLI安装失败")
		}
		args = append(args, "-c", "credential.helper=!'"+strings.ReplaceAll(filepath.Join(dir, template.Binary), "'", "'\\''")+"' auth git-credential")
		for k, v := range p.Env {
			if executor.IsReservedEnv(k) || k == "GH_CONFIG_DIR" {
				return "", errors.New("仓库凭证包含保留环境变量")
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = s.CloneTimeout
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("仓库分支无法解析")
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
	return "", errors.New("仓库分支不存在")
}
