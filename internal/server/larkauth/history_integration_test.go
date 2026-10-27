package larkauth

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
)

func TestLarkHistoricalVersionsTwoAgentsAndAtomicProjectPurge(t *testing.T) {
	f := newLarkFixture(t)
	ctx := context.Background()
	second, err := f.st.CreateAgent(ctx, store.ProjectScope(f.binding.ProjectID), "second-consumer", "openai", "fake-model", "")
	if err != nil {
		t.Fatal(err)
	}
	b2 := f.binding
	b2.AgentID = second.ID
	write := func(c clicredential.Lark) store.ProjectCredentialWrite {
		b, e := c.Encode()
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { clear(b) })
		label := c.Identity.OpenID
		return store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, LarkIdentity: &c.Identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: b}
	}
	old, err := f.st.GetLarkCLIGrant(ctx, f.binding)
	if err != nil {
		t.Fatal(err)
	}
	current := f.credential.Clone()
	current.AccessToken = "historical-new-access"
	current.RefreshToken = "historical-new-refresh"
	current.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	w := write(current)
	account, err := f.st.MutateProjectCredential(ctx, f.binding.ProjectID, f.binding.AccountID, 1, nil, &w, false)
	if err != nil {
		t.Fatal(err)
	}
	v2 := f.binding
	v2.VersionID = *account.View.CurrentVersionID
	if v2.VersionID == f.binding.VersionID {
		t.Fatal("replacement rewrote captured version")
	}
	// Static replacement clears only the current public identity; old dynamic
	// versions remain available to already captured execution scopes.
	static := store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: []byte(`{"env":{},"files":[]}`)}
	account, err = f.st.MutateProjectCredential(ctx, f.binding.ProjectID, f.binding.AccountID, account.View.Revision, nil, &static, false)
	if err != nil {
		t.Fatal(err)
	}
	if account.View.LarkIdentity != nil || account.View.Status != "saved" {
		t.Fatal("static replacement retained user identity")
	}
	if _, err = f.st.MutateProjectCredential(ctx, f.binding.ProjectID, f.binding.AccountID, account.View.Revision, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	rf := larkRefreshFunc(func(_ context.Context, c clicredential.Lark) (clicredential.Lark, error) {
		calls.Add(1)
		c.AccessToken = "historical-rotated-access"
		c.RefreshToken = "historical-rotated-refresh"
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	})
	c1 := NewCoordinator(f.st, rf, CoordinatorOptions{}, nil)
	c2 := NewCoordinator(f.st, rf, CoordinatorOptions{}, nil)
	t.Cleanup(func() { c1.Close(ctx); c2.Close(ctx) })
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, job := range []struct {
		c *Coordinator
		b clicredential.LarkBinding
	}{{c1, f.binding}, {c2, b2}} {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := job.c.Resolve(ctx, job.b); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("historical shared grant consumed more than once")
	}
	v1After, err := f.st.GetLarkCLIGrant(ctx, f.binding)
	if err != nil || v1After.Generation != 2 {
		t.Fatal("captured deleted-account grant lost")
	}
	v2After, err := f.st.GetLarkCLIGrant(ctx, v2)
	if err != nil || v2After.Generation != 1 {
		t.Fatal("historical other version was rotated")
	}
	if bytes.Equal(old.Secret, v1After.Secret) {
		t.Fatal("successful rotation did not commit")
	}
	// A second project must survive both rollback and successful purge.
	d, err := f.st.EnsureDefaults(ctx, "Lark TEST组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.CreateProject(ctx, d.Organization.ID, "other-owner")
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := f.st.CreateProjectCredential(ctx, other.ID, "cli", "lark-cli", "other-user", write(current))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.st.Pool.Exec(ctx, `CREATE FUNCTION deny_lark_purge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER deny_lark_purge BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION deny_lark_purge()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.DeleteProject(ctx, f.binding.ProjectID); err == nil {
		t.Fatal("injected purge unexpectedly committed")
	}
	afterRollback, err := f.st.GetLarkCLIGrant(ctx, f.binding)
	if err != nil || !bytes.Equal(afterRollback.Secret, v1After.Secret) || afterRollback.Generation != 2 {
		t.Fatal("purge rollback changed old grant")
	}
	if _, err = f.st.Pool.Exec(ctx, `DROP TRIGGER deny_lark_purge ON projects; DROP FUNCTION deny_lark_purge()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.DeleteProject(ctx, f.binding.ProjectID); err != nil {
		t.Fatal(err)
	}
	var rows, cipher, otherRows int
	err = f.st.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE octet_length(v.secret)>0) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1`, f.binding.ProjectID).Scan(&rows, &cipher)
	if err != nil || rows != 0 || cipher != 0 {
		t.Fatal("project purge retained version credential")
	}
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants WHERE version_id=$1`, *otherAccount.View.CurrentVersionID).Scan(&otherRows); err != nil || otherRows != 1 {
		t.Fatal("purge crossed project scope")
	}
	if _, err = f.st.GetLarkCLIGrant(ctx, b2); err == nil {
		t.Fatal("purged grant remains resolvable")
	}
}
