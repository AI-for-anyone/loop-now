package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
)

func larkSwitchTarget(t *testing.T, s *store.Store, project uuid.UUID, refreshExpiry time.Time) store.ProjectCredentialRecord {
	return larkSwitchTargetWithScopes(t, s, project, refreshExpiry, []string{"offline_access", "im:message", "im:message.send_as_user", "im:message.group_msg:get_as_user"})
}

func larkSwitchTargetWithScopes(t *testing.T, s *store.Store, project uuid.UUID, refreshExpiry time.Time, scopes []string) store.ProjectCredentialRecord {
	t.Helper()
	identity := credentialmodel.LarkIdentity{Brand: "feishu", AppID: "synthetic-app", OpenID: "synthetic-user", DisplayName: "Synthetic User", GrantedScopes: scopes, VerifiedAt: time.Now().UTC()}
	credential := clicredential.Lark{Type: "lark_user", AppSecret: "synthetic-secret", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshExpiresAt: refreshExpiry.UnixMilli(), Identity: identity, RuntimeVariantVersion: clicredential.LarkUserVariant}
	plain, err := credential.Encode()
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.CreateProjectCredential(context.Background(), project, "cli", "lark-cli", "synthetic login", store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &identity.OpenID, LarkIdentity: &identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1.0.94"}, Secret: plain})
	clear(plain)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestTaskLarkTargetPreservesActualScopeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
	}{
		{"unknown", nil},
		{"known_empty", []string{}},
		{"known_subset", []string{"offline_access"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, in := larkFenceFixture(t)
			ctx := context.Background()
			record, err := s.GetTask(ctx, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			account := larkSwitchTargetWithScopes(t, s, record.View.Task.ProjectID, time.Now().Add(time.Hour), tc.scopes)
			_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				m.Unchanged()
				got, err := s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, *account.View.CurrentVersionID, account.View.Revision)
				if err != nil {
					return nil, err
				}
				if !slices.Equal(got.Identity.GrantedScopes, tc.scopes) || (got.Identity.GrantedScopes == nil) != (tc.scopes == nil) {
					t.Fatal("actual grant metadata replaced with requested scopes")
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal("valid login rejected based on grant metadata", err)
			}
		})
	}
}

func TestTaskLarkTargetPinnedVersionAndGeneration(t *testing.T) {
	s, reservation := larkFenceFixture(t)
	ctx := context.Background()
	r, err := s.GetTask(ctx, reservation.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	account := larkSwitchTarget(t, s, r.View.Task.ProjectID, time.Now().Add(time.Hour))
	version := *account.View.CurrentVersionID
	check := func(expected int64, want bool) {
		t.Helper()
		_, err := s.MutateTask(ctx, reservation.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			got, err := s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, version, expected)
			if err == nil && (got.VersionID != version || got.Identity.OpenID != "synthetic-user") {
				t.Fatal("target identity changed")
			}
			return nil, err
		})
		if (err == nil) != want {
			t.Fatalf("target accepted=%v want=%v: %v", err == nil, want, err)
		}
	}
	check(account.View.Revision, true)
	if _, err = s.Pool.Exec(ctx, `UPDATE lark_cli_grants SET generation=generation+1 WHERE version_id=$1`, version); err != nil {
		t.Fatal(err)
	}
	check(account.View.Revision, true)
	check(account.View.Revision+1, false)
	if _, err = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET revision=revision+1 WHERE id=$1`, account.View.ID); err != nil {
		t.Fatal(err)
	}
	check(account.View.Revision, false)
}

func TestTaskLarkTargetExpiredOrUnavailableIsNotRefreshed(t *testing.T) {
	s, reservation := larkFenceFixture(t)
	ctx := context.Background()
	r, _ := s.GetTask(ctx, reservation.TaskID)
	account := larkSwitchTarget(t, s, r.View.Task.ProjectID, time.Now().Add(-time.Second))
	_, err := s.MutateTask(ctx, reservation.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, err := s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, *account.View.CurrentVersionID, account.View.Revision)
		return nil, err
	})
	if err == nil {
		t.Fatal("expired refresh authorization accepted")
	}
	var generation int64
	var state string
	if err = s.Pool.QueryRow(ctx, `SELECT generation,refresh_state FROM lark_cli_grants WHERE version_id=$1`, *account.View.CurrentVersionID).Scan(&generation, &state); err != nil || generation != 1 || state != "ready" {
		t.Fatal("local selection started a provider refresh", generation, state, err)
	}
}

func TestTaskLarkTargetLockWaitCannotCrossGrantExpiry(t *testing.T) {
	s, reservation := larkFenceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, _ := s.GetTask(ctx, reservation.TaskID)
	expires := time.Now().Add(time.Second)
	account := larkSwitchTarget(t, s, r.View.Task.ProjectID, expires)
	holder, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM project_cli_versions WHERE id=$1 FOR UPDATE`, *account.View.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	pid := make(chan uint32, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.MutateTask(ctx, reservation.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			pid <- m.Tx.Conn().PgConn().PID()
			_, err := s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, *account.View.CurrentVersionID, account.View.Revision)
			return nil, err
		})
		done <- err
	}()
	var backend uint32
	select {
	case backend = <-pid:
	case <-ctx.Done():
		t.Fatal("task lock not acquired")
	}
	for {
		var waiting bool
		if err = s.Pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, backend).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("did not observe actual target row lock wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if until := time.Until(expires.Add(20 * time.Millisecond)); until > 0 {
		time.Sleep(until)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("transaction-start time allowed an expired grant after lock wait")
		}
	case <-ctx.Done():
		t.Fatal("target validation did not complete")
	}
}
