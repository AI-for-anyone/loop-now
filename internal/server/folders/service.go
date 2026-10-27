package folders

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
)

// GitAccess 返回从 Git 拉取时使用的环境与凭证助手：该 Agent 当前配置里的 gh CLI 凭证（项目 GitHub 账号）。
// helper 为空表示匿名克隆。
type GitAccess func(ctx context.Context, agentID uuid.UUID) (env []string, helper string, err error)

// Options 是文件夹服务的参数。
type Options struct {
	WorkRoot     string // <WORK_ROOT>：Agent 文件夹在 agents/<id>/workspace
	ContextDir   string // 项目与全局上下文文件夹的根
	MaxUpload    int64  // 单个上传文件的字节上限
	GitBin       string
	CloneTimeout time.Duration
}

// Service 是文件夹服务。
type Service struct {
	Store *store.Store
	Git   GitAccess
	O     Options
	Log   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New 创建文件夹服务；Start 之后才执行 Git 拉取。
func New(st *store.Store, git GitAccess, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{Store: st, Git: git, O: o, Log: log, ctx: ctx, cancel: cancel}
}

// AgentRoot 返回 Agent（或任务实例）在工作根目录下的目录。
func AgentRoot(workRoot string, id uuid.UUID) string {
	return filepath.Join(workRoot, "agents", id.String())
}

// AgentWorkspace 返回 Agent（或任务实例）的文件夹，也就是 Pi 的工作目录。
func AgentWorkspace(workRoot string, id uuid.UUID) string {
	return filepath.Join(AgentRoot(workRoot, id), "workspace")
}

// ContextFolder 返回上下文文件夹：projectID 为空时为全局，否则为该项目的。
func ContextFolder(contextDir string, projectID *uuid.UUID) string {
	if projectID == nil {
		return filepath.Join(contextDir, "global")
	}
	return filepath.Join(contextDir, "projects", projectID.String())
}

// AgentFolder 返回未删除的常态或平台 Agent 的文件夹。
func (s *Service) AgentFolder(ctx context.Context, agentID uuid.UUID) (Folder, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return Folder{}, err
	}
	return Folder{Dir: AgentWorkspace(s.O.WorkRoot, agentID), Trash: filepath.Join(AgentRoot(s.O.WorkRoot, agentID), ".trash")}, nil
}

// ProjectContextFolder 返回未删除项目的上下文文件夹（不存在时在首次使用时创建）。
func (s *Service) ProjectContextFolder(ctx context.Context, projectID uuid.UUID) (Folder, error) {
	if _, err := s.Store.GetProject(ctx, projectID); err != nil {
		return Folder{}, err
	}
	return Folder{Dir: ContextFolder(s.O.ContextDir, &projectID), Trash: filepath.Join(s.O.ContextDir, ".trash")}, nil
}

// GlobalContextFolder 返回全局上下文文件夹。
func (s *Service) GlobalContextFolder() Folder {
	return Folder{Dir: ContextFolder(s.O.ContextDir, nil), Trash: filepath.Join(s.O.ContextDir, ".trash")}
}

// ---------- 从 Git 拉取 ----------

// GitPullInput 是一次拉取的请求。
type GitPullInput struct {
	ActionID uuid.UUID
	URL      string
	Branch   string
	Path     string
}

// StartGitPull 校验并登记一次拉取，在后台执行。同一 actionId 返回原操作；目标不是空目录时返回 path_conflict。
func (s *Service) StartGitPull(ctx context.Context, agentID uuid.UUID, in GitPullInput) (store.FolderGitPull, error) {
	var errs []apperr.FieldError
	if in.ActionID == uuid.Nil {
		errs = append(errs, apperr.Field("actionId", "必须提供有效动作ID"))
	}
	if err := content.ValidateGitURL(in.URL); err != nil {
		errs = append(errs, apperr.Field("url", err.Error()))
	}
	if err := content.ValidateBranch(in.Branch); err != nil {
		errs = append(errs, apperr.Field("branch", err.Error()))
	}
	target, err := content.CleanRelPath(in.Path)
	if err != nil {
		errs = append(errs, apperr.Field("path", err.Error()))
	}
	if len(errs) > 0 {
		return store.FolderGitPull{}, apperr.Validation(errs...)
	}
	f, err := s.AgentFolder(ctx, agentID)
	if err != nil {
		return store.FolderGitPull{}, err
	}
	// 同一 actionId 的重放返回原操作（原拉取可能已经把目标填上），不再检查目标。
	if prior, ok, err := s.Store.FindFolderGitPull(ctx, agentID, in.ActionID); err != nil {
		return store.FolderGitPull{}, err
	} else if ok {
		if prior.URL != in.URL || prior.Branch != in.Branch || prior.Path != target {
			return store.FolderGitPull{}, apperr.Conflict(apperr.CodeActionConflict, "动作ID已用于其他请求")
		}
		return prior, nil
	}
	// 目标不是空目录时直接返回冲突，不留下拉取记录：列表只显示真正受理了的拉取。
	if err := f.checkEmptyTarget(target); err != nil {
		return store.FolderGitPull{}, err
	}
	p, created, err := s.Store.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: agentID, ActionID: in.ActionID, URL: in.URL, Branch: in.Branch, Path: target})
	if err != nil || !created {
		return p, err
	}
	s.wg.Go(func() { s.runGitPull(p, f) })
	return p, nil
}

// GitPull 返回一次拉取的状态。
func (s *Service) GitPull(ctx context.Context, agentID, id uuid.UUID) (store.FolderGitPull, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return store.FolderGitPull{}, err
	}
	return s.Store.GetFolderGitPull(ctx, agentID, id)
}

