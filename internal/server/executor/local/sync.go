package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

// failure 是可预期的执行失败，对应 RunErrorCode。
type failure struct {
	code string
	msg  string
}

func (f *failure) Error() string { return f.code + ": " + f.msg }

func fail(code, format string, args ...any) *failure {
	return &failure{code: code, msg: fmt.Sprintf(format, args...)}
}

// prepared 是启动前准备的结果。
type prepared struct {
	env        []string
	skillDirs  []string
	cliBinDirs []string
}

// prepare 按规则同步配置：安装缺失的模板 CLI、重建 Skills 与 Prompt、先删后写凭证文件、同步工作区来源。
// 实际进行的 CLI 安装与这次同步的结果都记入该执行的运行记录。
func (e *Executor) prepare(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, runID uuid.UUID, larkShim string) (prepared, error) {
	var p prepared
	if snap.LarkBinding != nil && larkShim == "" {
		return p, fail(executor.ErrCredentialUnavailable, "Lark 用户运行通道尚未装配")
	}
	if err := d.ensure(); err != nil {
		return p, err
	}
	// 1. 安装被允许、且有模板的 CLI。
	ghBin := ""
	for _, cli := range snap.CLIs {
		if cli == "lark-cli" && snap.LarkBinding != nil {
			continue // dynamic user scopes use only the registered private-exec shim
		}
		t, ok := e.tmpl.Get(cli)
		if !ok {
			continue
		}
		begin := time.Now()
		dir, attempted, err := e.inst.EnsureReport(ctx, t)
		if attempted {
			data := map[string]any{"cli": t.Name, "version": t.Version, "result": "succeeded", "durationMs": time.Since(begin).Milliseconds()}
			if err != nil {
				data["result"], data["error"] = "failed", err.Error()
			}
			e.event(snap.AgentID, &runID, store.EventCLIInstall, data)
		}
		if err != nil {
			return p, fail(executor.ErrCLIInstallFailed, "%s：%v", cli, err)
		}
		p.cliBinDirs = append(p.cliBinDirs, dir)
		if cli == "gh" {
			ghBin = filepath.Join(dir, t.Binary)
		}
	}

	begin := time.Now()
	var outcomes []sourceOutcome
	err := e.syncConfig(ctx, snap, d, runID, &p, ghBin, &outcomes)
	if err == nil && larkShim != "" {
		for i, value := range p.env {
			if strings.HasPrefix(value, "PATH=") {
				p.env[i] = "PATH=" + larkShim + string(os.PathListSeparator) + strings.TrimPrefix(value, "PATH=")
			}
		}
	}
	data := map[string]any{"result": "succeeded", "durationMs": time.Since(begin).Milliseconds(), "sources": outcomesData(outcomes)}
	if err != nil {
		data["result"], data["error"] = "failed", failureMessage(err)
	}
	e.event(snap.AgentID, &runID, store.EventWorkspaceSync, data)
	return p, err
}

// syncConfig 依次重建 Skills 与 Prompt、写凭证文件、同步工作区来源。
func (e *Executor) syncConfig(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, runID uuid.UUID, p *prepared, ghBin string, outcomes *[]sourceOutcome) error {
	// 2. Skills 与 Prompt 按配置整体覆盖。
	var err error
	if p.skillDirs, err = e.syncSkills(ctx, snap, d); err != nil {
		return err
	}
	if err := removeStrayEntries(d); err != nil {
		return err
	}
	if err := writePluginMCP(d, snap.Plugins); err != nil {
		return err
	}
	prompt, err := e.appendedPrompt(snap, d)
	if err != nil {
		return err
	}
	if prompt != "" {
		if err := os.WriteFile(d.prompt, []byte(prompt), 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(d.prompt); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 3. 模型凭证为独立Pi缓存，CLI凭证保持模板规则。
	if err := seedModelAuth(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "模型凭证缓存写入失败")
	}
	// CLI凭证文件先删后写。
	if err := e.syncCredentialFiles(snap, d); err != nil {
		return err
	}
	p.env = e.buildEnv(d, snap, p.cliBinDirs, e.snapshotLog(snap).With("runId", runID))
	// 4. 工作区来源。
	return e.syncSources(ctx, snap, d, p.env, ghBin, outcomes)
}

// appendedPrompt 是 --append-system-prompt 文件的内容：Agent 的 Prompt 加上“文件夹与上下文”一节。
func (e *Executor) appendedPrompt(snap agentconfig.Snapshot, d agentDirs) (string, error) {
	section, err := e.contextPrompt(snap, d)
	if err != nil {
		return "", err
	}
	switch {
	case section == "":
		return snap.Prompt, nil
	case snap.Prompt == "":
		return section, nil
	}
	return strings.TrimRight(snap.Prompt, "\n") + "\n\n" + section, nil
}

// sourceOutcome 是一个文件夹来源在这次同步中的处理结果。
type sourceOutcome struct {
	path         string
	kind         string
	outcome      string // cloned / kept / files_synced / failed
	filesWritten int
	filesDeleted int
	err          string
}

func outcomesData(list []sourceOutcome) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, o := range list {
		m := map[string]any{"path": o.path, "kind": o.kind, "outcome": o.outcome}
		if o.kind == "files" {
			m["filesWritten"], m["filesDeleted"] = o.filesWritten, o.filesDeleted
		}
		if o.err != "" {
			m["error"] = o.err
		}
		out = append(out, m)
	}
	return out
}

