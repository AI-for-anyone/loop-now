package cliauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type googleFixture struct {
	st             *store.Store
	project, agent uuid.UUID
}

func newGoogleFixture(t *testing.T) googleFixture {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	conn, e := pgx.Connect(ctx, url)
	if e != nil {
		t.Fatal("TEST PG unavailable")
	}
	schema := "google_cli_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		defer conn.Close(ctx)
		if _, e := conn.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); e != nil {
			t.Error("TEST cleanup failed")
		}
	})
	st, e := store.Open(ctx, url, store.OpenOptions{SearchPath: schema})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(st.Close)
	if e = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); e != nil {
		t.Fatal(e)
	}
	defaults, e := st.EnsureDefaults(ctx, "Google TEST组织", "默认项目")
	if e != nil {
		t.Fatal(e)
	}
	p, e := st.CreateProject(ctx, defaults.Organization.ID, "Google隔离项目")
	if e != nil {
		t.Fatal(e)
	}
	a, e := st.CreateAgent(ctx, store.ProjectScope(p.ID), "Google测试", "openai", "fake-model", "")
	if e != nil {
		t.Fatal(e)
	}
	return googleFixture{st, p.ID, a.ID}
}
func syntheticGoogle() clicredential.Google {
	return clicredential.Google{Type: "authorized_user", Account: "synthetic@example.invalid", ClientID: "fixture-client", ClientSecret: "fixture-secret", RefreshToken: "fixture-refresh", AccessToken: "fixture-access", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(), Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}
}
func (f googleFixture) write(t *testing.T, c clicredential.Google) store.ProjectCredentialWrite {
	t.Helper()
	plain, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	label := c.Account
	return store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain}
}

type googleRefreshFunc func(context.Context, clicredential.Google) (clicredential.Google, error)

func (fn googleRefreshFunc) Refresh(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
	return fn(ctx, c)
}

type googleLoginFunc func(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error)

