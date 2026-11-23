package localmode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/localmode"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

func i64(v int64) *int64  { return &v }
func bp(v bool) *bool     { return &v }
func sp(s string) *string { return &s }

type fixture struct {
	ctx      context.Context
	st       *store.Store
	svc      *localmode.Service
	project  uuid.UUID
	agent    uuid.UUID
	platform uuid.UUID
	root     string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	pa, err := st.CreateAgent(ctx, store.PlatformScope(), "Platform", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{ctx: ctx, st: st, svc: &localmode.Service{Store: st}, project: dc.Project.ID, agent: a.ID, platform: pa.ID, root: t.TempDir()}
}

func (f fixture) dir(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.root, name)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSettingsDefaultOnAndPartialUpdate(t *testing.T) {
	f := newFixture(t)
	s, err := f.svc.Settings(f.ctx)
	if err != nil || !s.Directories || !s.Git || !s.CLI || s.Logins {
		t.Fatalf("directories, git and CLI should be on by default, logins off: %+v %v", s, err)
	}
	s, err = f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Logins: bp(true)})
	if err != nil || !s.Logins || !s.Directories || !s.Git || !s.CLI {
		t.Fatalf("turn on only logins: %+v %v", s, err)
	}
	if got, _ := f.svc.Access(f.ctx, f.agent); !got.Logins {
		t.Fatalf("Access should carry logins: %+v", got)
	}
	s, err = f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Logins: bp(false)})
	if err != nil || s.Logins {
		t.Fatalf("turn off logins: %+v %v", s, err)
	}
	s, err = f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Git: bp(false)})
	if err != nil || !s.Directories || s.Git || !s.CLI {
		t.Fatalf("only git should be off: %+v %v", s, err)
	}
	s, err = f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Directories: bp(false), CLI: bp(false)})
	if err != nil || s.Directories || s.Git || s.CLI {
		t.Fatalf("all should be off: %+v %v", s, err)
	}
	if _, err := f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("an empty patch should be a validation failure: %v", err)
	}
}

func TestProjectDirectoriesRevisionAndStatus(t *testing.T) {
	f := newFixture(t)
	got, err := f.svc.ProjectDirectories(f.ctx, f.project)
	if err != nil || got.Revision != 0 || len(got.Directories) != 0 {
		t.Fatalf("never saved should be an empty list at revision 0: %+v %v", got, err)
	}
	code := f.dir(t, "code")
	missing := filepath.Join(f.root, "missing")
	got, err = f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{
		{Path: code, Note: sp("Repository")}, {Path: missing},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || len(got.Directories) != 2 || got.Directories[0].Status != localmode.StatusOK || *got.Directories[0].Note != "Repository" ||
		got.Directories[1].Status != localmode.StatusMissing || got.Directories[1].Note != nil {
		t.Fatalf("unexpected save result: %+v", got)
	}
	// Saving again with an old revision: 412, nothing is overwritten.
	if _, err := f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{}}); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatalf("an old revision should be 412: %v", err)
	}
	if _, err := f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(5), Directories: []localmode.DirectoryInput{}}); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatalf("a mismatched revision should be 412: %v", err)
	}
	// The directory is created later, and the status read back changes with it.
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = f.svc.ProjectDirectories(f.ctx, f.project)
	if err != nil || got.Revision != 1 || got.Directories[1].Status != localmode.StatusOK {
		t.Fatalf("unexpected read back: %+v %v", got, err)
	}
	got, err = f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(1), Directories: []localmode.DirectoryInput{}})
	if err != nil || got.Revision != 2 || len(got.Directories) != 0 {
		t.Fatalf("should be revision 2 after clearing: %+v %v", got, err)
	}
	if _, err := f.svc.ProjectDirectories(f.ctx, uuid.Must(uuid.NewV7())); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("a missing project should be 404: %v", err)
	}
	// Validation failures carry indexed fields, and a missing expectedRevision is reported along with them.
	_, err = f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{Directories: []localmode.DirectoryInput{{Path: "rel"}}})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 2 || ae.Fields[0].Field != "expectedRevision" || ae.Fields[1].Field != "directories[0].path" {
		t.Fatalf("unexpected validation error: %+v", err)
	}
}

