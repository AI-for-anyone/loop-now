package taskmodel

import (
	"encoding/json"
	"reflect"
	"testing"
)

// A task view stored by stage 1, before workspaces had modes, still reads: as a git-mode workspace with the same
// values, the folder being the repository.
func TestWorkspaceReadsTheStage1Shape(t *testing.T) {
	stored := `{"repository":"/code/app","path":"/data/worktrees/t/app","branch":"task/3-fix","baseBranch":"main",` +
		`"baseCommit":"abc123","state":"kept","kept":{"uncommittedFiles":2,"unpushedCommits":1}}`
	var w Workspace
	if err := json.Unmarshal([]byte(stored), &w); err != nil {
		t.Fatal(err)
	}
	commit := "abc123"
	want := Workspace{Mode: WorkspaceModeGit, Folder: "/code/app", Path: "/data/worktrees/t/app", State: WorkspaceStateKept,
		Kept: &WorkspaceKept{UncommittedFiles: 2, UnpushedCommits: 1},
		Git:  &WorkspaceGit{Repository: "/code/app", Branch: "task/3-fix", BaseBranch: "main", BaseCommit: &commit}}
	if !reflect.DeepEqual(w, want) {
		t.Fatalf("got %+v\nwant %+v", w, want)
	}
	// A pending stage 1 workspace has no base commit yet.
	if err := json.Unmarshal([]byte(`{"repository":"/r","path":"/p","branch":"b","baseBranch":"","baseCommit":null,"state":"pending","kept":null}`), &w); err != nil {
		t.Fatal(err)
	}
	if w.Mode != WorkspaceModeGit || w.Git == nil || w.Git.BaseCommit != nil || w.Git.Branch != "b" || w.Kept != nil {
		t.Fatalf("pending stage 1 workspace: %+v", w)
	}
}

// The current shape round-trips unchanged, in both modes.
func TestWorkspaceRoundTrips(t *testing.T) {
	note := "No working git on this computer, so the folder was copied."
	for _, w := range []Workspace{
		{Mode: WorkspaceModeGit, Folder: "/code/app/web", Path: "/p", State: WorkspaceStateActive,
			Git: &WorkspaceGit{Repository: "/code/app", Branch: "b", BaseBranch: "main"}},
		{Mode: WorkspaceModeCopy, Folder: "/docs", Path: "/p", State: WorkspaceStateKept, ModeNote: &note,
			Kept: &WorkspaceKept{ChangedFiles: 1}, Copy: &WorkspaceCopy{Files: 3, Bytes: 1200}},
	} {
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		var got Workspace
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Fatalf("got %+v\nwant %+v", got, w)
		}
	}
}

// A view stored before task.workspaceMode existed gets it from its workspace; a task without one keeps null.
func TestDetailFillsWorkspaceMode(t *testing.T) {
	var d Detail
	stored := `{"task":{"branch":"task/3-fix"},"workspace":{"repository":"/code/app","path":"/p","branch":"task/3-fix","baseBranch":"main","baseCommit":null,"state":"active","kept":null}}`
	if err := json.Unmarshal([]byte(stored), &d); err != nil {
		t.Fatal(err)
	}
	if d.Task.WorkspaceMode == nil || *d.Task.WorkspaceMode != WorkspaceModeGit || d.Workspace.Git == nil {
		t.Fatalf("stage 1 view: mode %v, workspace %+v", d.Task.WorkspaceMode, d.Workspace)
	}
	var none Detail
	if err := json.Unmarshal([]byte(`{"task":{"branch":null},"workspace":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.Task.WorkspaceMode != nil {
		t.Fatalf("no workspace: mode %v", *none.Task.WorkspaceMode)
	}
}