func (fn googleLoginFunc) Login(ctx context.Context, id uuid.UUID, url func(string) error) (clicredential.Google, error) {
	return fn(ctx, id, url)
}
func TestGooglePGConcurrentRotationAndCancelledWaiter(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	old := syntheticGoogle()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, old))
	if e != nil {
		t.Fatal(e)
	}
	b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AgentID: f.agent, ProjectID: f.project, AccountID: r.View.ID, CLI: "gcloud"}
	var calls atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	refresher := googleRefreshFunc(func(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return c, ctx.Err()
		}
		c.RefreshToken = "rotated"
		c.AccessToken = "new-access"
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		rapt := "new-rapt"
		c.RAPTToken = &rapt
		return c, nil
	})
	o := CoordinatorOptions{RefreshTimeout: time.Second * 3, RecordTimeout: time.Second, WaitTimeout: time.Second * 5, PollInterval: time.Millisecond}
	one, two := NewCoordinator(f.st, refresher, o, nil), NewCoordinator(f.st, refresher, o, nil)
	t.Cleanup(func() { _ = one.Close(ctx); _ = two.Close(ctx) })
	caller, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, e := one.Resolve(caller, b); first <- e }()
	<-entered
	cancel()
	if e = <-first; !errors.Is(e, context.Canceled) {
		t.Fatal("waiter was not cancelled")
	}
	// A current-pointer change cannot redirect the worker bound to V1.
	next := old
	next.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	newer, e := f.st.MutateProjectCredential(ctx, f.project, r.View.ID, r.View.Revision, nil, ptrWrite(f.write(t, next)), false)
	if e != nil {
		t.Fatal(e)
	}
	second := make(chan error, 1)
	go func() {
		c, e := two.Resolve(ctx, b)
		if e == nil && (c.RefreshToken != "rotated" || c.RAPTToken == nil) {
			e = errors.New("rotation fields lost")
		}
		second <- e
	}()
	close(release)
	if e = <-second; e != nil {
		t.Fatal("second coordinator failed", e)
	}
	row, e := f.st.GetGoogleCLIGrant(ctx, b)
	if e != nil || row.Generation != 2 || row.RefreshState != "ready" || calls.Load() != 1 {
		t.Fatal("rotation was not committed exactly once")
	}
	current, e := f.st.GetProjectCredential(ctx, f.project, r.View.ID)
	if e != nil || *current.View.CurrentVersionID != *newer.View.CurrentVersionID {
		t.Fatal("old rotation changed current pointer")
	}
	wrong := b
	wrong.ProjectID = uuid.New()
	if _, e = two.Resolve(ctx, wrong); e == nil {
		t.Fatal("cross-project resolve allowed")
	}
}
func ptrWrite(w store.ProjectCredentialWrite) *store.ProjectCredentialWrite { return &w }
func TestGoogleLoginStableIDAndLateCancel(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	helper := googleLoginFunc(func(ctx context.Context, id uuid.UUID, url func(string) error) (clicredential.Google, error) {
		calls.Add(1)
		close(entered)
		<-release
		c := syntheticGoogle()
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	})
	s := NewService(f.st, helper, nil, ServiceOptions{LoginTimeout: 3 * time.Second, RecordTimeout: time.Second}, nil)
	t.Cleanup(func() { s.Close(ctx) })
	name := "个人账号"
	in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gcloud", AuthType: "google_user", Name: &name}
	if _, e := s.Start(ctx, f.project, in); e != nil {
		t.Fatal(e)
	}
	<-entered
	if _, e := s.Start(ctx, f.project, in); e != nil {
		t.Fatal("stable replay", e)
	}
	other := name + "different"
	conflict := in
	conflict.Name = &other
	if _, e := s.Start(ctx, f.project, conflict); e == nil {
		t.Fatal("conflicting stable ID accepted")
	}
	cancelled := make(chan error, 1)
	go func() { _, e := s.Cancel(ctx, f.project, in.OperationID); cancelled <- e }()
	// Wait for actual PG cancellation commit, then release a late valid result.
	deadline := time.Now().Add(time.Second)
	for {
		op, e := f.st.GetProjectCLIAuthOperation(ctx, f.project, in.OperationID)
		if e != nil {
			t.Fatal(e)
		}
		if op.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if e := <-cancelled; e != nil {
		t.Fatal(e)
	}
	op, e := s.Get(ctx, f.project, in.OperationID)
	if e != nil || op.Operation.Status != "cancelled" || op.Operation.AuthorizationURL != nil || calls.Load() != 1 {
		t.Fatal("late login revived cancelled operation")
	}
	var count int
	if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions`).Scan(&count); e != nil || count != 0 {
		t.Fatal("cancelled login sealed a version")
	}
}

type commitFaultStore struct {
	RefreshStore
	after bool
}

func (s commitFaultStore) CommitGoogleCLIRefresh(ctx context.Context, b clicredential.Binding, g int64, lease uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.RefreshStore.CommitGoogleCLIRefresh(ctx, b, g, lease, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement unavailable")
}
func TestGooglePGCommitUnknownAndLeaseFailClosed(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "write_failure", true: "acknowledgement_lost"}[after], func(t *testing.T) {
			f := newGoogleFixture(t)
			ctx := context.Background()
			c := syntheticGoogle()
			r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, c))
			if e != nil {
				t.Fatal(e)
			}
			b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
			var calls atomic.Int64
			resolver := NewCoordinator(commitFaultStore{f.st, after}, googleRefreshFunc(func(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
				calls.Add(1)
				c.RefreshToken = "new-rotated"
				c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
				return c, nil
			}), CoordinatorOptions{PollInterval: time.Millisecond, WaitTimeout: 2 * time.Second}, nil)
			defer resolver.Close(ctx)
			_, e = resolver.Resolve(ctx, b)
			if after && e != nil || !after && e == nil {
				t.Fatal("unknown commit did not follow PG authority")
			}
			row, err := f.st.GetGoogleCLIGrant(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if after {
				if row.Generation != 2 || row.RefreshState != "ready" {
					t.Fatal("failure marker clobbered committed rotation")
				}
			} else if row.Generation != 1 || row.RefreshState != "unavailable" {
				t.Fatal("failed commit did not close uncertain grant")
			}
			_, _ = resolver.Resolve(ctx, b)
			if calls.Load() != 1 {
				t.Fatal("uncertain refresh token replayed")
			}
		})
	}
	t.Run("abandoned_lease", func(t *testing.T) {
		f := newGoogleFixture(t)
		ctx := context.Background()
		r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, syntheticGoogle()))
		if e != nil {
			t.Fatal(e)
		}
		b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
		if _, e = f.st.Pool.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_agent_id=$3,lease_until=clock_timestamp()-interval '1 second' WHERE version_id=$1`, b.VersionID, uuid.New(), b.AgentID); e != nil {
			t.Fatal(e)
		}
		var calls atomic.Int64
		c := NewCoordinator(f.st, googleRefreshFunc(func(context.Context, clicredential.Google) (clicredential.Google, error) {
			calls.Add(1)
			return clicredential.Google{}, errors.New("unexpected")
		}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
		defer c.Close(ctx)
		if _, e = c.Resolve(ctx, b); e == nil || calls.Load() != 0 {
			t.Fatal("abandoned lease reused refresh")
		}
		row, e := f.st.GetGoogleCLIGrant(ctx, b)
		if e != nil || row.RefreshState != "unavailable" {
			t.Fatal("abandoned lease not converged")
		}
	})
}