func failureMessage(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.msg
	}
	return err.Error()
}

func (e *Executor) syncSkills(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error) {
	if err := os.RemoveAll(d.skills); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.skills, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(d.skills)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var dirs []string
	for _, k := range snap.Skills {
		if k.Name == guide.SkillName {
			e.snapshotLog(snap).Warn("Skill 副本使用了内置 Skill 的名称，已跳过", "skill", k.Name)
			continue
		}
		for _, f := range k.Files {
			data, err := e.src.ReadBlob(ctx, f.SHA256)
			if err != nil {
				return nil, err
			}
			p := path.Join(k.Name, f.Path)
			if err := root.MkdirAll(path.Dir(p), 0o700); err != nil {
				return nil, err
			}
			if err := root.WriteFile(p, data, fileMode(f.Path)); err != nil {
				return nil, err
			}
		}
		dirs = append(dirs, filepath.Join(d.skills, k.Name))
	}
	// 内置 Skill loopctl：内容由同版本 loopctl 生成，不存数据库，每次同步时写入。
	for name, data := range guide.Files() {
		p := path.Join(guide.SkillName, name)
		if err := root.MkdirAll(path.Dir(p), 0o700); err != nil {
			return nil, err
		}
		if err := root.WriteFile(p, data, fileMode(name)); err != nil {
			return nil, err
		}
	}
	dirs = append(dirs, filepath.Join(d.skills, guide.SkillName))
	return dirs, nil
}

// fileMode 让 Skill 中的脚本可执行。
func fileMode(p string) fs.FileMode {
	switch path.Ext(p) {
	case ".sh", ".py", ".js", ".mjs", ".rb", ".pl":
		return 0o700
	}
	return 0o600
}

func (e *Executor) syncCredentialFiles(snap agentconfig.Snapshot, d agentDirs) error {
	root, err := os.OpenRoot(d.home)
	if err != nil {
		return err
	}
	defer root.Close()
	// Mutable SDK caches must not survive a changed captured authorization.
	if err := root.RemoveAll(".loop-now/gcloud"); err != nil {
		return err
	}
	// Dynamic DB account directories must not survive a removed grant/CLI.
	if err := root.RemoveAll(".loop-now/db"); err != nil {
		return err
	}
	for _, p := range e.tmpl.AllCredentialFilePaths() {
		if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("删除旧凭证文件 %s 失败：%w", p, err)
		}
	}
	for _, cli := range snap.CLIs {
		if cli == "lark-cli" && snap.LarkBinding != nil {
			continue
		}
		for _, f := range snap.CLIPayloads[cli].Files {
			if err := root.MkdirAll(path.Dir(f.Path), 0o700); err != nil {
				return err
			}
			if err := root.WriteFile(f.Path, f.Content, 0o600); err != nil {
				return fmt.Errorf("写入凭证文件 %s 失败：%w", f.Path, err)
			}
		}
	}
	return nil
}

