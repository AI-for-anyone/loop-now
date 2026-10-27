package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/testutil/redistest"
)

func larkPublishedFixture(t *testing.T) (*store.Store, store.TaskLarkFenceReservation, taskmodel.NodeRun, uuid.UUID) {
	t.Helper()
	s, in, next, run := larkPublishFixture(t)
	_, err := s.MutateTask(context.Background(), in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(context.Background(), m, in.ActorKey, in.ActionID, next, run, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, in, next, run
}

func TestTaskLarkRecoveryQuestionWaitUsesExplicitActualRun(t *testing.T) {
	s, in, next, run := larkPublishedFixture(t)
	ctx := context.Background()
	p := larkCurrentPrincipal(next)
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	turn, found, err := s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(redistest.URL(t))
	if err != nil {
		t.Fatal("TEST Redis URL invalid")
	}
	rdb := redis.NewClient(opts)
	prefix := "loopnow:test:lark-recovery-question:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		if err := redistest.DeletePrefix(ctx, rdb, prefix); err != nil {
			t.Error(err)
		}
		rdb.Close()
	})
	inboxSvc := &inbox.Service{St: s, Tasks: svc, Co: coord.New(rdb, coord.Options{Prefix: prefix, StreamMaxLen: 100}), Log: quietLogger(), O: inbox.Options{MaxMessage: 65536, PreviewChars: 160}}
	for _, id := range []uuid.UUID{uuid.Nil, uuid.New()} {
		if _, err = inboxSvc.Send(ctx, p, auth.ConsoleAddress, "must not create stale wait", "queue", inbox.QuestionInput{NodeRunID: next.ID, RunID: id, Options: []string{"continue"}}); !apperr.Is(err, "task_conflict") {
			t.Fatal("wait guessed active turn", err)
		}
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_questions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid run left question", err)
	}
	msg, err := inboxSvc.Send(ctx, p, auth.ConsoleAddress, "actual recovery question", "queue", inbox.QuestionInput{NodeRunID: next.ID, RunID: run, Options: []string{"continue"}})
	if err != nil || msg.ID == uuid.Nil {
		t.Fatal("accurate recovery run cannot ask", err)
	}
	record, err := s.GetTask(ctx, in.TaskID)
	if err != nil || record.Current().Question == nil || record.Current().Question.ID != msg.ID {
		t.Fatal("question wait lost original node", err)
	}
}
func larkCurrentPrincipal(next taskmodel.NodeRun) auth.Principal {
	return auth.Principal{Kind: auth.KindAgent, ID: next.AgentID, RuntimeID: next.RuntimeID, TaskID: next.TaskID}
}