// GitPulls 返回 Agent 最近的 limit 次拉取，新的在前（页面刷新后恢复状态用）。
func (s *Service) GitPulls(ctx context.Context, agentID uuid.UUID, limit int) ([]store.FolderGitPull, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	return s.Store.ListFolderGitPulls(ctx, agentID, limit)
}

// checkEmptyTarget 确认目标不存在或是空目录。
func (f Folder) checkEmptyTarget(rel string) error {
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return mapErr("path", err)
	}
	if info.IsDir() {
		d, err := root.Open(rel)
		if err != nil {
			return mapErr("path", err)
		}
		defer d.Close()
		if names, _ := d.Readdirnames(1); len(names) == 0 {
			return nil
		}
	}
	return apperr.Conflict(apperr.CodePathConflict, "目标子目录已存在且不为空")
}

func (s *Service) gitPullTemp(agentID, id uuid.UUID) string {
	return filepath.Join(AgentRoot(s.O.WorkRoot, agentID), ".git-pulls", id.String())
}

func (s *Service) runGitPull(p store.FolderGitPull, f Folder) {
	ctx := s.ctx
	log := s.Log.With("agentId", p.AgentID, "gitPullId", p.ID, "path", p.Path)
	if ok, err := s.Store.StartFolderGitPull(ctx, p.ID); err != nil || !ok {
		return
	}
	code, message := s.clone(ctx, p, f)
	if code != "" {
		log.Warn("从 Git 拉取失败", "errorCode", code)
	} else {
		log.Info("从 Git 拉取完成")
	}
	if err := s.Store.FinishFolderGitPull(context.WithoutCancel(ctx), p.ID, code, message); err != nil {
		log.Error("记录拉取结果失败", "err", err)
	}
}

// clone 拉取这一个分支的完整历史（--single-branch，不截断），先放在文件夹之外的临时目录，成功后再移入目标。
func (s *Service) clone(ctx context.Context, p store.FolderGitPull, f Folder) (code, message string) {
	tmp := s.gitPullTemp(p.AgentID, p.ID)
	defer os.RemoveAll(tmp)
	if err := os.RemoveAll(tmp); err != nil {
		return "clone_failed", err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		return "clone_failed", err.Error()
	}
	env, helper := []string{"GIT_TERMINAL_PROMPT=0"}, ""
	if s.Git != nil {
		var err error
		if env, helper, err = s.Git(ctx, p.AgentID); err != nil {
			return "clone_failed", "读取 Agent 的 gh 凭证失败：" + err.Error()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.O.CloneTimeout)
	defer cancel()
	args := []string{"-c", "credential.helper="}
	if helper != "" {
		args = append(args, "-c", "credential.helper="+helper)
	}
	args = append(args, "clone", "--single-branch", "--no-local", "--branch", p.Branch, "--", p.URL, tmp)
	cmd := exec.CommandContext(ctx, s.O.GitBin, args...)
	cmd.Env = env
	cmd.Dir = filepath.Dir(tmp)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		switch {
		case s.ctx.Err() != nil:
			return "server_restart", "服务停止时仍在进行，已中止"
		case ctx.Err() != nil:
			return "timeout", fmt.Sprintf("超过克隆时限（%s）", s.O.CloneTimeout)
		case bytes.Contains(out, []byte("Remote branch")) && bytes.Contains(out, []byte("not found")):
			return "branch_not_found", tail(string(out), 2000)
		}
		return "clone_failed", tail(string(out), 2000)
	}
	if err := f.place(tmp, p.Path); err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == apperr.CodePathConflict {
			return "target_exists", ae.Message
		}
		return "clone_failed", err.Error()
	}
	return "", ""
}

// place 把文件夹之外的目录 src 移入文件夹内的 rel：目标必须不存在或是空目录（空目录先删除）。
func (f Folder) place(src, rel string) error {
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := f.checkEmptyTarget(rel); err != nil {
		return err
	}
	if info, err := root.Lstat(rel); err == nil && info.IsDir() {
		if err := root.Remove(rel); err != nil {
			return mapErr("path", err)
		}
	}
	if err := mkdirAll(root, path.Dir(rel)); err != nil {
		return err
	}
	// 目标的上级目录经 os.Root 打开，按目录句柄改名，不经由任何路径重新解析。
	parent, err := root.Open(path.Dir(rel))
	if err != nil {
		return mapErr("path", err)
	}
	defer parent.Close()
	srcDir, err := unix.Open(filepath.Dir(src), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(srcDir)
	return unix.Renameat(srcDir, filepath.Base(src), int(parent.Fd()), path.Base(rel))
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}

// ---------- 启动与停止 ----------

// Start 处理上次运行留下的状态：中断的拉取标为失败并删除其临时目录，清空各回收区。
func (s *Service) Start(ctx context.Context) error {
	pulls, err := s.Store.FailInterruptedFolderGitPulls(ctx)
	if err != nil {
		return err
	}
	for _, p := range pulls {
		_ = os.RemoveAll(s.gitPullTemp(p.AgentID, p.ID))
	}
	trashes := []string{filepath.Join(s.O.ContextDir, ".trash")}
	if dirs, err := os.ReadDir(filepath.Join(s.O.WorkRoot, "agents")); err == nil {
		for _, d := range dirs {
			if d.IsDir() {
				trashes = append(trashes, filepath.Join(s.O.WorkRoot, "agents", d.Name(), ".trash"))
			}
		}
	}
	// 删除可能很慢（大目录），不阻塞启动与停止；没删完的下次启动继续。
	go func() {
		for _, t := range trashes {
			_ = os.RemoveAll(t)
		}
	}()
	return nil
}

// Stop 取消进行中的拉取并等待它们记录结果。
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
