package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
)

func TestLarkAppDeleteRollbackPreservesWorkerThenCommittedDeleteDrains(t *testing.T) {
	w := githubAppWorld(t)
	p := larktest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.Lark = p.Client
	ctx := context.Background()
	project := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "lark-lifecycle"}))
	base := "/projects/" + project.ID.String() + "/cli-auth/operations"
	source := decode[cm.Account](t, s.must(201, "POST", "/projects/"+project.ID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "source", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	attempt := p.Add("ou-not-persisted", "cancelled user", nil, "")
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	waitLarkApp(t, s, base, id, "awaiting_authorization")
	if _, err := s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION stop_lark_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER stop_lark_delete BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION stop_lark_delete()`); err != nil {
		t.Fatal(err)
	}
	s.must(500, "DELETE", "/projects/"+project.ID.String(), nil)
	// A live polling request after rollback proves no post-commit cancellation
	// hook ran early; status alone would not prove the worker remained alive.
	for until := time.Now().Add(3 * time.Second); p.TokenCalls.Load() == 0 && time.Now().Before(until); {
		time.Sleep(5 * time.Millisecond)
	}
	if p.TokenCalls.Load() == 0 {
		t.Fatal("rollback cancelled the pending protocol worker")
	}
	waitLarkApp(t, s, base, id, "awaiting_authorization")
	if _, err := s.a.Store.Pool.Exec(ctx, `DROP TRIGGER stop_lark_delete ON projects; DROP FUNCTION stop_lark_delete()`); err != nil {
		t.Fatal(err)
	}
	s.must(204, "DELETE", "/projects/"+project.ID.String(), nil)
	attempt.Release()
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.a.CLIAuth.Close(bounded); err != nil {
		t.Fatal("committed deletion failed to drain login worker")
	}
	var status string
	var url, code *string
	var versions, grants, runs int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT status,authorization_url,user_code FROM project_cli_auth_operations WHERE id=$1`, id).Scan(&status, &url, &code); err != nil {
		t.Fatal(err)
	}
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1),(SELECT count(*) FROM lark_cli_grants),(SELECT count(*) FROM runs)`, project.ID).Scan(&versions, &grants, &runs); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || url != nil || code != nil || versions+grants+runs != 0 || p.UserCalls.Load() != 0 || p.DeniedDials.Load() != 0 {
		t.Fatal("deleted operation revived, retained secrets or triggered work")
	}
}

func TestLarkAppLoginTimeoutClearsInteractionAndStopsPolling(t *testing.T) {
	w := githubAppWorld(t)
	p := larktest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.Lark = p.Client
	s.a.CLIAuth.Options.LoginTimeout = 150 * time.Millisecond
	_, base, _, source := larkAppSource(t, s)
	p.Add("ou-timeout", "timeout", nil, "")
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	state := waitLarkApp(t, s, base, id, "expired")
	if state.Operation.AuthorizationURL != nil || state.Operation.UserCode != nil || state.Account != nil {
		t.Fatal("expired login retained interaction or saved account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.a.CLIAuth.Close(ctx); err != nil {
		t.Fatal("timeout worker did not drain")
	}
	var grants int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants`).Scan(&grants); err != nil || grants != 0 || p.DeviceCalls.Load() != 1 || p.TokenCalls.Load() != 0 || p.UserCalls.Load() != 0 {
		t.Fatal("timed out worker polled or saved after terminal")
	}
}
