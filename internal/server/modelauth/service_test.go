package modelauth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
)

type loginFunc func(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error)

func (f loginFunc) Login(ctx context.Context, id uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
	return f(ctx, id, notify)
}
func catalog() *modelcatalog.Catalog {
	return modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{{ID: "openai-codex", AuthMethods: []string{"oauth"}}, {ID: "anthropic", AuthMethods: []string{"api_key"}}})
}
func closeAuth(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := s.Close(ctx); e != nil {
		t.Error(e)
	}
}

type metadataQueryKey struct{}
type metadataTracer struct {
	version          uuid.UUID
	entered, release chan struct{}
	once             atomic.Bool
}

func (t *metadataTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if len(d.Args) > 0 && d.Args[0] == t.version && strings.Contains(d.SQL, "FROM model_auth_versions") {
		return context.WithValue(ctx, metadataQueryKey{}, true)
	}
	return ctx
}
func (t *metadataTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(metadataQueryKey{}) == true && t.once.CompareAndSwap(false, true) {
		close(t.entered)
		select {
		case <-t.release:
		case <-ctx.Done():
		}
	}
}
func TestStateNeverCombinesOldMetadataWithNewSuccessfulLogin(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "A", time.Now().Add(time.Hour)))
	originalPool := st.Pool
	trace := &metadataTracer{version: old.ID, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := originalPool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	st.Pool = pool
	t.Cleanup(func() { st.Pool = originalPool; pool.Close() })
	releaseLogin := make(chan struct{})
	svc := NewService(st, loginFunc(func(ctx context.Context, _ uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
		<-releaseLogin
		return oauth(t, "account-B", "B", time.Now().Add(time.Hour)), nil
	}), nil, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, svc)
	agent, e := st.GetAgent(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan State, 1)
	errs := make(chan error, 1)
	go func() { state, e := svc.State(ctx, a.ID); result <- state; errs <- e }()
	select {
	case <-trace.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata query not reached")
	}
	close(releaseLogin)
	waitAuth(t, func() bool {
		op, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
		return e == nil && op.Status == "succeeded"
	})
	close(trace.release)
	state := <-result
	if e = <-errs; e != nil {
		t.Fatal(e)
	}
	if state.Operation.Status == "succeeded" && state.Credential.VersionID != nil && *state.Credential.VersionID == old.ID {
		t.Fatal("success paired with pre-commit account metadata")
	}
	fresh, e := svc.State(ctx, a.ID)
	if e != nil || fresh.Operation.Status != "succeeded" || fresh.Credential.VersionID == nil || *fresh.Credential.VersionID == old.ID {
		t.Fatal("UI cannot observe new account", e)
	}
}

func TestLoginCommitDeadlineUsesFreshFailureContext(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "old", "old", time.Now().Add(time.Hour)))
	// The helper result arrives while the Agent row is held, so the commit outlives
	// its record deadline. The failure write locks only the operation and must
	// use a fresh context.
	locked := make(chan pgx.Tx, 1)
	var failureLogs bytes.Buffer
	svc := NewService(st, loginFunc(func(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
		lock, e := st.Pool.Begin(ctx)
		if e != nil {
			return modelcredential.Credential{}, e
		}
		locked <- lock
		if _, e = lock.Exec(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, a.ID); e != nil {
			return modelcredential.Credential{}, e
		}
		return oauth(t, "new", "new", time.Now().Add(time.Hour)), nil
	}), nil, catalog(), ServiceOptions{RecordTimeout: 100 * time.Millisecond}, slog.New(slog.NewJSONHandler(&failureLogs, nil)))
	defer closeAuth(t, svc)
	agent, e := st.GetAgent(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
	if e != nil {
		t.Fatal(e)
	}
	var lock pgx.Tx
	select {
	case lock = <-locked:
	case <-time.After(4 * time.Second):
		t.Fatal("login helper did not run")
	}
	defer lock.Rollback(ctx)
	waitAuth(t, func() bool {
		row, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
		return e == nil && row.Status == "failed"
	})
	if e = lock.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	pointer, _, e := st.CurrentModelAuthVersion(ctx, a.ID)
	if e != nil || pointer.ID != old.ID {
		t.Fatal("deadline replaced original grant", e)
	}
	closeAuth(t, svc)
	if !strings.Contains(failureLogs.String(), `"stage":"credential_commit"`) {
		t.Fatal("commit failure stage missing")
	}
}
func TestCancelledLoginRejectsLateSuccessAndKeepsOldAccount(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "old", "old", time.Now().Add(time.Hour)))
	entered, release := make(chan struct{}), make(chan struct{})
	svc := NewService(st, loginFunc(func(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
		close(entered)
		<-release
		return oauth(t, "new", "new", time.Now().Add(time.Hour)), nil
	}), nil, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, svc)
	agent, e := st.GetAgent(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	if _, e = svc.Cancel(ctx, a.ID, op.ID); e != nil {
		t.Fatal(e)
	}
	close(release)
	closeAuth(t, svc)
	pointer, _, e := st.CurrentModelAuthVersion(ctx, a.ID)
	if e != nil || pointer.ID != old.ID {
		t.Fatal("late login replaced old account", e)
	}
	view, e := svc.Get(ctx, a.ID, op.ID)
	if e != nil || view.Status != "cancelled" || view.AuthorizationURL != nil {
		t.Fatal("cancelled operation revived", e)
	}
}

