package modelauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

// Deleting a project account revokes it for normal runs too: an in-flight
// bound refresh ends in a clean failure and never writes the secret back.
func TestAccountDeletionRevokesInFlightBoundRefresh(t *testing.T) {
	st, _ := authFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "原来源", "openai-codex", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "存活消费者", "openai-codex", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	old := oauth(t, "account-A", "expired", time.Now().Add(-time.Hour))
	plain, err := old.Encode()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	account, err := st.CreateProjectCredential(ctx, dc.Project.ID, "model", "openai-codex", "原共享账号", store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: plain})
	if err != nil {
		t.Fatal(err)
	}
	helper := fakeGoHelper(t, `if(frame.type!=='abort'){fs.writeFileSync(new URL('./started',import.meta.url),'started');const timer=setInterval(()=>{if(fs.existsSync(new URL('./release',import.meta.url))){clearInterval(timer);send({type:'result',id:frame.id,status:'succeeded',credential});}},5);}`)
	coord := NewCoordinator(st, helper, CoordinatorOptions{RefreshTimeout: 5 * time.Second, PollInterval: time.Millisecond}, nil)
	auth := NewService(st, nil, coord, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, auth)
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	project := NewProjectService(auth, templates, 4096)
	binding := modelcredential.Binding{OwnerKind: "project", AgentID: consumer.ID, ProjectID: &dc.Project.ID, AccountID: &account.View.ID, VersionID: *account.View.CurrentVersionID, Provider: "openai-codex"}
	done := make(chan error, 1)
	go func() {
		fresh, err := coord.Resolve(ctx, binding)
		if err == nil && !old.SameAccount(fresh) {
			err = ErrCredentialUnavailable
		}
		done <- err
	}()
	root := filepath.Dir(helper.Options.Entry)
	waitAuth(t, func() bool { _, err := os.Stat(filepath.Join(root, "started")); return err == nil })
	if err = st.SoftDeleteAgent(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = project.Delete(ctx, dc.Project.ID, account.View.ID, account.View.Revision); err != nil {
		t.Fatal(err)
	}
	// Deletion revokes the account at once: no task binds the version, so it
	// is wiped and the consumer waiting on the in-flight refresh fails cleanly.
	if _, err = st.GetModelAuthVersion(ctx, binding.VersionID); err == nil {
		t.Fatal("account deletion kept the bound version")
	}
	select {
	case err = <-done:
		if !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatal("waiting consumer did not fail cleanly", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The helper's late rotation must not revive the secret.
	if err = os.WriteFile(filepath.Join(root, "release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = coord.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, account.View.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("late refresh revived the deleted secret", versions, err)
	}
	tombstone, err := project.Get(ctx, dc.Project.ID, account.View.ID)
	if err != nil || tombstone.Status != "deleted" || tombstone.DeletedAt == nil || tombstone.CurrentVersionID != nil || tombstone.Revision != account.View.Revision+1 {
		t.Fatal("refresh revived account", err)
	}
	if _, err = coord.Resolve(ctx, binding); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("captured consumer still resolves a deleted account", err)
	}
}
