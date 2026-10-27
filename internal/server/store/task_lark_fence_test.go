package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

func larkFenceFixture(t *testing.T) (*store.Store, store.TaskLarkFenceReservation) {
	t.Helper()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	ctx := context.Background()
	claimed, found, err := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if err != nil || !found {
		t.Fatal("original fixture dispatch", err)
	}
	if err = s.MarkTaskRunning(ctx, claimed, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTaskTurn(ctx, task, initial.ID, "failed", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	record, err := s.GetTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	followup, err := s.CreateAgent(ctx, store.ProjectScope(record.View.Task.ProjectID), "future-followup", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.New()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'))`, runtime, task, followup.ID, followup.Name, larkSwitchSnapshot(t, task, followup.ID, true)); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err = s.GetTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	return s, store.TaskLarkFenceReservation{ActionID: uuid.New(), TaskID: task, NodeRunID: node, ActorKey: "console:synthetic", RequestHash: sha256.Sum256([]byte("synthetic pinned request")), ExpectedTaskVersion: record.View.Task.Version, Members: []store.TaskLarkFenceMember{{AgentID: agent, RuntimeID: initial.RuntimeID, Role: "publisher"}, {AgentID: followup.ID, RuntimeID: runtime, Role: "followup"}}}
}

func reserveLarkFence(ctx context.Context, s *store.Store, in store.TaskLarkFenceReservation) (bool, error) {
	created := false
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var err error
		created, err = s.ReserveTaskLarkFenceTx(ctx, m, in)
		return nil, err
	})
	return created, err
}

func TestTaskLarkFenceReservationAndSafeFailure(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	if created, err := reserveLarkFence(ctx, s, in); err != nil || !created {
		t.Fatal("reserve", created, err)
	}
	if created, err := reserveLarkFence(ctx, s, in); err != nil || created {
		t.Fatal("replay duplicated reservation", created, err)
	}
	changed := in
	changed.RequestHash = sha256.Sum256([]byte("different request"))
	if _, err := reserveLarkFence(ctx, s, changed); err == nil {
		t.Fatal("same action accepted different contents")
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		for _, member := range in.Members {
			fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, member.RuntimeID)
			if err != nil || !fenced {
				t.Fatal("member missing durable fence", err)
			}
		}
		if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, uuid.New()); err != nil || fenced {
			t.Fatal("fence escaped explicit member set", err)
		}
		return nil, s.FailTaskLarkFenceTx(ctx, m, in.ActorKey, in.ActionID, "synthetic_safe_stop_failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	var fences, bindings, turns int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, in.TaskID).Scan(&fences, &bindings, &turns); err != nil || fences != 0 || bindings != 0 || turns != 1 {
		t.Fatal("A/failure created binding or continuation", fences, bindings, turns, err)
	}
	if err = s.MigrateDownTo(ctx, 20); err == nil {
		t.Fatal("Down discarded action audit")
	}
}

func TestTaskLarkFenceRejectsBusyFollowupAtomically(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	member := in.Members[1]
	if _, err := s.Pool.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,status,origin,text,user_message_id,assistant_message_id) VALUES($1,$2,$3,'queued','inbox_reminder','real queued work',$4,$5)`, uuid.New(), in.TaskID, member.RuntimeID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveLarkFence(ctx, s, in); err == nil {
		t.Fatal("busy second member accepted")
	}
	var actions, fences int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences)`).Scan(&actions, &fences); err != nil || actions != 0 || fences != 0 {
		t.Fatal("rejected group left partial reservation", actions, fences, err)
	}
}

func TestTaskLarkFenceRejectsManagerRetryWithoutChangingAttempt(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	if _, err := reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{MaxVisits: 100}, nil)
	defer svc.Stop(ctx)
	_, err = svc.Retry(ctx, auth.Console(), in.TaskID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: in.NodeRunID})
	if e, ok := apperr.As(err); !ok || e.Code != "task_conflict" || e.Message != "原实例正在凭证交接，不能并发重试节点" {
		t.Fatal("manager retry bypassed handoff fence", err)
	}
	after, err := s.GetTask(ctx, in.TaskID)
	if err != nil || after.View.Task.Version != before.View.Task.Version || len(after.View.Nodes) != len(before.View.Nodes) || after.Current().ID != before.Current().ID {
		t.Fatal("refused retry changed current attempt", err)
	}
}

