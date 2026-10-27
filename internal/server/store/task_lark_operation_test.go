package store_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

// Explicit test-only native injection: proves tasks orchestration with real
// PG/local leases, not broker/native readiness or a complete App acceptance.
type larkOperationNative struct {
	acquires, releases atomic.Int32
	seal               func() error
	acquireErr         error
}
type larkOperationNativeLease struct{ owner *larkOperationNative }

func (n *larkOperationNative) Acquire(context.Context, []uuid.UUID) (tasks.TaskLarkNativeLease, error) {
	n.acquires.Add(1)
	if n.acquireErr != nil {
		return nil, n.acquireErr
	}
	return &larkOperationNativeLease{n}, nil
}

func TestTaskLarkOperationBusyConsumerOrLocalGateCannotPublish(t *testing.T) {
	for _, mode := range []string{"consumer", "local"} {
		t.Run(mode, func(t *testing.T) {
			s, in := larkSwitchReadyFixture(t, nil)
			n := &larkOperationNative{}
			svc, ex := larkOperationService(t, s, n)
			ctx := context.Background()
			if mode == "consumer" {
				n.acquireErr = tasks.ErrTaskLarkNativeBusy
			} else {
				old, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{in.Members[0].RuntimeID})
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
			}
			if _, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in)); !apperr.Is(err, "task_conflict") {
				t.Fatal("busy handoff not refused", err)
			}
			state, err := svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
			if err != nil || state.Status != "failed" || state.BindingRevision != nil {
				t.Fatal("busy action published", err)
			}
			var fences, groups int
			if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings)`).Scan(&fences, &groups); err != nil || fences+groups != 0 {
				t.Fatal("safe busy abort left partial state", err)
			}
		})
	}
}
func (l *larkOperationNativeLease) Seal(context.Context) error {
	if l.owner.seal != nil {
		return l.owner.seal()
	}
	return nil
}
func (l *larkOperationNativeLease) Release() { l.owner.releases.Add(1) }

func larkOperationInput(in store.TaskLarkFenceReservation) taskmodel.TaskLarkSwitchInput {
	out := taskmodel.TaskLarkSwitchInput{ActionID: in.ActionID, ExpectedTaskVersion: in.ExpectedTaskVersion, ExpectedNodeRunID: in.NodeRunID, ExpectedLarkBindingRevision: in.Capture.ExpectedBindingRevision, TargetAccountID: in.Capture.Target.AccountID, TargetVersionID: in.Capture.Target.VersionID, ExpectedAccountRevision: in.Capture.Target.AccountRevision}
	for _, member := range in.Capture.Members {
		out.ExpectedMembers = append(out.ExpectedMembers, taskmodel.TaskLarkSwitchMemberInput{AgentID: member.AgentID, RuntimeID: member.RuntimeID, ExpectedBindingRevision: member.PreviousRevision})
		if member.Role == "publisher" {
			out.ExpectedAgentID, out.ExpectedRuntimeID = member.AgentID, member.RuntimeID
		}
	}
	return out
}
func larkOperationService(t *testing.T, s *store.Store, n *larkOperationNative) (*tasks.Service, *local.Executor) {
	t.Helper()
	ex := local.New(local.Options{WorkRoot: t.TempDir()}, nil, nil, nil, nil)
	svc := tasks.New(s, nil, ex, tasks.Options{RecordTimeout: 100 * time.Millisecond}, nil)
	svc.LarkSwitchNative = n
	t.Cleanup(func() { _ = svc.Stop(context.Background()); _ = ex.Shutdown(context.Background()) })
	return svc, ex
}

func TestTaskLarkOperationConcreteLeaseAndUniqueC(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	n := &larkOperationNative{}
	svc, _ := larkOperationService(t, s, n)
	input := larkOperationInput(in)
	ctx := context.Background()
	receipt, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, input)
	if err != nil || receipt.Status != "succeeded" || receipt.Continuation == nil || receipt.BindingRevision == nil || receipt.DispatchStatus == nil || *receipt.DispatchStatus != "queued" {
		t.Fatal("actual PG/local service publish", err)
	}
	if n.acquires.Load() != 1 || n.releases.Load() != 1 {
		t.Fatal("native lifecycle count")
	}
	svc.LarkSwitchNative = nil // original action survives current runtime unavailability
	replayed, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, input)
	if err != nil || replayed.Continuation == nil || replayed.Continuation.RunID != receipt.Continuation.RunID || n.acquires.Load() != 1 {
		t.Fatal("same action repeated handoff/C", err)
	}
	var groups, members, turns, fences int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns WHERE task_id=$1),(SELECT count(*) FROM task_lark_switch_fences)`, in.TaskID).Scan(&groups, &members, &turns, &fences); err != nil || groups != 1 || members != 2 || turns != 2 || fences != 0 {
		t.Fatal("partial or repeated C", groups, members, turns, fences, err)
	}
}

