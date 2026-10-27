package cliauth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/cliverify"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func managementOperation(t *testing.T, f googleFixture, account, version uuid.UUID) clicredential.VerificationGrantBinding {
	t.Helper()
	id := uuid.New()
	_, _, e := f.st.BeginCLIVerification(context.Background(), f.project, account, cliverify.Start{OperationID: id, ExpectedVersionID: version}, store.VerificationMetadata{TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: strings.Repeat("a", 64), AdapterRevision: "fixture", ProbeKind: "google_crm_read"}, "console", nil, time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	return clicredential.VerificationGrantBinding{ProjectID: f.project, AccountID: account, VersionID: version, OperationID: id}
}
func TestGoogleManagementAndAgentShareCancelledRotation(t *testing.T) {
	f := newGoogleFixture(t)
	ctx, cancelAll := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelAll()
	old := syntheticGoogle()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "A", f.write(t, old))
	if e != nil {
		t.Fatal(e)
	}
	a := clicredential.Binding{ProjectID: f.project, AgentID: f.agent, AccountID: r.View.ID, VersionID: *r.View.CurrentVersionID, CLI: "gcloud"}
	m := managementOperation(t, f, a.AccountID, a.VersionID)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	refresher := googleRefreshFunc(func(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return c, ctx.Err()
		}
		c.AccessToken = "synthetic-rotated-access"
		c.RefreshToken = "synthetic-rotated-refresh"
		rapt := "synthetic-rotated-rapt"
		c.RAPTToken = &rapt
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	})
	options := CoordinatorOptions{RefreshTimeout: 3 * time.Second, RecordTimeout: time.Second, WaitTimeout: 5 * time.Second, PollInterval: time.Millisecond}
	one, two := NewCoordinator(f.st, refresher, options, nil), NewCoordinator(f.st, refresher, options, nil)
	t.Cleanup(func() { one.Close(context.Background()); two.Close(context.Background()) })
	caller, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, e := one.ResolveVerification(caller, m); result <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("management rotation did not start")
	}
	cancel()
	if e = <-result; !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled management waiter", e)
	}
	if _, e = f.st.CancelCLIVerification(ctx, m.ProjectID, m.AccountID, m.OperationID); e != nil {
		t.Fatal(e)
	}
	next := old
	next.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	newer, e := f.st.MutateProjectCredential(ctx, f.project, r.View.ID, r.View.Revision, nil, ptrWrite(f.write(t, next)), false)
	if e != nil {
		t.Fatal(e)
	}
	agentResult := make(chan error, 1)
	go func() {
		c, e := two.Resolve(ctx, a)
		if e == nil && (c.RefreshToken != "synthetic-rotated-refresh" || c.RAPTToken == nil) {
			e = errors.New("rotation fields lost")
		}
		agentResult <- e
	}()
	close(release)
	if e = <-agentResult; e != nil {
		t.Fatal("Agent could not consume completed management rotation", e)
	}
	row, e := f.st.GetGoogleCLIGrant(ctx, a)
	if e != nil || row.Generation != 2 || calls.Load() != 1 || row.LeaseVerificationID != nil || row.RefreshState != "ready" {
		t.Fatal("shared rotation was not exactly once", e)
	}
	current, e := f.st.GetProjectCredential(ctx, f.project, r.View.ID)
	if e != nil || *current.View.CurrentVersionID != *newer.View.CurrentVersionID {
		t.Fatal("old management rotation changed current B", e)
	}
	if _, e = two.ResolveVerification(ctx, m); e == nil || calls.Load() != 1 {
		t.Fatal("cancelled operation accessed grant again")
	}
}
func TestGoogleManagementLeaseScopeAndProjectPurge(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "scope", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	m := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	lease := uuid.New()
	claimed, e := f.st.ClaimGoogleCLIVerificationRefresh(ctx, m, 1, lease, time.Now().Add(time.Minute))
	if e != nil || !claimed {
		t.Fatal("management claim", e)
	}
	seal := []byte("must-not-write")
	wrong := m
	wrong.OperationID = uuid.New()
	if e = f.st.CommitGoogleCLIVerificationRefresh(ctx, wrong, 1, lease, seal); e == nil {
		t.Fatal("fake operation accepted")
	}
	wrong = m
	wrong.ProjectID = uuid.New()
	if _, e = f.st.GetGoogleCLIVerificationGrant(ctx, wrong); e == nil {
		t.Fatal("cross-project accepted")
	}
	if e = f.st.CommitGoogleCLIVerificationRefresh(ctx, m, 2, lease, seal); e == nil {
		t.Fatal("wrong generation accepted")
	}
	if e = f.st.CommitGoogleCLIVerificationRefresh(ctx, m, 1, uuid.New(), seal); e == nil {
		t.Fatal("wrong lease accepted")
	}
	if _, e = f.st.DeleteProject(ctx, f.project); e != nil {
		t.Fatal("project delete", e)
	}
	if e = f.st.CommitGoogleCLIVerificationRefresh(ctx, m, 1, lease, seal); e == nil {
		t.Fatal("deleted project accepted rotation")
	}
	var written int
	if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions WHERE secret=$1`, seal).Scan(&written); e != nil || written != 0 {
		t.Fatal("unauthorized/late result wrote credential", e)
	}
	var version uuid.UUID
	var live *uuid.UUID
	var status string
	if e = f.st.Pool.QueryRow(ctx, `SELECT version_id,live_version_id,status FROM cli_credential_verifications WHERE id=$1`, m.OperationID).Scan(&version, &live, &status); e != nil || version != m.VersionID || live != nil || status != "cancelled" {
		t.Fatal("project purge lost safe operation history", e)
	}
}
func TestGoogleManagementBrokerCannotChooseAnotherPrincipal(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	c := syntheticGoogle()
	c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "broker", f.write(t, c))
	if e != nil {
		t.Fatal(e)
	}
	m := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	co := NewCoordinator(f.st, nil, CoordinatorOptions{}, nil)
	defer co.Close(ctx)
	broker, e := NewVerificationBroker(m, co)
	if e != nil {
		t.Fatal(e)
	}
	defer broker.Close(ctx)
	for _, spoof := range []bool{true, false} {
		conn, e := net.Dial("unix", broker.path)
		if e != nil {
			t.Fatal(e)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		frame := map[string]any{"protocol": 1, "type": "resolve", "requestId": uuid.New(), "capability": broker.capability}
		if spoof {
			frame["operationId"] = uuid.New()
		}
		json.NewEncoder(conn).Encode(frame)
		raw, e := io.ReadAll(conn)
		conn.Close()
		if e != nil {
			t.Fatal(e)
		}
		if spoof {
			if len(raw) != 0 {
				t.Fatal("child supplied principal accepted")
			}
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 || fields["accessToken"] == nil || fields["refreshToken"] != nil {
			t.Fatal("broker leaked non-access fields")
		}
		clear(raw)
	}
}

// Holding the grant row reproduces the window after the operation's SHARE lock
// and initial deadline check, without a fake clock or a refresh provider.
func TestGoogleManagementClaimWaitPastOperationDeadline(t *testing.T) {
	f := newGoogleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r, err := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "deadline", f.write(t, syntheticGoogle()))
	if err != nil {
		t.Fatal(err)
	}
	m := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	if _, err = f.st.Pool.Exec(ctx, `UPDATE cli_credential_verifications SET expires_at=clock_timestamp()+interval '600 milliseconds' WHERE id=$1`, m.OperationID); err != nil {
		t.Fatal(err)
	}
	lock, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	var generation int64
	if err = lock.QueryRow(ctx, `SELECT generation FROM google_cli_grants WHERE version_id=$1 FOR UPDATE`, m.VersionID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		claimed, e := f.st.ClaimGoogleCLIVerificationRefresh(ctx, m, generation, uuid.New(), time.Now().Add(time.Minute))
		if claimed {
			e = errors.New("expired operation consumed a new lease")
		}
		done <- e
	}()
	// Observe the real claimant waiting on the row; don't merely sleep and hope
	// that it started before expiry.
	for {
		var waiting bool
		err = f.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'SELECT v.account_label%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("claim did not wait on grant: %v", e)
		case <-ctx.Done():
			t.Fatal("claim lock wait not observed")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err = f.st.Pool.Exec(ctx, `SELECT pg_sleep(greatest(0,extract(epoch FROM expires_at-clock_timestamp()))+0.03) FROM cli_credential_verifications WHERE id=$1`, m.OperationID); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("expired claimant was not rejected")
	}
	var state string
	var lease *uuid.UUID
	if err = f.st.Pool.QueryRow(ctx, `SELECT refresh_state,lease_id FROM google_cli_grants WHERE version_id=$1`, m.VersionID).Scan(&state, &lease); err != nil || state != "ready" || lease != nil {
		t.Fatal("late claim mutated grant", err)
	}
}

type managementCommitFault struct {
	RefreshStore
	ManagementRefreshStore
	after bool
}

func (s managementCommitFault) CommitGoogleCLIVerificationRefresh(ctx context.Context, b clicredential.VerificationGrantBinding, g int64, lease uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.ManagementRefreshStore.CommitGoogleCLIVerificationRefresh(ctx, b, g, lease, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement unavailable")
}
func TestGoogleManagementCommitUnknownFailsClosed(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "write_rejected"
		if after {
			name = "commit_ack_lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newGoogleFixture(t)
			ctx := context.Background()
			r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "fault", f.write(t, syntheticGoogle()))
			if e != nil {
				t.Fatal(e)
			}
			b := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
			var calls atomic.Int32
			refresh := googleRefreshFunc(func(_ context.Context, c clicredential.Google) (clicredential.Google, error) {
				calls.Add(1)
				c.AccessToken = "synthetic-new"
				c.RefreshToken = "synthetic-new-refresh"
				c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
				return c, nil
			})
			faulty := managementCommitFault{RefreshStore: f.st, ManagementRefreshStore: f.st, after: after}
			one := NewCoordinator(faulty, refresh, CoordinatorOptions{RecordTimeout: time.Second, WaitTimeout: 2 * time.Second, PollInterval: time.Millisecond}, nil)
			defer one.Close(ctx)
			_, firstErr := one.ResolveVerification(ctx, b)
			var generation int64
			var state string
			if e = f.st.Pool.QueryRow(ctx, `SELECT generation,refresh_state FROM google_cli_grants WHERE version_id=$1`, b.VersionID).Scan(&generation, &state); e != nil {
				t.Fatal(e)
			}
			if after {
				if generation != 2 || state != "ready" {
					t.Fatal("committed rotation lost")
				}
			} else {
				if firstErr == nil || generation != 1 || state != "unavailable" {
					t.Fatal("uncommitted rotation was consumed", firstErr)
				}
			}
			two := NewCoordinator(f.st, refresh, CoordinatorOptions{WaitTimeout: time.Second, PollInterval: time.Millisecond}, nil)
			defer two.Close(ctx)
			_, e = two.ResolveVerification(ctx, b)
			if !after && e == nil {
				t.Fatal("uncertain grant resumed")
			}
			if after && e != nil {
				t.Fatal("committed grant not recoverable", e)
			}
			if calls.Load() != 1 {
				t.Fatal("old rotated refresh replayed")
			}
		})
	}
}
func TestGoogleManagementRequiresLoginPersistsToNewBroker(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "reauth", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	var calls atomic.Int32
	refresh := googleRefreshFunc(func(context.Context, clicredential.Google) (clicredential.Google, error) {
		calls.Add(1)
		return clicredential.Google{}, &SafeError{Code: "reauth_required"}
	})
	one := NewCoordinator(f.st, refresh, CoordinatorOptions{WaitTimeout: time.Second, PollInterval: time.Millisecond}, nil)
	if _, e = one.ResolveVerification(ctx, b); e == nil {
		t.Fatal("reauth accepted")
	}
	one.Close(ctx)
	two := NewCoordinator(f.st, refresh, CoordinatorOptions{WaitTimeout: time.Second, PollInterval: time.Millisecond}, nil)
	defer two.Close(ctx)
	broker, e := NewVerificationBroker(b, two)
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
	if e = json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "type": "resolve", "requestId": uuid.NewString(), "capability": broker.capability}); e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(conn)
	defer clear(raw)
	if e != nil {
		t.Fatal(e)
	}
	var reply map[string]any
	if json.Unmarshal(raw, &reply) != nil || reply["code"] != "requires_login" || reply["accessToken"] != nil || calls.Load() != 1 {
		t.Fatal("reauth lost across new broker or automatic refresh attempted")
	}
}
func TestGoogleManagementExpiredLeaseRejectsCommitAndNewRefresh(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "abandoned", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	lease := uuid.New()
	if ok, e := f.st.ClaimGoogleCLIVerificationRefresh(ctx, b, 1, lease, time.Now().Add(time.Minute)); e != nil || !ok {
		t.Fatal("claim", e)
	}
	if _, e = f.st.Pool.Exec(ctx, `UPDATE google_cli_grants SET lease_until=clock_timestamp()-interval '1 second' WHERE version_id=$1`, b.VersionID); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	if e = f.st.CommitGoogleCLIVerificationRefresh(ctx, b, 1, lease, []byte("must-not-write")); e == nil {
		t.Fatal("expired lease wrote credential")
	}
	if g, e := f.st.GetGoogleCLIVerificationGrant(ctx, b); e != nil || string(g.Secret) == "must-not-write" {
		t.Fatal("expired lease changed credential", e)
	}
	co := NewCoordinator(f.st, googleRefreshFunc(func(context.Context, clicredential.Google) (clicredential.Google, error) {
		calls.Add(1)
		return clicredential.Google{}, nil
	}), CoordinatorOptions{WaitTimeout: time.Second, PollInterval: time.Millisecond}, nil)
	defer co.Close(ctx)
	if _, e = co.ResolveVerification(ctx, b); e == nil || calls.Load() != 0 {
		t.Fatal("abandoned rotation retried old refresh")
	}
}

func TestGoogleManagementReloginReplacementInvalidatesOnlyAfterCommit(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "original", f.write(t, syntheticGoogle()))
	if e != nil {
		t.Fatal(e)
	}
	b := managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	revision := r.View.Revision
	loginID := uuid.New()
	if _, _, e = f.st.NewProjectCLIAuthOperation(ctx, f.project, cm.CLIAuthStart{OperationID: loginID, CLI: "gcloud", AuthType: "google_user", AccountID: &r.View.ID, ExpectedRevision: &revision}, time.Now().Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	var cancelled atomic.Int32
	f.st.VerificationInvalidated = func(p uuid.UUID, a *uuid.UUID) {
		if p != f.project || a == nil || *a != r.View.ID {
			t.Error("wrong cancellation scope")
		}
		cancelled.Add(1)
	}
	bad := f.write(t, syntheticGoogle())
	bad.Secret = nil // rejected inside the commit transaction, which rolls back
	if _, _, e = f.st.CompleteProjectCLIAuthOperation(ctx, f.project, loginID, bad); e == nil {
		t.Fatal("rollback fixture committed")
	}
	op, e := f.st.GetCLIVerification(ctx, f.project, r.View.ID, b.OperationID)
	if e != nil || op.Status != "pending" || cancelled.Load() != 0 {
		t.Fatal("failed login cancelled old verification", e)
	}
	if _, saved, e := f.st.CompleteProjectCLIAuthOperation(ctx, f.project, loginID, f.write(t, syntheticGoogle())); e != nil || saved == nil {
		t.Fatal("synthetic login commit failed", e)
	}
	op, e = f.st.GetCLIVerification(ctx, f.project, r.View.ID, b.OperationID)
	if e != nil || op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "target_changed" || cancelled.Load() != 1 {
		t.Fatal("successful replacement failed to stop original verification", e)
	}
}
