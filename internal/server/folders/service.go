package folders

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	neturl "net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
	"loop-now/internal/procgroup"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
)

// GitEnv is the git environment for one pull and the arguments placed before clone (-c config, such as credential
// helpers).
type GitEnv struct {
	Env  []string
	Args []string
}

// GitAccess returns the environment used when pulling from Git: the gh CLI credential in the Agent's current config
// (the project GitHub account), plus the local git identity when the local mode git switch is on (see folderGitAccess
// in app).
type GitAccess func(ctx context.Context, agentID uuid.UUID) (GitEnv, error)

// Options are the parameters of the folder service.
type Options struct {
	WorkRoot     string // <WORK_ROOT>: the Agent folder is at agents/<id>/workspace
	ContextDir   string // root of the project and global context folders
	MaxUpload    int64  // byte limit for a single uploaded file
	GitBin       string
	CloneTimeout time.Duration
}

// Service is the folder service.
type Service struct {
	Store *store.Store
	Git   GitAccess
	O     Options
	Log   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates the folder service; Git pulls run only after Start.
func New(st *store.Store, git GitAccess, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{Store: st, Git: git, O: o, Log: log, ctx: ctx, cancel: cancel}
}

// AgentRoot returns the directory of an Agent (or task instance) under the work root directory.
func AgentRoot(workRoot string, id uuid.UUID) string {
	return filepath.Join(workRoot, "agents", id.String())
}

// AgentWorkspace returns the folder of an Agent (or task instance), which is Pi's working directory.
func AgentWorkspace(workRoot string, id uuid.UUID) string {
	return filepath.Join(AgentRoot(workRoot, id), "workspace")
}

// ContextFolder returns a context folder: the global one when projectID is nil, otherwise the project's.
func ContextFolder(contextDir string, projectID *uuid.UUID) string {
	if projectID == nil {
		return filepath.Join(contextDir, "global")
	}
	return filepath.Join(contextDir, "projects", projectID.String())
}

// AgentFolder returns the folder of a regular or platform Agent that is not deleted.
func (s *Service) AgentFolder(ctx context.Context, agentID uuid.UUID) (Folder, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return Folder{}, err
	}
	return Folder{Dir: AgentWorkspace(s.O.WorkRoot, agentID), Trash: filepath.Join(AgentRoot(s.O.WorkRoot, agentID), ".trash")}, nil
}

// ProjectContextFolder returns the context folder of a project that is not deleted (created on first use if it does not
// exist).
func (s *Service) ProjectContextFolder(ctx context.Context, projectID uuid.UUID) (Folder, error) {
	if _, err := s.Store.GetProject(ctx, projectID); err != nil {
		return Folder{}, err
	}
	return Folder{Dir: ContextFolder(s.O.ContextDir, &projectID), Trash: filepath.Join(s.O.ContextDir, ".trash")}, nil
}

// GlobalContextFolder returns the global context folder.
func (s *Service) GlobalContextFolder() Folder {
	return Folder{Dir: ContextFolder(s.O.ContextDir, nil), Trash: filepath.Join(s.O.ContextDir, ".trash")}
}

// ---------- Pull from Git ----------

// GitPullInput is the request for one pull.
type GitPullInput struct {
	ActionID uuid.UUID
	URL      string
	Branch   string
	Path     string
}

// StartGitPull validates and registers a pull and runs it in the background. The same actionId returns the original
// operation; when the target is not an empty directory it returns path_conflict.
func (s *Service) StartGitPull(ctx context.Context, agentID uuid.UUID, in GitPullInput) (store.FolderGitPull, error) {
	var errs []apperr.FieldError
	if in.ActionID == uuid.Nil {
		errs = append(errs, apperr.Field("actionId", "A valid actionId is required."))
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
	// A replay with the same actionId returns the original operation (the original pull may already have filled the
	// target), without checking the target again.
	if prior, ok, err := s.Store.FindFolderGitPull(ctx, agentID, in.ActionID); err != nil {
		return store.FolderGitPull{}, err
	} else if ok {
		if prior.URL != in.URL || prior.Branch != in.Branch || prior.Path != target {
			return store.FolderGitPull{}, apperr.Conflict(apperr.CodeActionConflict, "This actionId was already used for a different request.")
		}
		return prior, nil
	}
	// When the target is not an empty directory, return a conflict right away and leave no pull record: the list shows
	// only pulls that were actually accepted.
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

// GitPull returns the status of one pull.
func (s *Service) GitPull(ctx context.Context, agentID, id uuid.UUID) (store.FolderGitPull, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return store.FolderGitPull{}, err
	}
	return s.Store.GetFolderGitPull(ctx, agentID, id)
}

// GitPulls returns the Agent's most recent limit pulls, newest first (used to restore state after a page reload).
func (s *Service) GitPulls(ctx context.Context, agentID uuid.UUID, limit int) ([]store.FolderGitPull, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	return s.Store.ListFolderGitPulls(ctx, agentID, limit)
}

// checkEmptyTarget confirms that the target does not exist or is an empty directory.
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
	return apperr.Conflict(apperr.CodePathConflict, "The target subdirectory already exists and is not empty.")
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
		log.Warn("pull from Git failed", "errorCode", code)
	} else {
		log.Info("pull from Git finished")
	}
	if err := s.Store.FinishFolderGitPull(context.WithoutCancel(ctx), p.ID, code, message); err != nil {
		log.Error("failed to record the pull result", "err", err)
	}
}