func TestTaskLarkOperationSealUnknownRetainsRealLocalGate(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	n := &larkOperationNative{seal: func() error { return errors.New("synthetic unknown seal") }}
	svc, ex := larkOperationService(t, s, n)
	ctx := context.Background()
	receipt, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in))
	if err != nil || receipt.Status != "handoff" || receipt.BindingRevision != nil || n.releases.Load() != 0 {
		t.Fatal("unknown stop/seal became released/success", err)
	}
	for _, member := range in.Members {
		if lease, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{member.RuntimeID}); !errors.Is(err, local.ErrRuntimeBusy) || lease != nil {
			t.Fatal("unknown seal released runtime", err)
		}
	}
	var fences, groups int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings)`).Scan(&fences, &groups); err != nil || fences != 2 || groups != 0 {
		t.Fatal("unknown pre-C changed binding", err)
	}
}

func TestTaskLarkOperationLateSealConfirmationAbortsInsteadOfRunningQA(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	var calls atomic.Int32
	n := &larkOperationNative{seal: func() error {
		if calls.Add(1) == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}}
	svc, _ := larkOperationService(t, s, n)
	receipt, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in))
	if err != nil || receipt.Status != "handoff" {
		t.Fatal(err)
	}
	if err = svc.ReconcileLarkSwitchOutcomes(ctx); err != nil {
		t.Fatal(err)
	}
	receipt, err = svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
	if err != nil || receipt.Status != "failed" || receipt.BindingRevision != nil || n.acquires.Load() != 1 || n.releases.Load() != 1 {
		t.Fatal("late cleanup confirmation continued QA", err)
	}
	var groups, turns int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, in.TaskID).Scan(&groups, &turns); err != nil || groups != 0 || turns != 1 {
		t.Fatal("pre-C unknown restarted C", err)
	}
}

func TestTaskLarkOperationUnknownCConfirmsOriginalActionOnly(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	var saturated []*pgxpool.Conn
	defer func() {
		for _, c := range saturated {
			c.Release()
		}
	}()
	n := &larkOperationNative{}
	n.seal = func() error {
		// Production commit relay runs after C commits and returns its pool
		// connection. Withhold every real pool connection before confirmation.
		s.TaskCommitted = func() {
			for i := 0; i < int(s.Pool.Config().MaxConns); i++ {
				c, err := s.Pool.Acquire(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				saturated = append(saturated, c)
			}
		}
		return nil
	}
	svc, ex := larkOperationService(t, s, n)
	_, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in))
	if err == nil || n.releases.Load() != 0 {
		t.Fatal("unconfirmed C released gates", err)
	}
	s.TaskCommitted = nil
	for _, c := range saturated {
		c.Release()
	}
	saturated = nil
	var fences, groups int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings)`).Scan(&fences, &groups); err != nil || fences != 0 || groups != 1 {
		t.Fatal("test did not commit C before confirmation failed", err)
	}
	for _, member := range in.Members {
		if lease, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{member.RuntimeID}); !errors.Is(err, local.ErrRuntimeBusy) || lease != nil {
			t.Fatal("C unknown lost memory gate", err)
		}
	}
	committed, err := svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
	if err != nil || committed.Continuation == nil {
		t.Fatal(err)
	}
	capability, err := svc.ConsoleMessageCapability(ctx, auth.Console(), in.TaskID, committed.Continuation.NodeRunID)
	if err != nil || capability.CanSend {
		t.Fatal("unknown C exposed strict send as available", err)
	}
	if _, err = svc.SendNodeInboxMessage(ctx, auth.Console(), in.TaskID, committed.Continuation.NodeRunID, taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: committed.SourceAgentID, ExpectedRuntimeID: committed.SourceRuntimeID, Text: "must retain draft", WhenBusy: "queue"}, 1000); !apperr.Is(err, "task_conflict") {
		t.Fatal("unknown C accepted new strict message", err)
	}
	var messages int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&messages); err != nil || messages != 0 {
		t.Fatal("unknown C left accepted message", err)
	}
	if err = svc.ReconcileLarkSwitchOutcomes(ctx); err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
	if err != nil || receipt.Status != "succeeded" || receipt.Continuation == nil || n.releases.Load() != 1 || n.acquires.Load() != 1 {
		t.Fatal("original-action confirmation repeated operation", err)
	}
	var turns int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, in.TaskID).Scan(&turns); err != nil || turns != 2 {
		t.Fatal("unknown C retried continuation", turns, err)
	}
}

func TestTaskLarkOperationKnownCRollbackSafelyAbortsWithoutRetry(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	n := &larkOperationNative{}
	n.seal = func() error {
		_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) { m.Changed(); return nil, nil })
		return err
	}
	svc, ex := larkOperationService(t, s, n)
	receipt, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in))
	if err != nil || receipt.Status != "failed" || receipt.BindingRevision != nil || n.acquires.Load() != 1 || n.releases.Load() != 1 {
		t.Fatal("known final CAS rollback did not abort safely", err)
	}
	lease, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{in.Members[0].RuntimeID, in.Members[1].RuntimeID})
	if err != nil {
		t.Fatal("known safe rollback kept unnecessary gate", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	var groups, turns, fences int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_turns WHERE task_id=$1),(SELECT count(*) FROM task_lark_switch_fences)`, in.TaskID).Scan(&groups, &turns, &fences); err != nil || groups != 0 || turns != 1 || fences != 0 {
		t.Fatal("failed C retried/partially published", err)
	}
}
