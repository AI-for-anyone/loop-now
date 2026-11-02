package modelauth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"

	"github.com/google/uuid"
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

// metadataTracer 让 State 读过版本元数据之后、发出下一条语句之前停住（只管带 metadataQueryKey 的上下文）。
// 语句钩子在语句之前调用，所以停在“下一条”之前，等价于原来停在元数据查询结束之后。
type metadataTracer struct {
	entered, release chan struct{}
	sawMeta, once    atomic.Bool
}

func (t *metadataTracer) hook(ctx context.Context, q string) {
	if ctx.Value(metadataQueryKey{}) != true {
		return
	}
	if !t.sawMeta.Load() {
		if strings.Contains(q, "FROM model_auth_versions") {
			t.sawMeta.Store(true)
		}
		return
	}
	if t.once.CompareAndSwap(false, true) {
		close(t.entered)
		select {
		case <-t.release:
		case <-ctx.Done():
		}
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

func TestStateNeverCombinesOldMetadataWithNewSuccessfulLogin(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "A", time.Now().Add(time.Hour)))
	trace := &metadataTracer{entered: make(chan struct{}), release: make(chan struct{})}
	st.Pool.SetQueryHook(trace.hook)
	t.Cleanup(func() { st.Pool.SetQueryHook(nil) })
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
	go func() {
		state, e := svc.State(context.WithValue(ctx, metadataQueryKey{}, true), a.ID)
		result <- state
		errs <- e
	}()
	// State 读过版本元数据后若还有下一次读取，就停在它前面，让登录在这之间提交；
	// 元数据若是最后一次读取（应有的顺序），State 读完就返回，没有可以与成功配对的旧元数据。
	succeed := func() {
		close(releaseLogin)
		waitAuth(t, func() bool {
			op, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
			return e == nil && op.Status == "succeeded"
		})
	}
	var state State
	select {
	case <-trace.entered:
		succeed()
		close(trace.release)
		state = <-result
	case state = <-result:
		succeed()
		close(trace.release)
	case <-time.After(3 * time.Second):
		t.Fatal("state read did not finish")
	}
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
	// The commit stops at its version write until the record deadline passes, so
	// it outlives that deadline and rolls back. The failure write runs in its own
	// transaction and must use a fresh context.
	var stalled atomic.Bool
	st.Pool.SetQueryHook(func(ctx context.Context, q string) {
		if strings.Contains(q, "INSERT INTO model_auth_versions") && stalled.CompareAndSwap(false, true) {
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
		}
	})
	t.Cleanup(func() { st.Pool.SetQueryHook(nil) })
	var failureLogs bytes.Buffer
	svc := NewService(st, loginFunc(func(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
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
	waitAuth(t, func() bool {
		row, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
		return e == nil && row.Status == "failed"
	})
	if !stalled.Load() {
		t.Fatal("commit did not reach the version write")
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
