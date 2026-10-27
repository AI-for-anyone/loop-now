package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type modelAuthEnv struct {
	t     *testing.T
	s     *store.Store
	ctx   context.Context
	agent store.Agent
}

func newModelAuthEnv(t *testing.T) *modelAuthEnv {
	t.Helper()
	s := pgtest.NewStore(t)
	ctx := context.Background()
	_, err := s.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.PlatformScope(), "模型Agent", "openai-codex", "codex-test", "")
	if err != nil {
		t.Fatal(err)
	}
	return &modelAuthEnv{t, s, ctx, a}
}
func (e *modelAuthEnv) seal(value string) []byte { return []byte(value) }
func (e *modelAuthEnv) config() int64 {
	e.t.Helper()
	a, err := e.s.GetAgent(e.ctx, e.agent.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return a.ConfigVersion
}
func (e *modelAuthEnv) operation() store.ModelAuthOperation {
	e.t.Helper()
	o, err := e.s.NewModelAuthOperation(e.ctx, e.agent.ID, e.agent.ModelProvider, e.config(), time.Now().Add(time.Minute))
	if err != nil {
		e.t.Fatal(err)
	}
	return o
}
func (e *modelAuthEnv) key(value string) store.ModelAuthVersion {
	e.t.Helper()
	v, err := e.s.PutModelAuthKey(e.ctx, e.agent.ID, e.agent.ModelProvider, nil, e.seal(value))
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}
func (e *modelAuthEnv) open(v store.ModelAuthVersion) string {
	e.t.Helper()
	return string(v.Secret)
}
func (e *modelAuthEnv) countVersions() int {
	e.t.Helper()
	var n int
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1`, e.agent.ID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
func (e *modelAuthEnv) current(id uuid.UUID) {
	e.t.Helper()
	v, exists, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID)
	if err != nil || !exists || v == nil || v.ID != id {
		e.t.Fatalf("unexpected current version: exists=%v err=%v", exists, err)
	}
}

func TestModelAuthVersionsKeepAccountAndLegacyMarker(t *testing.T) {
	e := newModelAuthEnv(t)
	if v, exists, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID); err != nil || exists || v != nil {
		t.Fatal("unconverted pointer must be absent")
	}
	legacy, err := e.s.PutCredential(e.ctx, e.agent.ID, store.PurposeModel, "", store.CredentialMeta{}, e.seal("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	before := e.config()
	a, err := e.s.PutModelAuthKey(e.ctx, e.agent.ID, e.agent.ModelProvider, &before, e.seal("account-A"))
	if err != nil {
		t.Fatal(err)
	}
	if e.open(a) != "account-A" || a.AgentID != e.agent.ID || a.AuthMethod != "api_key" || a.Generation != 1 || a.RefreshState != "ready" || e.config() != before+1 {
		t.Fatal("new key version contract violated")
	}
	if _, err = e.s.PutModelAuthKey(e.ctx, e.agent.ID, e.agent.ModelProvider, &before, e.seal("stale")); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("stale config accepted", err)
	}
	if _, err = e.s.PutModelAuthKey(e.ctx, e.agent.ID, "other", nil, e.seal("wrong provider")); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatal("provider mismatch accepted", err)
	}
	b := e.key("account-B")
	e.current(b.ID)
	old, err := e.s.GetModelAuthVersion(e.ctx, a.ID)
	if err != nil || e.open(old) != "account-A" || a.ID == b.ID {
		t.Fatal("old task account replaced")
	}
	// SQL prevents a current pointer from referencing another Agent's version.
	other, err := e.s.CreateAgent(e.ctx, store.PlatformScope(), "另一Agent", "openai-codex", "codex-test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Pool.Exec(e.ctx, `INSERT INTO agent_model_auth(agent_id,version_id) VALUES($1,$2)`, other.ID, b.ID); err == nil {
		t.Fatal("cross-Agent pointer accepted")
	}
	before = e.config()
	if err = e.s.ClearModelAuthVersion(e.ctx, e.agent.ID); err != nil {
		t.Fatal(err)
	}
	v, exists, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID)
	if err != nil || !exists || v != nil || e.config() != before+1 || e.countVersions() != 2 {
		t.Fatal("clear marker lost historical versions")
	}
	rows, err := e.s.ListCredentials(e.ctx, e.agent.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != legacy.ID {
		t.Fatal("legacy ciphertext was removed")
	}
	if _, err = e.s.GetModelAuthVersion(e.ctx, b.ID); err != nil {
		t.Fatal("clear deleted task-bound account")
	}
}

func TestModelAuthOperationLifecycleAndRollback(t *testing.T) {
	e := newModelAuthEnv(t)
	if o, err := e.s.LatestModelAuthOperation(e.ctx, e.agent.ID); err != nil || o != nil {
		t.Fatal("unexpected latest operation")
	}
	baseline := e.key("old-account")
	o := e.operation()
	if _, err := e.s.NewModelAuthOperation(e.ctx, e.agent.ID, e.agent.ModelProvider, e.config(), time.Now().Add(time.Minute)); !errors.Is(err, store.ErrModelAuthBusy) {
		t.Fatal("active login was not rejected", err)
	}
	if _, err := e.s.GetModelAuthOperation(e.ctx, uuid.New(), o.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatal("operation escaped Agent scope")
	}
	awaiting, err := e.s.SetModelAuthOperationURL(e.ctx, o.ID, "https://example.invalid/authorize")
	if err != nil || awaiting.Status != "awaiting_authorization" || awaiting.AuthorizationURL == nil {
		t.Fatal("URL state not saved")
	}
	ended, err := e.s.EndModelAuthOperation(e.ctx, o.ID, "cancelled", "")
	if err != nil || ended.AuthorizationURL != nil || ended.CompletedAt == nil {
		t.Fatal("cancel did not clean URL")
	}
	late, ver, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, e.seal("late"))
	if err != nil || ver != nil || late.Status != "cancelled" {
		t.Fatal("cancelled callback saved credentials")
	}
	late, err = e.s.SetModelAuthOperationURL(e.ctx, o.ID, "https://example.invalid/late")
	if err != nil || late.Status != "cancelled" || late.AuthorizationURL != nil {
		t.Fatal("late URL resurrected login")
	}
	late, err = e.s.EndModelAuthOperation(e.ctx, o.ID, "failed", "late")
	if err != nil || late.Status != "cancelled" {
		t.Fatal("late terminal changed result")
	}
	if _, err = e.s.EndModelAuthOperation(e.ctx, o.ID, "succeeded", ""); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatal("arbitrary success accepted")
	}
	e.current(baseline.ID)
	// A rejected credential write must leave pointer, config and pending operation untouched.
	o = e.operation()
	before := e.config()
	if _, _, err = e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, nil); err == nil {
		t.Fatal("missing write error")
	}
	unchanged, err := e.s.GetModelAuthOperation(e.ctx, e.agent.ID, o.ID)
	if err != nil || unchanged.Status != "pending" || e.config() != before || e.countVersions() != 1 {
		t.Fatal("write failure partially committed")
	}
	e.current(baseline.ID)
	// Success saves once and never writes again on duplicate callback.
	label := "account label"
	success, v, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, &label, e.seal("oauth-account"))
	if err != nil || v == nil || success.Status != "succeeded" || success.AuthorizationURL != nil || v.AuthMethod != "oauth" || v.AccountLabel == nil || *v.AccountLabel != label {
		t.Fatal("login completion failed", err)
	}
	if e.open(*v) != "oauth-account" || e.config() != before+1 {
		t.Fatal("success did not bind credential/config")
	}
	e.current(v.ID)
	success, repeat, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, e.seal("late"))
	if err != nil || repeat != nil || success.Status != "succeeded" || e.countVersions() != 2 {
		t.Fatal("duplicate completion created a version")
	}
	latest, err := e.s.LatestModelAuthOperation(e.ctx, e.agent.ID)
	if err != nil || latest == nil || latest.ID != o.ID {
		t.Fatal("latest operation incorrect")
	}
	// A rejected API Key write also leaves all prior data intact.
	before = e.config()
	if _, err = e.s.PutModelAuthKey(e.ctx, e.agent.ID, e.agent.ModelProvider, nil, nil); err == nil || e.config() != before || e.countVersions() != 2 {
		t.Fatal("API key write rollback failed")
	}
}

func TestModelAuthRejectsExpiredChangedOrDeletedCallbacks(t *testing.T) {
	for _, kind := range []string{"expired", "config", "provider", "replaced", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			e := newModelAuthEnv(t)
			baseline := e.key("account-A")
			o := e.operation()
			switch kind {
			case "expired":
				if _, err := e.s.Pool.Exec(e.ctx, `UPDATE model_auth_operations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, o.ID); err != nil {
					t.Fatal(err)
				}
			case "config":
				prompt := "changed"
				if _, err := e.s.UpdateAgent(e.ctx, e.agent.ID, store.AgentPatch{Prompt: &prompt, ExpectedPromptVersion: func() *int64 { v := int64(1); return &v }()}); err != nil {
					t.Fatal(err)
				}
			case "provider":
				provider := "anthropic"
				if _, err := e.s.UpdateAgent(e.ctx, e.agent.ID, store.AgentPatch{ModelProvider: &provider}); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				baseline = e.key("account-B")
			case "deleted":
				if _, err := e.s.SetModelAuthOperationURL(e.ctx, o.ID, "https://example.invalid/authorize"); err != nil {
					t.Fatal(err)
				}
				if err := e.s.SoftDeleteAgent(e.ctx, e.agent.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, v, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, e.seal("late"))
			if kind == "deleted" {
				// SoftDeleteAgent cancels active login and removes all account ciphertext/pointers
				// atomically. A late completion must preserve that terminal state without sealing.
				if err != nil || v != nil || result.Status != "cancelled" || result.ErrorCode != nil || result.AuthorizationURL != nil || result.CompletedAt == nil {
					t.Fatal("deleted Agent callback did not preserve cancellation", err)
				}
				current, exists, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID)
				if err != nil || exists || current != nil || e.countVersions() != 0 {
					t.Fatal("deleted Agent retained account ciphertext or pointer", err)
				}
				if _, err := e.s.GetModelAuthVersion(e.ctx, baseline.ID); !apperr.Is(err, apperr.CodeNotFound) {
					t.Fatal("deleted account version is still readable", err)
				}
				repeat, repeatedVersion, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, e.seal("late"))
				if err != nil || repeatedVersion != nil || repeat.Status != "cancelled" || repeat.AuthorizationURL != nil {
					t.Fatal("repeated deleted callback resurrected credentials", err)
				}
				return
			}
			expected, code := "failed", "configuration_changed"
			if kind == "expired" {
				expected, code = "expired", "auth_timeout"
			}
			if err != nil || v != nil || result.Status != expected || result.ErrorCode == nil || *result.ErrorCode != code || result.AuthorizationURL != nil {
				t.Fatal("invalid callback changed state", err)
			}
			e.current(baseline.ID)
		})
	}
}