func TestTaskLarkFenceInvalidReservationHasNoSideEffects(t *testing.T) {
	cases := []struct {
		name string
		edit func(context.Context, *testing.T, *store.Store, *store.TaskLarkFenceReservation)
	}{
		{"missing_action", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.ActionID = uuid.Nil
		}},
		{"stale_task_version", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.ExpectedTaskVersion--
		}},
		{"wrong_node", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.NodeRunID = uuid.New()
		}},
		{"one_member", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members = in.Members[:1]
		}},
		{"duplicate_runtime", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members[1].RuntimeID = in.Members[0].RuntimeID
		}},
		{"duplicate_agent", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members[1].AgentID = in.Members[0].AgentID
		}},
		{"duplicate_role", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members[1].Role = "publisher"
		}},
		{"swapped_publisher", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members[0].Role, in.Members[1].Role = "followup", "publisher"
		}},
		{"wrong_agent", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Members[1].AgentID = uuid.New()
		}},
		{"retired_followup", func(ctx context.Context, t *testing.T, s *store.Store, in *store.TaskLarkFenceReservation) {
			if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET retired=true WHERE id=$1`, in.Members[1].RuntimeID); err != nil {
				t.Fatal(err)
			}
		}},
		{"foreign_task_runtime", func(ctx context.Context, t *testing.T, s *store.Store, in *store.TaskLarkFenceReservation) {
			// The shared fixture uses a stable live Agent name; free that name
			// without changing the original task's frozen instance/snapshot.
			if _, err := s.Pool.Exec(ctx, `UPDATE agents SET name='original-node-agent' WHERE id=$1`, in.Members[0].AgentID); err != nil {
				t.Fatal(err)
			}
			_, _, agent, turn := taskConsoleRecord(t, s)
			in.Members[1].AgentID, in.Members[1].RuntimeID = agent, turn.RuntimeID
		}},
		{"stale_binding_revision", func(_ context.Context, _ *testing.T, _ *store.Store, in *store.TaskLarkFenceReservation) {
			in.Capture = &store.TaskLarkSwitchCapture{ExpectedBindingRevision: 1}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, in := larkFenceFixture(t)
			ctx := context.Background()
			tc.edit(ctx, t, s, &in)
			before, err := s.GetTask(ctx, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reserveLarkFence(ctx, s, in); err == nil {
				t.Fatal("invalid reservation accepted")
			}
			var actions, fences, bindings int
			if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings)`).Scan(&actions, &fences, &bindings); err != nil {
				t.Fatal(err)
			}
			after, err := s.GetTask(ctx, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if actions != 0 || fences != 0 || bindings != 0 || before.View.Task.Version != after.View.Task.Version || before.Current().ID != after.Current().ID {
				t.Fatal("rejected reservation changed task or published state", actions, fences, bindings)
			}
		})
	}
}

func TestTaskLarkMigrationEmptyRoundTrip(t *testing.T) {
	// 026 cannot be rolled back; the 021 round trip runs on a 025 schema.
	s := taskConsoleStoreAt(t, 25)
	ctx := context.Background()
	_, runtime := rawTaskAt25(t, s)
	var before []byte
	if err := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, runtime).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateDownTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateUpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, runtime).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("migration rewrote/resealed original task snapshot")
	}
}