func TestAgentDirectoriesInheritProject(t *testing.T) {
	f := newFixture(t)
	shared, own := f.dir(t, "shared"), f.dir(t, "own")
	if _, err := f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: shared, Note: sp("Shared")}}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.AgentDirectories(f.ctx, f.agent)
	if err != nil || got.Revision != 0 || len(got.Directories) != 0 || got.ProjectID == nil || *got.ProjectID != f.project || len(got.Inherited) != 1 || got.Inherited[0].Path != shared {
		t.Fatalf("unexpected Agent read back: %+v %v", got, err)
	}
	got, err = f.svc.PutAgentDirectories(f.ctx, f.agent, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: own}, {Path: shared, Note: sp("Listed by me too")}}})
	if err != nil || got.Revision != 1 || len(got.Directories) != 2 || len(got.Inherited) != 1 {
		t.Fatalf("unexpected Agent save: %+v %v", got, err)
	}
	// Platform Agent: nothing is inherited, and its own list works as usual.
	got, err = f.svc.PutAgentDirectories(f.ctx, f.platform, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: own}}})
	if err != nil || got.ProjectID != nil || len(got.Inherited) != 0 || len(got.Directories) != 1 {
		t.Fatalf("unexpected platform Agent result: %+v %v", got, err)
	}
	if _, err := f.svc.AgentDirectories(f.ctx, uuid.Must(uuid.NewV7())); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("a missing Agent should be 404: %v", err)
	}
}

func TestAccessMergesListsAndFollowsSwitches(t *testing.T) {
	f := newFixture(t)
	shared, own := f.dir(t, "shared"), f.dir(t, "own")
	missing := filepath.Join(f.root, "missing")
	if _, err := f.svc.PutProjectDirectories(f.ctx, f.project, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: shared, Note: sp("Shared")}, {Path: missing}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.PutAgentDirectories(f.ctx, f.agent, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: shared, Note: sp("Duplicate")}, {Path: own, Note: sp("Own")}}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Access(f.ctx, f.agent)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentconfig.LocalDirectory{{Path: shared, Note: "Shared", Inherited: true}, {Path: missing, Inherited: true}, {Path: own, Note: "Own"}}
	if !got.UserPath || len(got.Directories) != len(want) {
		t.Fatalf("Access = %+v", got)
	}
	for i := range want {
		if got.Directories[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, got.Directories[i], want[i])
		}
	}
	first := got.Identity()
	// When the list changes, the fingerprint changes; when it does not, it stays stable.
	again, _ := f.svc.Access(f.ctx, f.agent)
	if again.Identity() != first || first == "" {
		t.Fatalf("the fingerprint should be stable and non-empty: %q %q", first, again.Identity())
	}
	if _, err := f.svc.PutAgentDirectories(f.ctx, f.agent, localmode.PutInput{ExpectedRevision: i64(1), Directories: []localmode.DirectoryInput{{Path: own, Note: sp("Note changed")}}}); err != nil {
		t.Fatal(err)
	}
	changed, _ := f.svc.Access(f.ctx, f.agent)
	if changed.Identity() == first {
		t.Fatal("a note change should change the fingerprint")
	}
	// Turn off binding local directories: no directories are handed over; turn off CLI: the user's PATH is not
	// appended.
	if _, err := f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Directories: bp(false)}); err != nil {
		t.Fatal(err)
	}
	off, _ := f.svc.Access(f.ctx, f.agent)
	if len(off.Directories) != 0 || !off.UserPath {
		t.Fatalf("after turning off directories = %+v", off)
	}
	if _, err := f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{CLI: bp(false)}); err != nil {
		t.Fatal(err)
	}
	if off, _ = f.svc.Access(f.ctx, f.agent); off.UserPath || off.Identity() != "" {
		t.Fatalf("should be empty with everything off: %+v", off)
	}
	// Turning on only logins also yields a fingerprint (a switch change requires restarting Pi).
	if _, err := f.svc.UpdateSettings(f.ctx, localmode.SettingsPatch{Logins: bp(true)}); err != nil {
		t.Fatal(err)
	}
	if on, _ := f.svc.Access(f.ctx, f.agent); !on.Logins || on.Identity() == "" {
		t.Fatalf("only logins on: %+v", on)
	}
}

func TestAccessForDeletedBaseAgent(t *testing.T) {
	// The base Agent may already be deleted while its task is still running: its list is still given from its record,
	// so the task's runs do not fail.
	f := newFixture(t)
	own := f.dir(t, "own")
	if _, err := f.svc.PutAgentDirectories(f.ctx, f.agent, localmode.PutInput{ExpectedRevision: i64(0), Directories: []localmode.DirectoryInput{{Path: own}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SoftDeleteAgent(f.ctx, f.agent); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Access(f.ctx, f.agent)
	if err != nil || len(got.Directories) != 1 || got.Directories[0].Path != own {
		t.Fatalf("deleted base Agent: %+v %v", got, err)
	}
	if got, err := f.svc.Access(f.ctx, uuid.Must(uuid.NewV7())); err != nil || len(got.Directories) != 0 {
		t.Fatalf("a missing Agent should not be an error: %+v %v", got, err)
	}
}