func TestModelAuthRefreshCASPreservesOldAndCurrentAccounts(t *testing.T) {
	e := newModelAuthEnv(t)
	a := e.key("account-A")
	b := e.key("account-B")
	config := e.config()
	const workers = 20
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	attempts := make(chan uuid.UUID, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id := uuid.New()
			ok, err := e.s.ClaimModelAuthRefresh(e.ctx, a.ID, 1, id)
			if err != nil {
				errs <- err
			}
			if ok {
				wins.Add(1)
				attempts <- id
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if wins.Load() != 1 {
		t.Fatal("multiple refresh owners", wins.Load())
	}
	attempt := <-attempts
	sealed := e.seal("account-A-refreshed")
	if ok, err := e.s.CommitModelAuthRefresh(e.ctx, a.ID, uuid.New(), sealed); err != nil || ok {
		t.Fatal("foreign attempt committed")
	}
	if err := e.s.FailModelAuthRefresh(e.ctx, a.ID, uuid.New(), "stale"); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.s.CommitModelAuthRefresh(e.ctx, a.ID, attempt, sealed); err != nil || !ok {
		t.Fatal("owner could not commit", err)
	}
	if ok, err := e.s.CommitModelAuthRefresh(e.ctx, a.ID, attempt, sealed); err != nil || ok {
		t.Fatal("duplicate refresh committed")
	}
	old, err := e.s.GetModelAuthVersion(e.ctx, a.ID)
	if err != nil || old.Generation != 2 || old.RefreshState != "ready" || old.RefreshAttemptID != nil || old.RefreshStartedAt != nil || old.ErrorCode != nil || e.open(old) != "account-A-refreshed" {
		t.Fatal("refresh state incorrect", err)
	}
	e.current(b.ID)
	if e.config() != config {
		t.Fatal("refresh changed Agent config")
	}
	if current, _, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID); err != nil || e.open(*current) != "account-B" {
		t.Fatal("refresh switched account")
	}
	if ok, err := e.s.ClaimModelAuthRefresh(e.ctx, a.ID, 1, uuid.New()); err != nil || ok {
		t.Fatal("stale generation claimed")
	}
	next := uuid.New()
	if ok, err := e.s.ClaimModelAuthRefresh(e.ctx, a.ID, 2, next); err != nil || !ok {
		t.Fatal("current generation not claimable")
	}
	if err = e.s.FailModelAuthRefresh(e.ctx, a.ID, next, "refresh_failed"); err != nil {
		t.Fatal(err)
	}
	old, err = e.s.GetModelAuthVersion(e.ctx, a.ID)
	if err != nil || old.RefreshState != "unavailable" || old.Generation != 2 || old.ErrorCode == nil || *old.ErrorCode != "refresh_failed" || e.open(old) != "account-A-refreshed" {
		t.Fatal("failed refresh discarded old ciphertext")
	}
	if ok, err := e.s.ClaimModelAuthRefresh(e.ctx, a.ID, 2, uuid.New()); err != nil || ok {
		t.Fatal("unavailable refresh retried old token")
	}
}

func TestModelAuthRecoveryDoesNotReplayOrSwitchAccounts(t *testing.T) {
	e := newModelAuthEnv(t)
	a := e.key("account-A")
	b := e.key("account-B")
	o := e.operation()
	if _, err := e.s.SetModelAuthOperationURL(e.ctx, o.ID, "https://example.invalid/authorize"); err != nil {
		t.Fatal(err)
	}
	attempt := uuid.New()
	if ok, err := e.s.ClaimModelAuthRefresh(e.ctx, a.ID, 1, attempt); err != nil || !ok {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.s.RecoverModelAuth(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := e.s.GetModelAuthOperation(e.ctx, e.agent.ID, o.ID)
	if err != nil || operation.Status != "failed" || operation.AuthorizationURL != nil || operation.ErrorCode == nil || *operation.ErrorCode != "server_restarted" {
		t.Fatal("pending login not failed on recovery")
	}
	old, err := e.s.GetModelAuthVersion(e.ctx, a.ID)
	if err != nil || old.RefreshState != "unavailable" || old.ErrorCode == nil || *old.ErrorCode != "refresh_uncertain" || old.Generation != 1 || e.open(old) != "account-A" {
		t.Fatal("uncertain refresh replayed")
	}
	sealed := e.seal("late")
	if ok, err := e.s.CommitModelAuthRefresh(e.ctx, a.ID, attempt, sealed); err != nil || ok {
		t.Fatal("late refresh accepted after restart")
	}
	if result, v, err := e.s.CompleteModelAuthOperation(e.ctx, o.ID, nil, sealed); err != nil || v != nil || result.Status != "failed" {
		t.Fatal("late login accepted after restart")
	}
	e.current(b.ID)
	if current, _, err := e.s.CurrentModelAuthVersion(e.ctx, e.agent.ID); err != nil || current.RefreshState != "ready" || e.open(*current) != "account-B" {
		t.Fatal("recovery changed unaffected current account")
	}
}

func TestModelAuthConcurrentLoginAndTerminalResults(t *testing.T) {
	e := newModelAuthEnv(t)
	const workers = 12
	var wg sync.WaitGroup
	var winners atomic.Int32
	ids := make(chan uuid.UUID, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			o, err := e.s.NewModelAuthOperation(e.ctx, e.agent.ID, e.agent.ModelProvider, 1, time.Now().Add(time.Minute))
			if err == nil {
				winners.Add(1)
				ids <- o.ID
			} else if !errors.Is(err, store.ErrModelAuthBusy) {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if winners.Load() != 1 {
		t.Fatal("multiple active login owners")
	}
	id := <-ids
	// Completion/cancellation linearize under the operation lock; a losing callback never creates a second version.
	errs = make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := e.s.CompleteModelAuthOperation(e.ctx, id, nil, e.seal("oauth"))
		errs <- err
	}()
	go func() { defer wg.Done(); _, err := e.s.EndModelAuthOperation(e.ctx, id, "cancelled", ""); errs <- err }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	o, err := e.s.GetModelAuthOperation(e.ctx, e.agent.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	switch o.Status {
	case "succeeded":
		if e.countVersions() != 1 || e.config() != 2 {
			t.Fatal("success not atomic")
		}
	case "cancelled":
		if e.countVersions() != 0 || e.config() != 1 {
			t.Fatal("cancel saved credentials")
		}
	default:
		t.Fatal(fmt.Sprint("nonterminal result: ", o.Status))
	}
}
