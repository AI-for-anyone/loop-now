//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 lark_integration_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestLarkSourceCASAndIndependentUserGrant、TestLarkWrongScopeIdentityNeverSeals

package cliauth

import (
	"context"
	"io"
	"log/slog"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/store"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