func TestGooglePGOwnerDeleteAndOverflowNeverRevive(t *testing.T) {
	t.Run("deleted_project", func(t *testing.T) {
		f := newGoogleFixture(t)
		ctx := context.Background()
		r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, syntheticGoogle()))
		if e != nil {
			t.Fatal(e)
		}
		b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
		entered, release := make(chan struct{}), make(chan struct{})
		c := NewCoordinator(f.st, googleRefreshFunc(func(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
			close(entered)
			<-release
			c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
			return c, nil
		}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
		done := make(chan error, 1)
		go func() { _, e := c.Resolve(ctx, b); done <- e }()
		<-entered
		if _, e = f.st.DeleteProject(ctx, f.project); e != nil {
			close(release)
			t.Fatal(e)
		}
		close(release)
		if e = <-done; e == nil {
			t.Fatal("deleted owner authorization returned")
		}
		if e = c.Close(ctx); e != nil {
			t.Fatal(e)
		}
		var versions, grants int
		if e = f.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions),(SELECT count(*) FROM google_cli_grants)`).Scan(&versions, &grants); e != nil || versions != 0 || grants != 0 {
			t.Fatal("late rotation resurrected project secrets")
		}
	})
	t.Run("generation_overflow", func(t *testing.T) {
		f := newGoogleFixture(t)
		ctx := context.Background()
		r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, syntheticGoogle()))
		if e != nil {
			t.Fatal(e)
		}
		b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
		lease := uuid.New()
		if _, e = f.st.Pool.Exec(ctx, `UPDATE google_cli_grants SET generation=9223372036854775807,refresh_state='refreshing',lease_id=$2,lease_agent_id=$3,lease_until=clock_timestamp()+interval '1 minute' WHERE version_id=$1`, b.VersionID, lease, b.AgentID); e != nil {
			t.Fatal(e)
		}
		before, e := f.st.GetGoogleCLIGrant(ctx, b)
		if e != nil {
			t.Fatal(e)
		}
		next := syntheticGoogle()
		next.RefreshToken = "must-rollback"
		plain, _ := next.Encode()
		if e = f.st.CommitGoogleCLIRefresh(ctx, b, int64(9223372036854775807), lease, plain); e == nil {
			t.Fatal("overflow committed")
		}
		after, e := f.st.GetGoogleCLIGrant(ctx, b)
		if e != nil || string(before.Secret) != string(after.Secret) || after.Generation != before.Generation {
			t.Fatal("overflow partially wrote credential")
		}
	})
}

func TestGooglePGRequiresLoginIsStableSafeClassification(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
	var calls atomic.Int64
	c := NewCoordinator(f.st, googleRefreshFunc(func(context.Context, clicredential.Google) (clicredential.Google, error) {
		calls.Add(1)
		return clicredential.Google{}, &SafeError{Code: "reauth_required"}
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	for i := 0; i < 2; i++ {
		_, e = c.Resolve(ctx, b)
		var safe *SafeError
		if !errors.As(e, &safe) || safe.Code != "requires_login" {
			t.Fatal("reauth classification lost")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("reauth_required token retried")
	}
	var newRefreshCalls atomic.Int64
	restarted := NewCoordinator(f.st, googleRefreshFunc(func(context.Context, clicredential.Google) (clicredential.Google, error) {
		newRefreshCalls.Add(1)
		return clicredential.Google{}, errors.New("unexpected refresh after requires_login")
	}), CoordinatorOptions{}, nil)
	defer restarted.Close(ctx)
	broker, e := NewBroker(b, restarted)
	if e != nil {
		t.Fatal(e)
	}
	defer broker.Close(ctx)
	conn, e := net.Dial("unix", broker.path)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "type": "resolve", "requestId": uuid.New(), "capability": broker.capability})
	raw, e := io.ReadAll(conn)
	if e != nil {
		t.Fatal(e)
	}
	var response struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Code != "requires_login" {
		t.Fatal("IPC reauth classification lost")
	}
	if newRefreshCalls.Load() != 0 {
		t.Fatal("new coordinator retried requires_login grant")
	}
}

// 删除账号时（迁移 030）没有进行中任务使用的版本连同授权一起清空：正在刷新的普通对话授权随之失效，
// 刷新结果不写回、不复活已删除的账号。
func TestGooglePGDeletedAccountDropsBoundInflightGrant(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AccountID: r.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
	entered, release := make(chan struct{}), make(chan struct{})
	c := NewCoordinator(f.st, googleRefreshFunc(func(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
		close(entered)
		<-release
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		c.RefreshToken = "retained-inflight-rotation"
		return c, nil
	}), CoordinatorOptions{PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	done := make(chan error, 1)
	go func() { _, e := c.Resolve(ctx, b); done <- e }()
	<-entered
	removed, e := f.st.MutateProjectCredential(ctx, f.project, r.View.ID, r.View.Revision, nil, nil, true)
	if e != nil {
		close(release)
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e == nil {
		t.Fatal("deleted account grant still resolved")
	}
	var grants int
	if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM google_cli_grants WHERE account_id=$1`, r.View.ID).Scan(&grants); e != nil || grants != 0 {
		t.Fatal("deleted account grant retained or rewritten", grants, e)
	}
	current, e := f.st.GetProjectCredential(ctx, f.project, r.View.ID)
	if e != nil || current.View.DeletedAt == nil || current.View.Revision != removed.View.Revision {
		t.Fatal("refresh revived deleted account")
	}
}
