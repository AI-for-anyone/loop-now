package convimport

import (
	"context"
	"os"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

func newService(t *testing.T, f *fixture) (*Service, *store.Store) {
	t.Helper()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(context.Background(), "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	f.org = dc.Organization.ID
	s := &Service{Store: st, ClaudeProjects: func(context.Context) string { return f.path(".claude/projects") },
		CodexHome: func() string { return f.path(".codex") }, Now: func() time.Time { return f.now }}
	return s, st
}

func waitScan(t *testing.T, s *Service, id string) apitypes.ConversationScan {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		scan, err := s.Scan(id)
		if err != nil {
			t.Fatal(err)
		}
		if scan.State != "running" {
			return scan
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the scan did not end")
	return apitypes.ConversationScan{}
}

// A scan runs in the background and reports how far it got; once done it lists the folders, each with the project
// that already has it as a local directory, and the conversations with their own folders.
func TestScanOperation(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("src/app", "git@example.com:acme/app.git")
	wt := f.worktree(repo, "src/app-wt")
	f.claude("j0000001-1111-4111-8111-111111111111", repo, time.Hour, "one")
	f.claude("j0000002-1111-4111-8111-111111111111", wt, 2*time.Hour, "two")
	s, st := newService(t, f)
	ctx := context.Background()
	p, err := st.CreateProject(ctx, f.org, "app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutProjectLocalDirectories(ctx, p.ID, 0, []store.LocalDirectoryEntry{{Path: repo}}); err != nil {
		t.Fatal(err)
	}
	started, err := s.StartScan(ctx, apitypes.ConversationScanInput{})
	if err != nil || started.OperationID == "" || started.State != "running" {
		t.Fatalf("started: %+v %v", started, err)
	}
	scan := waitScan(t, s, started.OperationID)
	if scan.State != "done" || scan.Error != nil || scan.FilesTotal != 2 || scan.FilesDone != 2 || len(scan.Folders) != 1 {
		t.Fatalf("done: %+v", scan)
	}
	fo := scan.Folders[0]
	if fo.Path != repo || !fo.Git || fo.Repository == nil || fo.Repository.MainCheckout != repo || fo.ProjectID == nil || *fo.ProjectID != p.ID {
		t.Fatalf("folder: %+v", fo)
	}
	if len(fo.Conversations) != 2 || fo.Conversations[1].Cwd != wt || fo.Conversations[0].AgentID != nil || fo.Conversations[0].SkipReason != nil {
		t.Fatalf("conversations: %+v", fo.Conversations)
	}
	if _, err := s.Scan("00000000-0000-4000-8000-000000000000"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown scan: %v", err)
	}
	if _, err := s.StartScan(ctx, apitypes.ConversationScanInput{Path: ptr("relative/path")}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("a relative path: %v", err)
	}
	_ = os.Remove // keep os imported for fixtures
}

func ptr[T any](v T) *T { return &v }
