package modelauth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/testutil/pgtest"
)

func TestProjectLoginRealOfflineHelperWindow(t *testing.T) {
	for _, scenario := range []string{"cancel", "success", "unsafe-url", "safe-provider-diagnostic"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			st := pgtest.NewStore(t)
			dc, err := st.EnsureDefaults(ctx, "组织", "项目")
			if err != nil {
				t.Fatal(err)
			}
			templates, err := clitemplate.Load("")
			if err != nil {
				t.Fatal(err)
			}
			body := `if(frame.type==='abort')send({type:'result',id:operation.id,status:'cancelled'});`
			switch scenario {
			case "success":
				body = `if(frame.type!=='abort')send({type:'result',id:frame.id,status:'succeeded',credential});`
			case "unsafe-url":
				body = `if(frame.type!=='abort'){send({type:'notify',id:frame.id,event:{type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=x&code_challenge=x&code=secret-url-sentinel'}});send({type:'result',id:frame.id,status:'succeeded',credential});}`
			case "safe-provider-diagnostic":
				body = `if(frame.type!=='abort'){send({type:'diagnostic',id:frame.id,stage:'exchange_http',category:'http_error',httpStatus:401});send({type:'result',id:frame.id,status:'failed',code:'provider-secret-sentinel'});}`
			}
			var logs bytes.Buffer
			svc := NewService(st, fakeGoHelper(t, body), nil, catalog(), ServiceOptions{LoginTimeout: time.Second * 3}, slog.New(slog.NewJSONHandler(&logs, nil)))
			defer closeAuth(t, svc)
			p := NewProjectService(svc, templates, 0)
			name := "离线OAuth"
			start, err := p.Start(ctx, dc.Project.ID, cm.LoginStart{Provider: "openai-codex", Name: &name})
			if err != nil {
				t.Fatal(err)
			}
			id := start.Operation.ID
			if scenario == "cancel" {
				waitAuth(t, func() bool {
					state, e := p.GetLogin(ctx, dc.Project.ID, id)
					return e == nil && state.Operation.Status == "awaiting_authorization" && state.Operation.AuthorizationURL != nil
				})
				again, err := p.Start(ctx, dc.Project.ID, cm.LoginStart{Provider: "openai-codex", Name: &name})
				if err != nil || again.Operation.ID != id {
					t.Fatal("active duplicate changed operation", err)
				}
				active, err := p.ListLogins(ctx, dc.Project.ID, "openai-codex", nil)
				if err != nil || len(active.Operations) != 1 || active.Operations[0].ID != id {
					t.Fatal("active operation recovery", err)
				}
				end, err := p.CancelLogin(ctx, dc.Project.ID, id)
				if err != nil || end.Operation.Status != "cancelled" || end.Operation.AuthorizationURL != nil {
					t.Fatal("cancel projection", err)
				}
				svc.mu.Lock()
				_, running := svc.operations[id]
				svc.mu.Unlock()
				if running {
					t.Fatal("cancel returned before offline helper resources drained")
				}
			} else {
				waitAuth(t, func() bool {
					state, e := p.GetLogin(ctx, dc.Project.ID, id)
					return e == nil && terminal(state.Operation.Status)
				})
			}
			final, err := p.GetLogin(ctx, dc.Project.ID, id)
			if err != nil || final.Operation.AuthorizationURL != nil {
				t.Fatal("terminal URL remained", err)
			}
			var secretsCount int
			if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions v JOIN project_credential_accounts a ON a.id=v.project_account_id WHERE a.project_id=$1 AND octet_length(v.secret)>0`, dc.Project.ID).Scan(&secretsCount); err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				if final.Operation.Status != "succeeded" || final.Account == nil || final.Account.CurrentVersionID == nil || secretsCount != 1 {
					t.Fatal("real helper did not commit account")
				}
			} else if secretsCount != 0 || final.Account != nil {
				t.Fatal("cancel/failure created account")
			}
			if scenario == "safe-provider-diagnostic" && !strings.Contains(logs.String(), `"providerStage":"exchange_http","category":"http_error","httpStatus":401`) {
				t.Fatal("fixed safe diagnostic missing")
			}
			if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "https://") || strings.Contains(logs.String(), "fixture-access") {
				t.Fatal("helper secret leaked in diagnostics")
			}
			active, err := p.ListLogins(ctx, dc.Project.ID, "", nil)
			if err != nil || len(active.Operations) != 0 {
				t.Fatal("terminal operation listed active", err)
			}
		})
	}
}