func TestDeletedIdleAgentPurgesAllVersionCiphertextsAndEndsLogin(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	grant(t, st, a.ID, oauth(t, "A", "A", time.Now().Add(time.Hour)))
	grant(t, st, a.ID, oauth(t, "B", "B", time.Now().Add(time.Hour)))
	current, err := st.GetAgent(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.NewModelAuthOperation(ctx, a.ID, "openai-codex", current.ConfigVersion, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetModelAuthOperationURL(ctx, op.ID, "https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"); err != nil {
		t.Fatal(err)
	}
	if err := st.SoftDeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted Agent retained ciphertext", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_model_auth WHERE agent_id=$1`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted Agent retained pointer", err)
	}
	row, err := st.GetModelAuthOperation(ctx, a.ID, op.ID)
	if err != nil || row.Status != "cancelled" || row.AuthorizationURL != nil {
		t.Fatal("deleted Agent retained active login", err)
	}
	late, version, err := st.CompleteModelAuthOperation(ctx, op.ID, nil, []byte(`{"type":"oauth"}`))
	if err != nil || version != nil || late.Status != "cancelled" {
		t.Fatal("deleted Agent login revived", err)
	}
}

func TestRealHelperLoginEncryptedCommitAndSafeFailureDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantStage string
		success               bool
	}{
		{"flushed-success", `send({type:'result',id:frame.id,status:'succeeded',credential});process.exit(0);`, "", true},
		{"decode-failure", `send({type:'diagnostic',id:frame.id,stage:'exchange_http',httpStatus:200});credential.expires=Date.now()+0.5;send({type:'result',id:frame.id,status:'succeeded',credential});process.exit(0);`, "result_credential_decode", false},
		{"provider-failure", `send({type:'diagnostic',id:frame.id,stage:'exchange_http',httpStatus:401,category:'http_error'});send({type:'diagnostic',id:frame.id,stage:'secret-sentinel-stage',category:'secret-sentinel-category',httpStatus:999});send({type:'result',id:frame.id,status:'failed',code:'secret-sentinel-error'});process.exit(0);`, "helper_result_failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, a := authFixture(t)
			ctx := context.Background()
			old := grant(t, st, a.ID, oauth(t, "old-account", "old", time.Now().Add(time.Hour)))
			var logs bytes.Buffer
			svc := NewService(st, fakeGoHelper(t, tc.body), nil, catalog(), ServiceOptions{}, slog.New(slog.NewJSONHandler(&logs, nil)))
			agent, err := st.GetAgent(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			op, err := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
			if err != nil {
				t.Fatal(err)
			}
			waitAuth(t, func() bool {
				row, err := st.GetModelAuthOperation(ctx, a.ID, op.ID)
				return err == nil && terminal(row.Status)
			})
			closeAuth(t, svc)
			row, err := st.GetModelAuthOperation(ctx, a.ID, op.ID)
			if err != nil || row.AuthorizationURL != nil {
				t.Fatal("terminal URL not cleared", err)
			}
			pointer, _, err := st.CurrentModelAuthVersion(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.success {
				if row.Status != "succeeded" || pointer.ID == old.ID {
					t.Fatal("helper result not committed")
				}
				credential, err := modelcredential.Decode(pointer.Secret)
				if err != nil || credential.Kind() != "oauth" {
					t.Fatal("committed credential unavailable", err)
				}
			} else {
				if row.Status != "failed" || row.ErrorCode == nil || *row.ErrorCode != "helper_unavailable" || pointer.ID != old.ID {
					t.Fatal("failure replaced old grant or changed API code")
				}
				if !strings.Contains(logs.String(), `"stage":"`+tc.wantStage+`"`) {
					t.Fatal("fixed failure stage absent")
				}
				if !strings.Contains(logs.String(), `"providerStage":"exchange_http"`) {
					t.Fatal("provider stage missing")
				}
			}
			for _, secret := range []string{"fixture-access", "fixture-rotated", "secret-sentinel", "auth.openai.com", "code_challenge", "old-account"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("secret or raw provider value in log")
				}
			}
		})
	}
}
