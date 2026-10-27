package app_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// Management HTTP, single-account resolution and real execution capture share
// the same isolated App. No message is dispatched or CLI/provider contacted.
func TestProjectCLIAccountSavedState(t *testing.T) {
	db, err := url.Parse(pgtest.URL(t))
	if err != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("isolated TEST database required")
	}
	rd, err := redis.ParseURL(redistest.URL(t))
	if err != nil || rd.DB != 10 {
		t.Fatal("TEST Redis DB10 required")
	}
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	base := "/projects/" + dc.Project.ID.String() + "/credential-accounts"
	s.must(201, "POST", base, map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic model", "credential": map[string]any{"type": "api_key", "key": "synthetic-model"}})

	checkRead := func(t *testing.T, account cm.Account, want string) {
		t.Helper()
		got := decode[cm.Account](t, s.must(200, "GET", base+"/"+account.ID.String(), nil))
		if got.Status != want || got.CurrentVersionID == nil || *got.CurrentVersionID != *account.CurrentVersionID {
			t.Fatalf("account GET status=%s want=%s; version preserved=%t", got.Status, want, got.CurrentVersionID != nil && *got.CurrentVersionID == *account.CurrentVersionID)
		}
		list := decode[cm.AccountList](t, s.must(200, "GET", base+"?purpose=cli&cli="+*account.CLI, nil))
		for _, item := range list.Accounts {
			if item.ID == account.ID {
				if item.Status != want {
					t.Fatalf("list status=%s want=%s", item.Status, want)
				}
				return
			}
		}
		t.Fatal("account absent from list")
	}
	for _, tc := range []struct {
		cli    string
		fields map[string]string
	}{
		{"gh", map[string]string{"token": "synthetic-pat"}},
		{"lark-cli", map[string]string{"appId": "synthetic-app", "appSecret": "synthetic-secret", "brand": "feishu"}},
		{"mysql", map[string]string{"host": "127.0.0.1", "port": "1", "database": "fixture", "user": "fixture", "password": "synthetic-password", "sslMode": "DISABLED"}},
		{"psql", map[string]string{"host": "127.0.0.1", "port": "1", "database": "fixture", "user": "fixture", "password": "synthetic-password", "sslMode": "disable"}},
		{"gcloud", map[string]string{"serviceAccountJson": webDBConnectionsSAJSON(t, "status")}},
	} {
		t.Run(tc.cli, func(t *testing.T) {
			path, agent := s.createAgent("Status-" + tc.cli)
			s.must(200, "PUT", path+"/clis/"+tc.cli, nil)
			account := decode[cm.Account](t, s.must(201, "POST", base, map[string]any{"purpose": "cli", "cli": tc.cli, "name": "synthetic-" + tc.cli, "credential": map[string]any{"type": "cli", "fields": tc.fields}}))
			checkRead(t, account, "saved")
			if tc.cli == "gcloud" && (account.AuthMethod == nil || *account.AuthMethod != "service_account") {
				t.Fatal("service account authentication method changed")
			}
			choices := decode[cm.Selections](t, s.must(200, "GET", path+"/credential-selections", nil))
			found := false
			for _, choice := range choices.Selections {
				if choice.CLI != nil && *choice.CLI == tc.cli {
					found = choice.Status == "resolved" && choice.Mode == "automatic" && choice.ResolvedVersionID != nil && *choice.ResolvedVersionID == *account.CurrentVersionID
				}
			}
			if !found {
				t.Fatal("single-account automatic resolution unavailable or changed version")
			}
			snap, e := s.a.Config.ExecutionSnapshot(ctx, agent.ID)
			if e != nil {
				t.Fatalf("real execution capture rejected valid static credential: %v", e)
			}
			payload, ok := snap.CLIPayloads[tc.cli]
			if !ok || len(payload.Env)+len(payload.Files) == 0 {
				t.Fatal("capture omitted static CLI payload")
			}
			if tc.cli == "gh" && payload.Env["GH_TOKEN"] != tc.fields["token"] {
				t.Fatal("PAT capture mismatch")
			}
			if tc.cli == "gh" {
				s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": tc.cli, "accountId": account.ID, "expectedConfigVersion": choices.ConfigVersion})
				if _, e = s.a.Config.ExecutionSnapshot(ctx, agent.ID); e != nil {
					t.Fatal("explicit static account capture failed", e)
				}
				if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE project_cli_versions SET template_source='plugin:unavailable' WHERE id=$1`, *account.CurrentVersionID); e != nil {
					t.Fatal(e)
				}
				checkRead(t, account, "unavailable")
				if _, e = s.a.Config.ExecutionSnapshot(ctx, agent.ID); !errors.Is(e, agentconfig.ErrCredentialUnavailable) {
					t.Fatal("incompatible static credential accepted", e)
				}
				if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE project_cli_versions SET template_source='builtin' WHERE id=$1`, *account.CurrentVersionID); e != nil {
					t.Fatal(e)
				}
			}
			s.must(412, "PUT", base+"/"+account.ID.String()+"/credential", map[string]any{"expectedRevision": account.Revision + 1, "credential": map[string]any{"type": "cli", "fields": tc.fields}})
			checkRead(t, account, "saved")
			var count int
			if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions WHERE account_id=$1`, account.ID).Scan(&count); e != nil || count != 1 {
				t.Fatal("stale CAS changed original version", e)
			}
		})
	}

	t.Run("google-oauth-states", func(t *testing.T) {
		path, agent := s.createAgent("OAuthStatus")
		s.must(200, "PUT", path+"/clis/gcloud", nil)
		g := clicredential.Google{Type: "authorized_user", Account: "fixture@example.invalid", ClientID: "synthetic-client", ClientSecret: "synthetic-secret", RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"fixture"}}
		plain, e := g.Encode()
		if e != nil {
			t.Fatal(e)
		}
		defer clear(plain)
		account, e := s.a.Store.CreateProjectCredential(ctx, dc.Project.ID, "cli", "gcloud", "synthetic OAuth", store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &g.Account, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
		if e != nil {
			t.Fatal(e)
		}
		checkRead(t, account.View, "logged_in")
		choices := decode[cm.Selections](t, s.must(200, "GET", path+"/credential-selections", nil))
		s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "gcloud", "accountId": account.View.ID, "expectedConfigVersion": choices.ConfigVersion})
		for _, state := range []string{"ready", "refreshing", "unavailable"} {
			var lease, owner *uuid.UUID
			var until *time.Time
			if state == "refreshing" {
				l := uuid.New()
				lease = &l
				owner = &agent.ID
				u := time.Now().Add(time.Minute)
				until = &u
			}
			if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE google_cli_grants SET refresh_state=$2,lease_id=$3,lease_agent_id=$4,lease_until=$5 WHERE version_id=$1`, *account.View.CurrentVersionID, state, lease, owner, until); e != nil {
				t.Fatal(e)
			}
			want := "logged_in"
			if state == "unavailable" {
				want = "requires_login"
			}
			checkRead(t, account.View, want)
			snap, e := s.a.Config.ExecutionSnapshot(ctx, agent.ID)
			if state == "unavailable" {
				if !errors.Is(e, agentconfig.ErrCredentialUnavailable) {
					t.Fatal("unavailable OAuth accepted capture", e)
				}
			} else if e != nil || snap.GoogleBinding == nil || snap.GoogleBinding.VersionID != *account.View.CurrentVersionID {
				t.Fatal("OAuth capture changed binding", e)
			}
		}
		if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL WHERE id=$1`, account.View.ID); e != nil {
			t.Fatal(e)
		}
		got := decode[cm.Account](t, s.must(200, "GET", base+"/"+account.View.ID.String(), nil))
		if got.Status != "unavailable" || got.CurrentVersionID != nil {
			t.Fatal("no-version account became saved")
		}
		if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE project_credential_accounts SET deleted_at=now() WHERE id=$1`, account.View.ID); e != nil {
			t.Fatal(e)
		}
		got = decode[cm.Account](t, s.must(200, "GET", base+"/"+account.View.ID.String(), nil))
		if got.Status != "deleted" {
			t.Fatal("deleted account status lost")
		}
		if _, e = s.a.Config.ExecutionSnapshot(ctx, agent.ID); !errors.Is(e, agentconfig.ErrCredentialUnavailable) {
			t.Fatal("deleted explicit account fell back to service account", e)
		}
	})
	for _, table := range []string{"runs", "task_turns", "usage_records", "project_cli_auth_operations"} {
		var count int
		if err := s.a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected execution/login records in %s: count=%d err=%v", table, count, err)
		}
	}
}
