package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// 029：拉取记录的幂等与同一目标互斥；有拉取或回放记录时拒绝回退，空库可以回退再升级。
func TestAgentFolders029PullsAndDown(t *testing.T) {
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
	action := uuid.New()
	p := store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: action, URL: "https://example.com/r.git", Branch: "main", Path: "r"}
	first, created, err := st.CreateFolderGitPull(ctx, p)
	if err != nil || !created || first.Status != "queued" {
		t.Fatalf("登记：%v %v %+v", err, created, first)
	}
	if again, created, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: action, URL: p.URL, Branch: p.Branch, Path: p.Path}); err != nil || created || again.ID != first.ID {
		t.Fatalf("同一 actionId 返回原操作：%v %v", err, created)
	}
	if _, _, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: uuid.New(), URL: p.URL, Branch: p.Branch, Path: p.Path}); !apperr.Is(err, apperr.CodePathConflict) {
		t.Fatalf("同一目标进行中应冲突：%v", err)
	}
	if err := st.FinishFolderGitPull(ctx, first.ID, "clone_failed", "boom"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: uuid.New(), URL: p.URL, Branch: p.Branch, Path: p.Path}); err != nil || !created {
		t.Fatalf("上一次结束后可以再拉：%v", err)
	}
	if err := st.MigrateDownTo(ctx, 28); err == nil || !strings.Contains(err.Error(), "agent_folders_down_refused") {
		t.Fatalf("有拉取记录时应拒绝回退：%v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM agent_folder_git_pulls`); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateDownTo(ctx, 28); err != nil {
		t.Fatalf("空表可以回退：%v", err)
	}
	if err := st.MigrateUpTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
}