func TestTaskLarkRecoveryRunGuardAndSubmission(t *testing.T) {
	s, in, next, run := larkPublishedFixture(t)
	ctx := context.Background()
	p := larkCurrentPrincipal(next)
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	for _, provided := range []*uuid.UUID{nil, &run, &in.ActionID} {
		_, err := svc.Submit(ctx, p, in.TaskID, next.ID, taskmodel.SubmitInput{ActionID: uuid.New(), RunID: provided, Output: map[string]any{}})
		if !apperr.Is(err, "task_conflict") {
			t.Fatal("recovery accepted missing/wrong/not-running run", err)
		}
	}
	turn, found, err := s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID)
	if err != nil || !found || turn.ID != run {
		t.Fatal("real recovery claim", err)
	}
	if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Fail(ctx, p, in.TaskID, next.ID, taskmodel.FailInput{ActionID: uuid.New(), Reason: "old caller"}); !apperr.Is(err, "task_conflict") {
		t.Fatal("missing run fail accepted", err)
	}
	if _, err = svc.Submit(ctx, p, in.TaskID, next.ID, taskmodel.SubmitInput{ActionID: uuid.New(), RunID: &run, Output: map[string]any{}}); err != nil {
		t.Fatal("valid exact running turn rejected", err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		valid, err := s.TaskLarkSubmittedByTurnTx(ctx, m, turn)
		if err != nil || !valid {
			t.Fatal("own submitted turn lost", err)
		}
		other := turn
		other.ID = uuid.New()
		valid, err = s.TaskLarkSubmittedByTurnTx(ctx, m, other)
		if err != nil || valid {
			t.Fatal("another turn inherited old output", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskLarkRecoverySleepAndRealInboxSuccessors(t *testing.T) {
	s, in, next, initial := larkPublishedFixture(t)
	ctx := context.Background()
	p := larkCurrentPrincipal(next)
	turn, found, err := s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
		t.Fatal(err)
	}
	intent, err := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: initial, Seconds: 120}, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		if _, err := s.UpdateTaskTurnTx(ctx, m, initial, "completed", "sleep settles", nil, nil); err != nil {
			return nil, err
		}
		_, err := s.FinishSleepTx(ctx, m.Tx, m, initial, "completed")
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=$2 WHERE id=$1`, intent.Sleep.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	wake, found, err := s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID)
	if err != nil || !found || wake.ID == initial || wake.Origin != "sleep_wake" {
		t.Fatal("real sleep successor", err)
	}
	if err = s.MarkTaskRunning(ctx, wake, 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		if err := s.CheckTaskLarkRunTx(ctx, m, next.ID, next.RuntimeID, wake.ID); err != nil {
			return nil, err
		}
		if err := s.CheckTaskLarkRunTx(ctx, m, next.ID, next.RuntimeID, initial); !apperr.Is(err, "task_conflict") {
			t.Fatal("old initial run revived after wake", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTaskTurn(ctx, in.TaskID, wake.ID, "completed", "wake settled", nil, nil); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	message := uuid.New()
	if err = svc.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, Text: "real successor message", WhenBusy: "queue"}); err != nil {
		t.Fatal(err)
	}
	reminder, found, err := s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID)
	if err != nil || !found || reminder.TriggerInboxID == nil || *reminder.TriggerInboxID != message {
		t.Fatal("real Inbox successor", err)
	}
	if err = s.MarkTaskRunning(ctx, reminder, 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return nil, s.CheckTaskLarkRunTx(ctx, m, next.ID, next.RuntimeID, reminder.ID)
	})
	if err != nil {
		t.Fatal("Inbox did not inherit actual action/revision", err)
	}
	if _, err = s.UpdateTaskTurn(ctx, in.TaskID, reminder.ID, "completed", "settled", nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		_, err := s.EnqueueTaskTurn(ctx, m, "not backed by a real message", "inbox_reminder", 1, nil)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ClaimTaskTurn(ctx, in.TaskID, next.RuntimeID); !apperr.Is(err, "task_conflict") {
		t.Fatal("guessed recent run for unproven reminder", err)
	}
}

func TestTaskLarkFollowupLaterConversationRecordsFixedRevision(t *testing.T) {
	s, in, _, _ := larkPublishedFixture(t)
	ctx := context.Background()
	var member store.TaskLarkFenceMember
	for _, m := range in.Members {
		if m.Role == "followup" {
			member = m
		}
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, member.RuntimeID); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	message := uuid.New()
	if err := svc.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: member.AgentID, RuntimeID: member.RuntimeID}, Text: "later explicit followup message", WhenBusy: "queue"}); err != nil {
		t.Fatal(err)
	}
	turn, found, err := s.ClaimTaskInboxTurn(ctx, in.TaskID, member.RuntimeID)
	if err != nil || !found || turn.ExecutionMode != "task_conversation" {
		t.Fatal("later explicit conversation claim", err)
	}
	var revision int64
	var action uuid.UUID
	if err = s.Pool.QueryRow(ctx, `SELECT lark_binding_revision,lark_switch_action_id FROM task_turns WHERE id=$1`, turn.ID).Scan(&revision, &action); err != nil || revision != 1 || action != in.ActionID {
		t.Fatal("followup turn lost fixed source revision", err)
	}
}
