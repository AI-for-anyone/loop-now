package folders_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type fixture struct {
	t     *testing.T
	st    *store.Store
	svc   *folders.Service
	agent uuid.UUID
	work  string
}

func newFixture(t *testing.T, gitBin string, timeout time.Duration) *fixture {
	t.Helper()
	ctx := context.Background()
	st := pgtest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	svc := folders.New(st, nil, folders.Options{WorkRoot: work, ContextDir: t.TempDir(), MaxUpload: 1 << 20, GitBin: gitBin, CloneTimeout: timeout}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	return &fixture{t: t, st: st, svc: svc, agent: a.ID, work: work}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v：%v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo 建一个本地仓库：main 有 3 次提交，feature 另有 1 次提交；返回 file:// 地址。
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(strconv.Itoa(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-q", "-m", "c"+strconv.Itoa(i))
	}
	git(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature")
	git(t, dir, "checkout", "-q", "main")
	return "file://" + dir
}

func (f *fixture) wait(id uuid.UUID) store.FolderGitPull {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		p, err := f.svc.GitPull(context.Background(), f.agent, id)
		if err != nil {
			f.t.Fatal(err)
		}
		if p.Status == "succeeded" || p.Status == "failed" {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatal("等待拉取结束超时")
	return store.FolderGitPull{}
}

func errCode(err error) string {
	if ae, ok := apperr.As(err); ok {
		return ae.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// 从 Git 一次性拉取：只拉这一个分支、保留完整历史；之后就是普通文件。
func TestGitPullSingleBranchFullHistory(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "git", time.Minute)
	url := repo(t)
	action := uuid.New()
	p, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: action, URL: url, Branch: "main", Path: "src/app"})
	if err != nil || p.Status != "queued" {
		t.Fatalf("登记：%v %+v", err, p)
	}
	again, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: action, URL: url, Branch: "main", Path: "src/app"})
	if err != nil || again.ID != p.ID {
		t.Fatalf("同一 actionId 应返回原操作：%v", err)
	}
	if _, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: action, URL: url, Branch: "feature", Path: "src/app"}); errCode(err) != apperr.CodeActionConflict {
		t.Fatalf("同一 actionId 换内容应冲突：%v", err)
	}
	if done := f.wait(p.ID); done.Status != "succeeded" || done.FinishedAt == nil || done.ErrorCode != nil {
		t.Fatalf("结果：%+v", done)
	}
	dir := filepath.Join(folders.AgentWorkspace(f.work, f.agent), "src", "app")
	if n := git(t, dir, "rev-list", "--count", "HEAD"); n != "3" {
		t.Fatalf("应保留完整历史（3 次提交）：%s", n)
	}
	if b := git(t, dir, "branch", "-a"); strings.Contains(b, "feature") {
		t.Fatalf("只拉这一个分支：%s", b)
	}
	if shallow, _ := os.Stat(filepath.Join(dir, ".git", "shallow")); shallow != nil {
		t.Fatal("不应是浅克隆")
	}
	if list, err := f.svc.GitPulls(ctx, f.agent, 20); err != nil || len(list) != 1 || list[0].ID != p.ID || list[0].Status != "succeeded" {
		t.Fatalf("最近的拉取：%v %+v", err, list)
	}
	if _, err := f.svc.GitPulls(ctx, uuid.New(), 20); errCode(err) != apperr.CodeNotFound {
		t.Fatalf("Agent 不存在应 404：%v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(folders.AgentRoot(f.work, f.agent), ".git-pulls")); len(left) != 0 {
		t.Fatalf("临时目录应清理：%v", left)
	}
	// 目标已不为空：受理时直接冲突，不留下拉取记录；同一 actionId 的重放仍返回原操作。
	if _, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: uuid.New(), URL: url, Branch: "main", Path: "src/app"}); errCode(err) != apperr.CodePathConflict {
		t.Fatalf("目标不为空应冲突：%v", err)
	}
	if list, _ := f.svc.GitPulls(ctx, f.agent, 20); len(list) != 1 {
		t.Fatalf("同步冲突不应留下记录：%+v", list)
	}
	if replay, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: action, URL: url, Branch: "main", Path: "src/app"}); err != nil || replay.ID != p.ID || replay.Status != "succeeded" {
		t.Fatalf("重放应返回原操作：%v %+v", err, replay)
	}
	// 空目录可以作为目标。
	if err := os.MkdirAll(filepath.Join(folders.AgentWorkspace(f.work, f.agent), "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err = f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: uuid.New(), URL: url, Branch: "feature", Path: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.wait(p.ID); done.Status != "succeeded" {
		t.Fatalf("拉到空目录：%+v", done)
	}
	if list, _ := f.svc.GitPulls(ctx, f.agent, 1); len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("新的在前、按 limit 截取：%+v", list)
	}
	if _, err := os.Stat(filepath.Join(folders.AgentWorkspace(f.work, f.agent), "empty", "feature.txt")); err != nil {
		t.Fatal("应拉到 feature 分支的内容")
	}
}