func TestTaskLarkFenceConcurrentReplayAndOriginalActionQuery(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := reserveLarkFence(ctx, s, in)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	action, err := s.TaskLarkAction(ctx, in.TaskID, in.ActorKey, in.ActionID)
	if err != nil || action.Status != "pending" || action.ReservationTaskVersion != in.ExpectedTaskVersion+1 {
		t.Fatal("unknown response cannot recover original reservation", err)
	}
	if _, err = s.TaskLarkAction(ctx, uuid.New(), in.ActorKey, in.ActionID); !store.IsNotFound(err) {
		t.Fatal("cross-task action leaked", err)
	}
	var actions, fences int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences)`).Scan(&actions, &fences); err != nil || actions != 1 || fences != 2 {
		t.Fatal("concurrent action duplicated members", actions, fences, err)
	}
}

func TestTaskLarkFenceDefersRealInboxWithoutReading(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	if _, err := reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	message := uuid.New()
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		member := in.Members[0]
		if err := store.InsertInboxMessage(ctx, m.Tx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: member.AgentID, RuntimeID: member.RuntimeID}, Text: "real ordinary Inbox during handoff", WhenBusy: "queue"}); err != nil {
			return nil, err
		}
		deferred, err := s.DeferTaskLarkInboxTx(ctx, m, message, member.RuntimeID)
		if err == nil && !deferred {
			t.Fatal("fenced ordinary Inbox was not durably deferred")
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.GetInboxMessage(ctx, message, 160)
	if err != nil || msg.ReadAt != nil || msg.Text != "real ordinary Inbox during handoff" {
		t.Fatal("defer lost or pretended to read message", err)
	}
	var pending, turns int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_deferred_inbox WHERE message_id=$1 AND delivered_at IS NULL),(SELECT count(*) FROM task_turns WHERE task_id=$2)`, message, in.TaskID).Scan(&pending, &turns); err != nil || pending != 1 || turns != 1 {
		t.Fatal("defer fabricated a new execution", pending, turns, err)
	}
}

func TestTaskLarkFencePublicStrictAndOrdinaryInbox(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	publisher := in.Members[0]
	session := filepath.Join(t.TempDir(), "original.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		if _, err := m.Tx.Exec(ctx, `UPDATE task_instances SET session_state='active',session_file=$2 WHERE id=$1`, publisher.RuntimeID, session); err != nil {
			return nil, err
		}
		var old uuid.UUID
		if err := m.Tx.QueryRow(ctx, `UPDATE task_turns SET error_code='pi_error',execution_mode='task_node',started_at=clock_timestamp() WHERE task_id=$1 RETURNING id`, in.TaskID).Scan(&old); err != nil {
			return nil, err
		}
		return nil, s.RecordTaskConsoleEligibilityTx(ctx, m, old)
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetTask(ctx, in.TaskID)
	in.ExpectedTaskVersion = r.View.Task.Version
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		reason, err := s.TaskConsoleGuardTx(ctx, m, store.TaskConsoleLineage{TaskID: in.TaskID, NodeRunID: in.NodeRunID, AgentID: publisher.AgentID, RuntimeID: publisher.RuntimeID})
		if err == nil && reason != "" {
			t.Fatal("fixture was not locally chat eligible before fence", reason)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	_, err = svc.SendNodeInboxMessage(ctx, auth.Console(), in.TaskID, in.NodeRunID, taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: publisher.AgentID, ExpectedRuntimeID: publisher.RuntimeID, Text: "strict must not enqueue", WhenBusy: "interrupt"}, 65536)
	if !apperr.Is(err, "task_conflict") {
		t.Fatal("strict input bypassed fence", err)
	}
	before, _ := s.GetTask(ctx, in.TaskID)
	message := uuid.New()
	if err = svc.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: publisher.AgentID, RuntimeID: publisher.RuntimeID}, Text: "ordinary must persist", WhenBusy: "interrupt"}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetTask(ctx, in.TaskID)
	if before.View.Task.Version != after.View.Task.Version {
		t.Fatal("deferred Inbox invalidated own reservationTaskVersion")
	}
	var messages, turns, deferred int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM inbox_messages),(SELECT count(*) FROM task_turns WHERE task_id=$1),(SELECT count(*) FROM task_lark_deferred_inbox WHERE message_id=$2)`, in.TaskID, message).Scan(&messages, &turns, &deferred); err != nil || messages != 1 || turns != 1 || deferred != 1 {
		t.Fatal("public delivery lost message, enqueued work or left strict side effects", messages, turns, deferred, err)
	}
	// A queue created by another original path must remain unclaimed while the
	// handoff fence is held, without turning it into a failed/expired message.
	if _, err = s.Pool.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,status,origin,text,user_message_id,assistant_message_id) VALUES($1,$2,$3,'queued','inbox_reminder','retained queue',$4,$5)`, uuid.New(), in.TaskID, publisher.RuntimeID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ClaimTaskInboxTurn(ctx, in.TaskID, publisher.RuntimeID); err != nil || found {
		t.Fatal("fenced Inbox claim admitted work", found, err)
	}
	if _, found, err := s.ClaimTaskTurn(ctx, in.TaskID, publisher.RuntimeID); err != nil || found {
		t.Fatal("fenced workflow claim admitted work", found, err)
	}
}

