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