// 失败：分支不存在、超时；参数校验；服务重启时进行中的拉取标为 server_restart。
func TestGitPullFailures(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "git", time.Minute)
	url := repo(t)
	p, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: uuid.New(), URL: url, Branch: "nope", Path: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.wait(p.ID); done.Status != "failed" || *done.ErrorCode != "branch_not_found" || done.ErrorMessage == nil {
		t.Fatalf("分支不存在：%+v", done)
	}
	if _, err := os.Lstat(filepath.Join(folders.AgentWorkspace(f.work, f.agent), "x")); !os.IsNotExist(err) {
		t.Fatal("失败时不应留下目标目录")
	}
	for _, in := range []folders.GitPullInput{
		{ActionID: uuid.New(), URL: "https://user:token@example.com/r.git", Branch: "main", Path: "x"},
		{ActionID: uuid.New(), URL: url, Branch: "main", Path: "../x"},
		{ActionID: uuid.Nil, URL: url, Branch: "main", Path: "x"},
	} {
		if _, err := f.svc.StartGitPull(ctx, f.agent, in); errCode(err) != apperr.CodeValidationFailed {
			t.Fatalf("%+v 应 422：%v", in, err)
		}
	}
	if _, err := f.svc.StartGitPull(ctx, uuid.New(), folders.GitPullInput{ActionID: uuid.New(), URL: url, Branch: "main", Path: "x"}); errCode(err) != apperr.CodeNotFound {
		t.Fatalf("Agent 不存在应 404：%v", err)
	}

	slow := filepath.Join(t.TempDir(), "slow-git")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := newFixture(t, slow, 300*time.Millisecond)
	p, err = g.svc.StartGitPull(ctx, g.agent, folders.GitPullInput{ActionID: uuid.New(), URL: url, Branch: "main", Path: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if done := g.wait(p.ID); done.Status != "failed" || *done.ErrorCode != "timeout" {
		t.Fatalf("超时：%+v", done)
	}

	// 上次运行留下的排队记录：启动时标为 server_restart。
	if _, created, err := g.st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: g.agent, ActionID: uuid.New(), URL: url, Branch: "main", Path: "y"}); err != nil || !created {
		t.Fatal(err)
	}
	if err := g.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var code string
	if err := g.st.Pool.QueryRow(ctx, `SELECT error_code FROM agent_folder_git_pulls WHERE path='y'`).Scan(&code); err != nil || code != "server_restart" {
		t.Fatalf("重启后：%q %v", code, err)
	}
}

// 退役文件夹来源的一次性回放：从未同步的来源整体写入；已同步的只应用待写入与待删除；只执行一次，不改旧表数据。
func TestApplyLegacyFolderSourcesOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "git", time.Minute)
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	blob := func(text string) string {
		sum, err := f.st.PutBlob(ctx, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	fresh, synced, git := uuid.New(), uuid.New(), uuid.New()
	must(`INSERT INTO agent_folder_sources(id,agent_id,path,kind) VALUES($1,$2,'docs','files')`, fresh, f.agent)
	must(`INSERT INTO agent_folder_sources(id,agent_id,path,kind,synced_at) VALUES($1,$2,'notes','files',now())`, synced, f.agent)
	must(`INSERT INTO agent_folder_sources(id,agent_id,path,kind,git_url,git_branch) VALUES($1,$2,'repo','git','https://example.com/r.git','main')`, git, f.agent)
	file := func(src uuid.UUID, path, text, pending string) {
		must(`INSERT INTO agent_folder_files(source_id,path,blob_sha256,size,utf8,pending) VALUES($1,$2,$3,$4,true,$5)`, src, path, blob(text), len(text), pending)
	}
	file(fresh, "a.md", "全新", "none")
	file(fresh, "gone.md", "待删", "delete")
	file(synced, "keep.md", "旧内容（磁盘上已有）", "none")
	file(synced, "edit.md", "人改过", "write")
	file(synced, "del.md", "x", "delete")
	ws := folders.AgentWorkspace(f.work, f.agent)
	for p, text := range map[string]string{"notes/keep.md": "磁盘上的版本", "notes/edit.md": "旧", "notes/del.md": "将被删除"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, p), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := f.svc.ApplyLegacyFolderSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sources != 2 || sum.FilesWritten != 2 || sum.FilesDeleted != 1 || sum.UnclonedGitDirs != 1 {
		t.Fatalf("回放结果：%+v", sum)
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(ws, p))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	if read("docs/a.md") != "全新" || read("docs/gone.md") != "<missing>" || read("notes/keep.md") != "磁盘上的版本" || read("notes/edit.md") != "人改过" || read("notes/del.md") != "<missing>" {
		t.Fatalf("磁盘内容：%q %q %q %q %q", read("docs/a.md"), read("docs/gone.md"), read("notes/keep.md"), read("notes/edit.md"), read("notes/del.md"))
	}
	var pending int
	if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_folder_files WHERE pending<>'none'`).Scan(&pending); err != nil || pending != 3 {
		t.Fatalf("旧表数据不应修改：%d %v", pending, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes/edit.md"), []byte("人在新接口里又改了"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum, err := f.svc.ApplyLegacyFolderSources(ctx); err != nil || sum.Sources != 0 || sum.FilesWritten != 0 {
		t.Fatalf("第二次应不再回放：%+v %v", sum, err)
	}
	if read("notes/edit.md") != "人在新接口里又改了" {
		t.Fatal("第二次启动不应覆盖新的修改")
	}
}
