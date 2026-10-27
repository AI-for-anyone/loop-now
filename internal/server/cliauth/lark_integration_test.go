package cliauth

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

type larkLoginFunc func(context.Context, larkauth.Application, func(larkauth.Authorization) error) (clicredential.Lark, error)

func (f larkLoginFunc) Login(ctx context.Context, a larkauth.Application, on func(larkauth.Authorization) error) (clicredential.Lark, error) {
	return f(ctx, a, on)
}
func larkCredential(a larkauth.Application) clicredential.Lark {
	return clicredential.Lark{Type: "lark_user", AppSecret: a.AppSecret, AccessToken: "synthetic-lark-access", RefreshToken: "synthetic-lark-refresh", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshExpiresAt: time.Now().Add(24 * time.Hour).UnixMilli(), Identity: cm.LarkIdentity{Brand: a.Brand, AppID: a.AppID, OpenID: "ou-fixture", DisplayName: "已核实用户", VerifiedAt: time.Now()}, RuntimeVariantVersion: clicredential.LarkUserVariant}
}
func larkSource(t *testing.T, f googleFixture) store.ProjectCredentialRecord {
	t.Helper()
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	template, _ := catalog.Get("lark-cli")
	payload, fields, e := template.Render(map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"})
	if e != nil {
		t.Fatal(e)
	}
	plain, e := json.Marshal(payload)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	r, e := f.st.CreateProjectCredential(context.Background(), f.project, "cli", "lark-cli", "原应用", store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: fields, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}, Secret: plain})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func larkStart(source store.ProjectCredentialRecord) cm.CLIAuthStart {
	id := source.View.ID
	rev := source.View.Revision
	return cm.CLIAuthStart{OperationID: uuid.New(), CLI: "lark-cli", AuthType: "lark_user", SourceAccountID: &id, ExpectedSourceRevision: &rev}
}
func waitLark(t *testing.T, s *Service, project, id uuid.UUID, status string) cm.CLIAuthState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.Get(context.Background(), project, id)
		if e != nil {
			t.Fatal(e)
		}
		if v.Operation.Status == status {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Lark terminal deadline")
	return cm.CLIAuthState{}
}
func TestLarkSourceCASAndIndependentUserGrant(t *testing.T) {
	for _, scenario := range []string{"success_source_deleted_after_commit", "source_renamed_before_commit", "source_deleted_before_commit", "cancel_late_result"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGoogleFixture(t)
			ctx := context.Background()
			if e := f.st.MigrateThrough(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), 20); e != nil {
				t.Fatal(e)
			}
			source := larkSource(t, f)
			entered, release := make(chan struct{}), make(chan struct{})
			s := NewService(f.st, nil, nil, ServiceOptions{RecordTimeout: time.Second}, nil)
			t.Cleanup(func() { s.Close(ctx) })
			s.Lark = larkLoginFunc(func(ctx context.Context, a larkauth.Application, on func(larkauth.Authorization) error) (clicredential.Lark, error) {
				if a.Brand != "feishu" || a.AppID != "app-fixture" || a.AppSecret != "fixture-app-secret" {
					t.Error("source not resolved from captured application")
				}
				if e := on(larkauth.Authorization{URL: "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=public-fixture", ExpiresAt: time.Now().Add(time.Minute)}); e != nil {
					return clicredential.Lark{}, e
				}
				close(entered)
				<-release
				return larkCredential(a), nil
			})
			in := larkStart(source)
			first, e := s.Start(ctx, f.project, in)
			if e != nil {
				t.Fatal(e)
			}
			if first.Operation.ID != in.OperationID || first.Operation.SourceVersionID == nil || *first.Operation.SourceVersionID != *source.View.CurrentVersionID {
				t.Fatal("source version not captured")
			}
			<-entered
			if _, e = s.Start(ctx, f.project, in); e != nil {
				t.Fatal("same request should recover original operation")
			}
			if scenario == "source_renamed_before_commit" {
				name := "改名"
				if _, e = f.st.MutateProjectCredential(ctx, f.project, source.View.ID, source.View.Revision, &name, nil, false); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "source_deleted_before_commit" {
				if _, e = f.st.MutateProjectCredential(ctx, f.project, source.View.ID, source.View.Revision, nil, nil, true); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "cancel_late_result" {
				done := make(chan struct{})
				go func() { defer close(done); s.Cancel(ctx, f.project, in.OperationID) }()
				waitLark(t, s, f.project, in.OperationID, "cancelled")
				close(release)
				<-done
			} else {
				close(release)
			}
			want := "failed"
			if scenario == "success_source_deleted_after_commit" {
				want = "succeeded"
			}
			if scenario == "cancel_late_result" {
				want = "cancelled"
			}
			result := waitLark(t, s, f.project, in.OperationID, want)
			if result.Operation.AuthorizationURL != nil || result.Operation.UserCode != nil {
				t.Fatal("terminal retained authorization data")
			}
			var count int
			if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants`).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if want != "succeeded" {
				if count != 0 {
					t.Fatal("late/CAS result created a grant")
				}
				return
			}
			if count != 1 || result.Operation.AccountID == nil {
				t.Fatal("success not atomic")
			}
			userID := *result.Operation.AccountID
			if _, e = f.st.MutateProjectCredential(ctx, f.project, source.View.ID, source.View.Revision, nil, nil, true); e != nil {
				t.Fatal(e)
			}
			user, e := f.st.GetProjectCredential(ctx, f.project, userID)
			if e != nil || user.View.Status != "logged_in" || user.View.LarkIdentity == nil {
				t.Fatal("source deletion changed independent user grant")
			}
			// Direct storage branch can capture its own original application even
			// after source deletion. Public relogin policy is intentionally not opened.
			rev := user.View.Revision
			op, _, e := f.st.NewProjectCLIAuthOperation(ctx, f.project, cm.CLIAuthStart{OperationID: uuid.New(), CLI: "lark-cli", AuthType: "lark_user", AccountID: &userID, ExpectedRevision: &rev}, time.Now().Add(time.Minute))
			if e != nil {
				t.Fatal(e)
			}
			row, e := f.st.GetProjectLarkAuthSource(ctx, f.project, op.ID)
			if e != nil {
				t.Fatal(e)
			}
			saved, e := clicredential.DecodeLark(row.Secret)
			if e != nil || saved.AppSecret != "fixture-app-secret" {
				t.Fatal("relogin storage followed deleted source current")
			}
		})
	}
}
func TestLarkWrongScopeIdentityNeverSeals(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	if e := f.st.MigrateThrough(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), 20); e != nil {
		t.Fatal(e)
	}
	source := larkSource(t, f)
	in := larkStart(source)
	op, _, e := f.st.NewProjectCLIAuthOperation(ctx, f.project, in, time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = f.st.CompleteProjectCLIAuthOperation(ctx, f.project, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: []byte("must-not-write")})
	var written int
	if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions WHERE secret=$1`, []byte("must-not-write")).Scan(&written); e == nil || err != nil || written != 0 {
		t.Fatal("invalid identity wrote credential", err)
	}
}
