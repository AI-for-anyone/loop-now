package cliauth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
)

func TestGooglePGAgentDeleteFinishesClaimWithoutNewAccess(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "shared", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
	entered, release := make(chan struct{}), make(chan struct{})
	c := NewCoordinator(f.st, googleRefreshFunc(func(ctx context.Context, g clicredential.Google) (clicredential.Google, error) {
		close(entered)
		<-release
		g.RefreshToken = "synthetic-rotated-after-agent-delete"
		g.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return g, nil
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	done := make(chan error, 1)
	go func() { _, err := c.Resolve(ctx, b); done <- err }()
	<-entered
	leaseRow, e := f.st.GetGoogleCLIGrant(ctx, b)
	if e != nil || leaseRow.LeaseID == nil {
		close(release)
		t.Fatal("refresh claim missing")
	}
	lease := *leaseRow.LeaseID
	other, e := f.st.CreateAgent(ctx, store.ProjectScope(f.project), "other fixture", "openai", "fake-model", "")
	if e != nil {
		close(release)
		t.Fatal(e)
	}
	for _, bad := range []clicredential.Binding{
		{VersionID: b.VersionID, AccountID: b.AccountID, ProjectID: b.ProjectID, AgentID: other.ID, CLI: "gcloud"},
		{VersionID: b.VersionID, AccountID: b.AccountID, ProjectID: uuid.New(), AgentID: b.AgentID, CLI: "gcloud"},
	} {
		if e = f.st.CommitGoogleCLIRefresh(ctx, bad, 1, lease, []byte("must-not-write")); e == nil {
			close(release)
			t.Fatal("wrong refresh owner accepted")
		}
		_ = f.st.FailGoogleCLIRefresh(ctx, bad, 1, lease, "refresh_failed")
	}
	for _, bad := range []struct {
		generation int64
		lease      uuid.UUID
	}{{0, lease}, {1, uuid.New()}} {
		if e = f.st.CommitGoogleCLIRefresh(ctx, b, bad.generation, bad.lease, []byte("must-not-write")); e == nil {
			close(release)
			t.Fatal("old claim accepted")
		}
	}
	unchanged, e := f.st.GetGoogleCLIGrant(ctx, b)
	if e != nil || unchanged.RefreshState != "refreshing" || unchanged.LeaseID == nil || *unchanged.LeaseID != lease || string(unchanged.Secret) == "must-not-write" {
		close(release)
		t.Fatal("invalid owner changed original claim")
	}
	if e = f.st.SoftDeleteAgent(ctx, f.agent); e != nil {
		close(release)
		t.Fatal(e)
	}
	close(release)
	<-done // Deleted owner must not receive further access, even after commit.
	if e = c.Close(ctx); e != nil {
		t.Fatal(e)
	}
	var generation int64
	var state string
	if e = f.st.Pool.QueryRow(ctx, "SELECT generation,refresh_state FROM google_cli_grants WHERE version_id=$1", b.VersionID).Scan(&generation, &state); e != nil || generation != 2 || state != "ready" {
		t.Fatal("claimed rotation was lost after agent deletion", e)
	}
	if _, e = f.st.GetGoogleCLIGrant(ctx, b); e == nil {
		t.Fatal("deleted Agent can read retained grant")
	}
	if claimed, e := f.st.ClaimGoogleCLIRefresh(ctx, b, 2, uuid.New(), time.Now().Add(time.Minute)); e == nil || claimed {
		t.Fatal("deleted Agent can claim new refresh")
	}
	account, e := f.st.GetProjectCredential(ctx, f.project, r.View.ID)
	if e != nil || account.View.DeletedAt != nil || account.View.CurrentVersionID == nil || *account.View.CurrentVersionID != b.VersionID {
		t.Fatal("Agent deletion changed project account", e)
	}
}