func TestTaskLarkFenceSuspendsOriginalSleepAndFailureKeepsDeadline(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	publisher := in.Members[0]
	session := filepath.Join(t.TempDir(), "original.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET session_state='active',session_file=$2 WHERE id=$1`, publisher.RuntimeID, session); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		_, err := enqueueBackedInboxTurn(ctx, s, m, store.TaskInstance{ID: publisher.RuntimeID, TaskID: in.TaskID, AgentID: publisher.AgentID}, "original ordinary conversation", 1)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, found, err := s.ClaimTaskInboxTurn(ctx, in.TaskID, publisher.RuntimeID)
	if err != nil || !found {
		t.Fatal("original conversation claim", err)
	}
	if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: publisher.AgentID, RuntimeID: publisher.RuntimeID, TaskID: in.TaskID}
	receipt, err := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 120}, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		if _, err := s.UpdateTaskTurnTx(ctx, m, turn.ID, "completed", "normal settle", nil, nil); err != nil {
			return nil, err
		}
		_, err := s.FinishSleepTx(ctx, m.Tx, m, turn.ID, "completed")
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only this TEST record's deadline is advanced; no server/global clock is
	// changed. Fencing must retain its exact stored deadline and revision.
	record, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAgent(ctx, store.ProjectScope(record.View.Task.ProjectID), "other-sleeping-member", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	otherRuntime := uuid.New()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared,session_state,session_file) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'),true,'active',$6)`, otherRuntime, in.TaskID, other.ID, other.Name, larkSwitchSnapshot(t, in.TaskID, other.ID, true), session); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		_, err := enqueueBackedInboxTurn(ctx, s, m, store.TaskInstance{ID: otherRuntime, TaskID: in.TaskID, AgentID: other.ID}, "unselected member conversation", 1)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	otherTurn, found, err := s.ClaimTaskInboxTurn(ctx, in.TaskID, otherRuntime)
	if err != nil || !found {
		t.Fatal("third runtime claim", err)
	}
	if err = s.MarkTaskRunning(ctx, otherTurn, 1); err != nil {
		t.Fatal(err)
	}
	otherReceipt, err := s.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: other.ID, RuntimeID: otherRuntime, TaskID: in.TaskID}, sleepmodel.Input{ActionID: uuid.New(), RunID: otherTurn.ID, Seconds: 120}, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		if _, err := s.UpdateTaskTurnTx(ctx, m, otherTurn.ID, "completed", "normal settle", nil, nil); err != nil {
			return nil, err
		}
		_, err := s.FinishSleepTx(ctx, m.Tx, m, otherTurn.ID, "completed")
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Second)
	if _, err = s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=$2 WHERE id=$1 OR id=$3`, receipt.Sleep.ID, expired, otherReceipt.Sleep.ID); err != nil {
		t.Fatal(err)
	}
	var originalRevision int64
	if err = s.Pool.QueryRow(ctx, `SELECT revision FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&originalRevision); err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetTask(ctx, in.TaskID)
	in.ExpectedTaskVersion = r.View.Task.Version
	if _, err = reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal("dormant original sleep timer was mistaken for queued business work", err)
	}
	if err = s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var state string
	var deadline time.Time
	var revision int64
	if err = s.Pool.QueryRow(ctx, `SELECT state,wake_at,revision FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state, &deadline, &revision); err != nil || state != "sleeping" || revision != originalRevision || !deadline.Equal(expired.Truncate(time.Microsecond)) {
		t.Fatal("fence cancelled, consumed or rescheduled original deadline", state, revision, deadline, err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, otherReceipt.Sleep.ID).Scan(&state); err != nil || state != "woken" {
		t.Fatal("member fence incorrectly suspended entire task sleep", state, err)
	}
	if _, found, err := s.ClaimTaskInboxTurn(ctx, in.TaskID, otherRuntime); err != nil || !found {
		t.Fatal("member fence blocked unselected runtime claim", err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.FailTaskLarkFenceTx(ctx, m, in.ActorKey, in.ActionID, "safe_failed_handoff")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT state,wake_at FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state, &deadline); err != nil || state != "woken" || !deadline.Equal(expired.Truncate(time.Microsecond)) {
		t.Fatal("safe failed handoff lost original due wake", state, deadline, err)
	}
}
