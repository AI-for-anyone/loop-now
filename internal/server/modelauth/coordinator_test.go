package modelauth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type refreshFunc func(context.Context, modelcredential.Credential) (modelcredential.Credential, error)

func (f refreshFunc) Refresh(ctx context.Context, c modelcredential.Credential) (modelcredential.Credential, error) {
	return f(ctx, c)
}
func authFixture(t *testing.T) (*store.Store, store.Agent) {
	t.Helper()
	st := pgtest.NewStore(t)
	ctx := context.Background()
	_, e := st.EnsureDefaults(ctx, "组织", "项目")
	if e != nil {
		t.Fatal(e)
	}
	a, e := st.CreateAgent(ctx, store.PlatformScope(), "认证Agent", "openai-codex", "fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	return st, a
}
func oauth(t *testing.T, account, token string, expires time.Time) modelcredential.Credential {
	t.Helper() // Credentials are synthetic sentinels; no host account or provider is used.
	data := struct {
		Type, Access, Refresh, AccountID string
		Expires                          int64
	}{"oauth", "access-" + token, "refresh-" + token, account, expires.UnixMilli()}
	encoded := []byte(`{"type":"oauth","access":"` + data.Access + `","refresh":"` + data.Refresh + `","accountId":"` + data.AccountID + `","expires":` + fmtInt(data.Expires) + `}`)
	c, e := modelcredential.Decode(encoded)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func fmtInt(value int64) string { return strconv.FormatInt(value, 10) }
func grant(t *testing.T, st *store.Store, agent uuid.UUID, value modelcredential.Credential) store.ModelAuthVersion {
	t.Helper()
	ctx := context.Background()
	a, e := st.GetAgent(ctx, agent)
	if e != nil {
		t.Fatal(e)
	}
	op, e := st.NewModelAuthOperation(ctx, agent, "openai-codex", a.ConfigVersion, time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	plain, e := value.Encode()
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	_, v, e := st.CompleteModelAuthOperation(ctx, op.ID, nil, plain)
	if e != nil || v == nil {
		t.Fatal(e)
	}
	return *v
}
func binding(v store.ModelAuthVersion) modelcredential.Binding {
	return modelcredential.Binding{VersionID: v.ID, AgentID: v.AgentID, Provider: v.Provider}
}
func waitAuth(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("authentication state not reached")
}
func TestPGRefreshSerializesWavesAndKeepsOldGrant(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	old := grant(t, st, a.ID, oauth(t, "old-account", "old", now().Add(-time.Hour)))
	current := grant(t, st, a.ID, oauth(t, "new-account", "new", now().Add(time.Hour)))
	var calls atomic.Int32
	provider := refreshFunc(func(ctx context.Context, c modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return oauth(t, "old-account", "rotated", now().Add(time.Hour)), nil
	})
	options := CoordinatorOptions{PollInterval: time.Millisecond, Now: now}
	first := NewCoordinator(st, provider, options, nil)
	second := NewCoordinator(st, provider, options, nil)
	defer first.Close(ctx)
	defer second.Close(ctx)
	for wave := 1; wave <= 2; wave++ {
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := first
				if i%2 == 0 {
					c = second
				}
				value, e := c.Resolve(ctx, binding(old))
				if e == nil && !value.SameAccount(oauth(t, "old-account", "x", now())) {
					e = errors.New("account switched")
				}
				errs <- e
			}(i)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		if calls.Load() != int32(wave) {
			t.Fatal("duplicate provider rotation", calls.Load(), wave)
		}
		clock.Add(int64((2 * time.Hour) / time.Millisecond))
	}
	pointer, _, e := st.CurrentModelAuthVersion(ctx, a.ID)
	if e != nil || pointer.ID != current.ID {
		t.Fatal("old refresh switched current account", e)
	}
}
func TestRefreshWaiterCancellationDoesNotLoseRotatedCredential(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "account", "old", time.Now().Add(-time.Hour)))
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(ctx context.Context, _ modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Error("waiter cancelled rotation")
		}
		return oauth(t, "account", "new", time.Now().Add(time.Hour)), nil
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	caller, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := c.Resolve(caller, binding(v)); done <- e }()
	<-entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	waitAuth(t, func() bool {
		row, e := st.GetModelAuthVersion(ctx, v.ID)
		return e == nil && row.Generation == 2 && row.RefreshState == "ready"
	})
	if _, e := c.Resolve(ctx, binding(v)); e != nil || calls.Load() != 1 {
		t.Fatal("committed token was discarded/replayed", e, calls.Load())
	}
}
func TestRefreshUncertainNeverRetriesOrSwitchesAccount(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "old", "old", time.Now().Add(-time.Hour)))
	current := grant(t, st, a.ID, oauth(t, "new", "new", time.Now().Add(time.Hour)))
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return modelcredential.Credential{}, errors.New("provider-body-secret-sentinel")
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	for i := 0; i < 2; i++ {
		if _, e := c.Resolve(ctx, binding(v)); !errors.Is(e, ErrCredentialUnavailable) {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retried invalid old refresh token")
	}
	row, e := st.GetModelAuthVersion(ctx, v.ID)
	if e != nil || !bytes.Equal(row.Secret, v.Secret) {
		t.Fatal("lost original evidence", e)
	}
	pointer, _, e := st.CurrentModelAuthVersion(ctx, a.ID)
	if e != nil || pointer.ID != current.ID {
		t.Fatal("failure changed account", e)
	}
}

