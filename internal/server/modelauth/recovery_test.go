package modelauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

type lostClaimAck struct{ *store.Store }

func (s lostClaimAck) ClaimModelAuthRefresh(ctx context.Context, id uuid.UUID, generation int64, attempt uuid.UUID) (bool, error) {
	claimed, err := s.Store.ClaimModelAuthRefresh(ctx, id, generation, attempt)
	if err != nil {
		return false, err
	}
	if claimed {
		return false, errors.New("synthetic lost commit acknowledgement")
	}
	return false, nil
}
func TestClaimAckLossRecoversWithoutRestartOrProviderReplay(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "A", "old", time.Now().Add(-time.Hour)))
	var calls atomic.Int32
	c := NewCoordinator(lostClaimAck{st}, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return modelcredential.Credential{}, nil
	}), CoordinatorOptions{RecoveryInterval: 5 * time.Millisecond}, nil)
	defer c.Close(ctx)
	if _, err := c.Resolve(ctx, binding(v)); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal(err)
	}
	row, err := st.GetModelAuthVersion(ctx, v.ID)
	if err != nil || row.RefreshState != "refreshing" {
		t.Fatal("claim was not durably committed", err)
	}
	// Advance only the persisted abandoned attempt's age, not credential expiry
	// or the coordinator's wall clock; no active provider operation exists.
	if _, err := st.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_started_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	waitAuth(t, func() bool {
		row, err := st.GetModelAuthVersion(ctx, v.ID)
		return err == nil && row.RefreshState == "unavailable"
	})
	if _, err := c.Resolve(ctx, binding(v)); !errors.Is(err, ErrCredentialUnavailable) || calls.Load() != 0 {
		t.Fatal("abandoned claim retried old token", err)
	}
}

type unavailableWrites struct {
	*store.Store
	down   atomic.Bool
	failed atomic.Bool
}

func (s *unavailableWrites) CommitModelAuthRefresh(context.Context, uuid.UUID, uuid.UUID, []byte) (bool, error) {
	return false, errors.New("synthetic commit response loss")
}
func (s *unavailableWrites) FailModelAuthRefresh(ctx context.Context, id, attempt uuid.UUID, code string) error {
	s.failed.Store(true)
	if s.down.Load() {
		return errors.New("synthetic persistence outage")
	}
	return s.Store.FailModelAuthRefresh(ctx, id, attempt, code)
}
func (s *unavailableWrites) RecoverStaleModelAuthRefresh(ctx context.Context, before time.Time) (int64, error) {
	if s.down.Load() {
		return 0, errors.New("synthetic persistence outage")
	}
	return s.Store.RecoverStaleModelAuthRefresh(ctx, before)
}
func TestCommitAndFailureWriteOutageRecoversAfterPGReturnsWithoutRestart(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "A", "old", time.Now().Add(-time.Hour)))
	failing := &unavailableWrites{Store: st}
	failing.down.Store(true)
	var calls atomic.Int32
	c := NewCoordinator(failing, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		calls.Add(1)
		return oauth(t, "A", "new", time.Now().Add(time.Hour)), nil
	}), CoordinatorOptions{RecoveryInterval: 5 * time.Millisecond, PollInterval: time.Millisecond, WaitTimeout: time.Second}, nil)
	defer c.Close(ctx)
	if _, err := c.Resolve(ctx, binding(v)); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("outage was not bounded", err)
	}
	if !failing.failed.Load() {
		t.Fatal("terminal persistence failure not injected")
	}
	row, err := st.GetModelAuthVersion(ctx, v.ID)
	if err != nil || row.RefreshState != "refreshing" {
		t.Fatal("double failure did not retain durable attempt", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_started_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	failing.down.Store(false)
	waitAuth(t, func() bool {
		row, err := st.GetModelAuthVersion(ctx, v.ID)
		return err == nil && row.RefreshState == "unavailable"
	})
	row, _ = st.GetModelAuthVersion(ctx, v.ID)
	if row.ErrorCode == nil || *row.ErrorCode != "refresh_uncertain" || calls.Load() != 1 {
		t.Fatal("PG recovery retried old provider token")
	}
	service := NewService(st, nil, c, catalog(), ServiceOptions{}, nil)
	meta, err := service.Metadata(ctx, a.ID)
	if err != nil || meta.Status != "unavailable" {
		t.Fatal("recovered failure still appeared set", err)
	}
	if _, err := c.Resolve(ctx, binding(v)); !errors.Is(err, ErrCredentialUnavailable) || calls.Load() != 1 {
		t.Fatal("uncertain token replayed", err)
	}
}

type monitoredRecovery struct {
	*store.Store
	ticks chan struct{}
}

func (s monitoredRecovery) RecoverStaleModelAuthRefresh(ctx context.Context, before time.Time) (int64, error) {
	count, err := s.Store.RecoverStaleModelAuthRefresh(ctx, before)
	select {
	case s.ticks <- struct{}{}:
	default:
	}
	return count, err
}
func TestRecoverySupervisorLeavesActiveAttemptWithinBudgetUntouched(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	v := grant(t, st, a.ID, oauth(t, "A", "old", time.Now().Add(-time.Hour)))
	entered, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	unblock := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	monitored := monitoredRecovery{Store: st, ticks: make(chan struct{}, 20)}
	c := NewCoordinator(monitored, refreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		close(entered)
		<-release
		return oauth(t, "A", "new", time.Now().Add(time.Hour)), nil
	}), CoordinatorOptions{RecoveryInterval: 5 * time.Millisecond, PollInterval: time.Millisecond}, nil)
	defer c.Close(ctx)
	defer unblock()
	done := make(chan error, 1)
	go func() { _, err := c.Resolve(ctx, binding(v)); done <- err }()
	<-entered
	// Place a live attempt just inside the full effective horizon and run several
	// actual PG sweeps. The supervisor must not take it over before its deadline.
	horizon := c.Options.RefreshTimeout + c.Options.RefreshDrainTimeout + 2*c.Options.RecordTimeout + 2*time.Second
	if _, err := st.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_started_at=$2 WHERE id=$1`, v.ID, time.Now().Add(-horizon+time.Second)); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		select {
		case <-monitored.ticks:
		case <-time.After(time.Second):
			t.Fatal("recovery supervisor did not run")
		}
	}
	row, err := st.GetModelAuthVersion(ctx, v.ID)
	if err != nil || row.RefreshState != "refreshing" {
		t.Fatal("active attempt was fenced early", err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal("active attempt could not commit", err)
	}
	row, err = st.GetModelAuthVersion(ctx, v.ID)
	if err != nil || row.RefreshState != "ready" || row.Generation != 2 {
		t.Fatal("active attempt lost its token", err)
	}
}