// clone fetches the full history of this one branch (--single-branch, not shallow), first into a temporary directory
// outside the folder, then moves it into the target on success.
func (s *Service) clone(ctx context.Context, p store.FolderGitPull, f Folder) (code, message string) {
	tmp := s.gitPullTemp(p.AgentID, p.ID)
	defer os.RemoveAll(tmp)
	if err := os.RemoveAll(tmp); err != nil {
		return "clone_failed", err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		return "clone_failed", err.Error()
	}
	access := GitEnv{Env: []string{"GIT_TERMINAL_PROMPT=0"}, Args: []string{"-c", "credential.helper="}}
	if s.Git != nil {
		var err error
		if access, err = s.Git(ctx, p.AgentID); err != nil {
			return "clone_failed", "Could not prepare the pull environment: " + err.Error()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.O.CloneTimeout)
	defer cancel()
	args := append(slices.Clone(access.Args), "clone", "--single-branch", "--no-local", "--branch", p.Branch, "--", p.URL, tmp)
	cmd := exec.CommandContext(ctx, s.O.GitBin, args...)
	cmd.Env = access.Env
	cmd.Dir = filepath.Dir(tmp)
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	out, err := procgroup.CombinedOutput(cmd)
	if err != nil {
		switch {
		case s.ctx.Err() != nil:
			return "server_restart", "The pull was still running when the server stopped, so it was aborted."
		case ctx.Err() != nil:
			return "timeout", fmt.Sprintf("The clone exceeded the time limit (%s).", s.O.CloneTimeout)
		}
		return classifyCloneFailure(p.URL, string(out))
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

// classifyCloneFailure derives the failure code from the output of git (and ssh); the output uses the C locale (LANG is
// not passed), so it is all English.
func classifyCloneFailure(url, out string) (code, message string) {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "host key verification failed") || strings.Contains(lower, "host identification has changed") ||
		strings.Contains(lower, "host key is known for"):
		target := sshTarget(url)
		if target == "" {
			target = "git@<host>"
		}
		return "host_key_unverified", tail(fmt.Sprintf("SSH cannot confirm the identity of this host: it is not in known_hosts, or its fingerprint has changed. "+
			"Pulling from Git does not confirm host fingerprints for you. Run ssh -T %s in a terminal on this computer, check the fingerprint and accept it, then pull again.\n\n%s", target, strings.TrimSpace(out)), 2000)
	case containsAny(lower, "could not read username", "could not read password", "terminal prompts disabled", "authentication failed",
		"permission denied (publickey", "invalid username or password", "http basic: access denied", "bad credentials",
		"the requested url returned error: 401", "the requested url returned error: 403"):
		return "auth_failed", tail(out, 2000)
	case strings.Contains(out, "Remote branch") && strings.Contains(out, "not found"):
		return "branch_not_found", tail(out, 2000)
	}
	return "clone_failed", tail(out, 2000)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// sshTarget extracts the target for ssh -T from an SSH address (user@host, plus -p when there is a port); empty when it
// is not an SSH address.
func sshTarget(raw string) string {
	if strings.HasPrefix(raw, "ssh://") || strings.HasPrefix(raw, "git+ssh://") || strings.HasPrefix(raw, "ssh+git://") {
		u, err := neturl.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		target := u.Hostname()
		if u.User != nil && u.User.Username() != "" {
			target = u.User.Username() + "@" + target
		}
		if port := u.Port(); port != "" {
			target = "-p " + port + " " + target
		}
		return target
	}
	if strings.Contains(raw, "://") {
		return ""
	}
	// scp-style address user@host:path
	if i := strings.Index(raw, ":"); i > 0 && !strings.Contains(raw[:i], "/") {
		return raw[:i]
	}
	return ""
}

// place moves the directory src outside the folder to rel inside the folder: the target must not exist or must be an
// empty directory (an empty directory is deleted first).
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
	// The target's parent directory is opened through os.Root and the rename goes through the directory handle, without
	// re-resolving any path.
	parent, err := root.Open(path.Dir(rel))
	if err != nil {
		return mapErr("path", err)
	}
	defer parent.Close()
	srcDir, err := privatefs.OpenNoFollow(filepath.Dir(src), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer srcDir.Close()
	return renameAt(srcDir, filepath.Base(src), parent, path.Base(rel))
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}

// ---------- Start and stop ----------

// Start handles state left by the previous run: interrupted pulls are marked failed and their temporary directories
// deleted, and every trash directory is emptied.
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
	// Deletion may be slow (large directories), so it does not block startup or shutdown; whatever is not deleted
	// continues at the next startup.
	go func() {
		for _, t := range trashes {
			_ = os.RemoveAll(t)
		}
	}()
	return nil
}

// Stop cancels pulls in progress and waits for them to record their results.
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