func (e *Executor) syncSources(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, env []string, ghBin string, outcomes *[]sourceOutcome) error {
	root, err := os.OpenRoot(d.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, src := range snap.Sources {
		exists := false
		if fi, err := root.Lstat(src.Path); err == nil && fi.IsDir() {
			exists = true
		}
		o := sourceOutcome{path: src.Path, kind: src.Kind}
		switch src.Kind {
		case "git":
			if exists {
				o.outcome = "kept" // 已存在的仓库不拉取、不覆盖、不重置
				*outcomes = append(*outcomes, o)
				continue
			}
			if err := e.cloneRepo(ctx, root, d, src, env, ghBin); err != nil {
				o.outcome, o.err = "failed", failureMessage(err)
				*outcomes = append(*outcomes, o)
				return err
			}
			o.outcome = "cloned"
		case "files":
			if err := e.syncFilesSource(ctx, root, src, exists, &o); err != nil {
				o.outcome, o.err = "failed", err.Error()
				*outcomes = append(*outcomes, o)
				return fail(executor.ErrWorkspaceSyncFailed, "来源 %s：%v", src.Path, err)
			}
			o.outcome = "files_synced"
		}
		*outcomes = append(*outcomes, o)
	}
	return nil
}

func (e *Executor) syncFilesSource(ctx context.Context, root *os.Root, src store.FolderSource, exists bool, o *sourceOutcome) error {
	files, err := e.src.SourceFilesForSync(ctx, src.ID)
	if err != nil {
		return err
	}
	full := !exists || src.SyncedAt == nil
	if err := root.MkdirAll(src.Path, 0o755); err != nil {
		return err
	}
	var processed []store.SyncFile
	for _, f := range files {
		p := path.Join(src.Path, f.Path)
		switch {
		case f.Pending == "delete":
			if !full {
				if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				o.filesDeleted++
			}
			processed = append(processed, f)
			continue
		case full || f.Pending == "write":
			// 首次同步写入全部文件；人工修改过的文件覆盖磁盘内容。
		default:
			if _, err := root.Lstat(p); err == nil {
				continue // 没被人改过的已有文件不覆盖
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		data, err := e.src.ReadBlob(ctx, f.SHA256)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(path.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(root, p, data); err != nil {
			return err
		}
		o.filesWritten++
		if f.Pending != "none" {
			processed = append(processed, f)
		}
	}
	return e.src.MarkSourceSynced(ctx, src.ID, processed)
}

// writeFileAtomic 先写同目录下的临时文件再改名覆盖，避免 Agent 读到写了一半的文件。
func writeFileAtomic(root *os.Root, p string, data []byte) error {
	tmp := path.Join(path.Dir(p), ".loop-now-tmp-"+randHex())
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := root.Rename(tmp, p); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

func (e *Executor) cloneRepo(ctx context.Context, root *os.Root, d agentDirs, src store.FolderSource, env []string, ghBin string) error {
	tmpName := ".loop-now-tmp-" + randHex()
	tmpAbs := filepath.Join(d.workspace, tmpName)
	defer root.RemoveAll(tmpName)

	ctx, cancel := context.WithTimeout(ctx, e.o.CloneTimeout)
	defer cancel()
	baseArgs := []string{"-c", "credential.helper="}
	if ghBin != "" {
		baseArgs = append(baseArgs, "-c", "credential.helper=!"+ghBin+" auth git-credential")
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, e.o.GitBin, append(append([]string{}, baseArgs...), args...)...)
		cmd.Env = env
		cmd.Dir = d.workspace
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second
		return cmd.CombinedOutput()
	}
	failed := func(reason string) error {
		if ctx.Err() != nil {
			return fail(executor.ErrWorkspaceSyncFailed, "来源 %s：克隆超时（%s）", src.Path, e.o.CloneTimeout)
		}
		return fail(executor.ErrWorkspaceSyncFailed, "来源 %s：%s", src.Path, reason)
	}
	if src.GitCommit != "" {
		// Only full object IDs are accepted: a branch/ref expression must never
		// turn a captured task commit into a later branch head.
		if _, err := hex.DecodeString(src.GitCommit); err != nil || (len(src.GitCommit) != 40 && len(src.GitCommit) != 64) {
			return failed("冻结版本无效")
		}
		initArgs := []string{"init", "--quiet"}
		if len(src.GitCommit) == 64 {
			initArgs = append(initArgs, "--object-format=sha256")
		}
		initArgs = append(initArgs, tmpAbs)
		for _, args := range [][]string{
			initArgs,
			{"-C", tmpAbs, "remote", "add", "--", "origin", src.GitURL},
			{"-C", tmpAbs, "fetch", "--depth=1", "--no-tags", "origin", src.GitCommit},
			{"-C", tmpAbs, "checkout", "--detach", "FETCH_HEAD"},
		} {
			if _, err := run(args...); err != nil {
				return failed("冻结版本不可用（精确浅获取失败）")
			}
		}
		head, err := run("-C", tmpAbs, "rev-parse", "HEAD")
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(head)), src.GitCommit) {
			return failed("冻结版本校验失败")
		}
	} else {
		out, err := run("clone", "--depth=1", "--single-branch", "--no-local", "--branch", src.GitBranch, "--", src.GitURL, tmpAbs)
		if err != nil {
			return failed("克隆失败：" + tail(string(out), 2000))
		}
	}
	if err := root.MkdirAll(path.Dir(src.Path), 0o755); err != nil {
		return err
	}
	if err := root.Rename(tmpName, src.Path); err != nil {
		return fail(executor.ErrWorkspaceSyncFailed, "来源 %s：%v", src.Path, err)
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func randHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func seedModelAuth(snap agentconfig.Snapshot, d agentDirs) error {
	data := []byte("{}")
	if snap.ModelCredential.Kind() != "" {
		var e error
		data, e = snap.ModelCredential.AuthFile(snap.Provider)
		if e != nil {
			return e
		}
	}
	defer clear(data)
	return modelcredential.WriteAuthFile(d.pi, data)
}