func TestPGCommittedGrantRebuildsLostCacheAfterRestartWithoutRotation(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "old", time.Now().Add(-time.Hour)))
	current := grant(t, st, a.ID, oauth(t, "account-B", "B", time.Now().Add(time.Hour)))
	var calls atomic.Int32
	refresher := refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return oauth(t, "account-A", "committed", time.Now().Add(time.Hour)), nil
	})
	original := NewCoordinator(st, refresher, CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	value, err := original.Resolve(ctx, binding(old))
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverModelAuth(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := NewCoordinator(st, refresher, CoordinatorOptions{}, nil)
	defer restarted.Close(ctx)
	cacheDir := t.TempDir()
	cache := filepath.Join(cacheDir, "auth.json")
	for _, damage := range []string{"truncated", "missing"} {
		if damage == "truncated" {
			if err := os.WriteFile(cache, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Remove(cache); err != nil {
				t.Fatal(err)
			}
		}
		value, err = restarted.Resolve(ctx, binding(old))
		if err != nil || !value.SameAccount(oauth(t, "account-A", "x", time.Now())) {
			t.Fatal("restart changed bound grant", err)
		}
		data, err := value.AuthFile("openai-codex")
		if err != nil {
			t.Fatal(err)
		}
		if err := modelcredential.WriteAuthFile(cacheDir, data); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(cache)
		if err != nil || !bytes.Contains(data, []byte("refresh-committed")) {
			t.Fatal("PG committed token not reconstructed", err)
		}
	}
	row, err := st.GetModelAuthVersion(ctx, old.ID)
	if err != nil || calls.Load() != 1 || row.Generation != 2 {
		t.Fatal("recovery replayed rotation", err)
	}
	pointer, _, err := st.CurrentModelAuthVersion(ctx, a.ID)
	if err != nil || pointer.ID != current.ID {
		t.Fatal("cache recovery rebound to current account", err)
	}
}
func TestProviderRotationCommitFailurePreservesEvidenceAndNeverRetries(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "old", time.Now().Add(-time.Hour)))
	_, err := st.Pool.Exec(ctx, `CREATE FUNCTION reject_rotated_token() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.generation>OLD.generation THEN RAISE EXCEPTION 'synthetic-write-failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `CREATE TRIGGER reject_rotated_token BEFORE UPDATE ON model_auth_versions FOR EACH ROW EXECUTE FUNCTION reject_rotated_token()`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return oauth(t, "account-A", "rotated", time.Now().Add(time.Hour)), nil
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	for range 2 {
		if _, err := c.Resolve(ctx, binding(old)); !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatal("failed commit returned token", err)
		}
	}
	row, err := st.GetModelAuthVersion(ctx, old.ID)
	if err != nil || row.RefreshState != "unavailable" || row.ErrorCode == nil || *row.ErrorCode != "refresh_write_failed" || !bytes.Equal(row.Secret, old.Secret) || calls.Load() != 1 {
		t.Fatal("rotation failure lost evidence or replayed old token", err)
	}
}
func TestCoordinatorRejectsWrongRuntimeIdentityBeforeRefresh(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "account", "old", time.Now().Add(-time.Hour)))
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return modelcredential.Credential{}, nil
	}), CoordinatorOptions{}, nil)
	defer c.Close(ctx)
	for _, bad := range []modelcredential.Binding{
		{VersionID: v.ID, AgentID: uuid.New(), Provider: v.Provider},
		{VersionID: v.ID, AgentID: v.AgentID, Provider: "anthropic"},
		{VersionID: uuid.New(), AgentID: v.AgentID, Provider: v.Provider},
	} {
		if _, err := c.Resolve(ctx, bad); !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatal("runtime selected another grant", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized binding reached provider")
	}
}

func TestRefreshConcurrentLoginKeepsIndependentGrantVersions(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "same-account", "old-grant", time.Now().Add(-time.Hour)))
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		close(entered)
		<-release
		return oauth(t, "same-account", "rotated-old-grant", time.Now().Add(time.Hour)), nil
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	done := make(chan error, 1)
	go func() { _, err := c.Resolve(ctx, binding(old)); done <- err }()
	<-entered
	// Even the same displayed account's second login is a distinct grant and
	// must keep its independently issued refresh token while A rotates.
	newer := grant(t, st, a.ID, oauth(t, "same-account", "independent-new-login", time.Now().Add(time.Hour)))
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := c.Resolve(ctx, binding(newer))
	plain, _ := value.Encode()
	defer clear(plain)
	if err != nil || !bytes.Contains(plain, []byte("refresh-independent-new-login")) || calls.Load() != 1 {
		t.Fatal("same-account logins shared a rotation token", err)
	}
	oldRow, err := st.GetModelAuthVersion(ctx, old.ID)
	if err != nil || oldRow.Generation != 2 {
		t.Fatal("old grant rotation did not commit", err)
	}
	newRow, err := st.GetModelAuthVersion(ctx, newer.ID)
	if err != nil || newRow.Generation != 1 || !bytes.Equal(newRow.Secret, newer.Secret) {
		t.Fatal("old rotation rewrote new login", err)
	}
	pointer, _, err := st.CurrentModelAuthVersion(ctx, a.ID)
	if err != nil || pointer.ID != newer.ID {
		t.Fatal("old rotation reverted current login", err)
	}
}
func TestRestartFailsInflightGrantAndLoginWithoutProviderReplay(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "old", time.Now().Add(-time.Hour)))
	attempt := uuid.New()
	if owned, err := st.ClaimModelAuthRefresh(ctx, old.ID, old.Generation, attempt); err != nil || !owned {
		t.Fatal(err)
	}
	agent, err := st.GetAgent(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.NewModelAuthOperation(ctx, a.ID, "openai-codex", agent.ConfigVersion, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetModelAuthOperationURL(ctx, op.ID, "https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverModelAuth(ctx); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c := NewCoordinator(st, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return modelcredential.Credential{}, nil
	}), CoordinatorOptions{}, nil)
	defer c.Close(ctx)
	if _, err := c.Resolve(ctx, binding(old)); !errors.Is(err, ErrCredentialUnavailable) || calls.Load() != 0 {
		t.Fatal("restart retried uncertain old token", err)
	}
	row, err := st.GetModelAuthVersion(ctx, old.ID)
	if err != nil || row.RefreshState != "unavailable" || !bytes.Equal(row.Secret, old.Secret) {
		t.Fatal("restart lost original evidence", err)
	}
	operation, err := st.GetModelAuthOperation(ctx, a.ID, op.ID)
	if err != nil || operation.Status != "failed" || operation.AuthorizationURL != nil || operation.ErrorCode == nil || *operation.ErrorCode != "server_restarted" {
		t.Fatal("restart revived login operation", err)
	}
}
