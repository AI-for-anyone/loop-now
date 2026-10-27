package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
)

func TestTaskLarkPublicationConfirmsOriginalCommitWithoutReplaying(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	state, err := s.TaskLarkPublication(ctx, in.TaskID, in.ActorKey, in.ActionID)
	if err != nil || state.Status != "pending" || state.OutstandingFences != 2 || state.BindingRevision != nil {
		t.Fatal("pending is not committed", err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately discard C's result, then query only its original action.
	// No second C or new action/run is issued to recover the response.
	for i := 0; i < 3; i++ {
		state, err = s.TaskLarkPublication(ctx, in.TaskID, in.ActorKey, in.ActionID)
		if err != nil || state.Status != "succeeded" || state.BindingRevision == nil || *state.BindingRevision != 1 || state.ContinuationTurnID == nil || *state.ContinuationTurnID != turn || state.ContinuationNodeRunID == nil || *state.ContinuationNodeRunID != next.ID || state.DispatchStatus == nil || *state.DispatchStatus != "queued" {
			t.Fatal("original durable outcome not recoverable", err)
		}
	}
	if _, err = s.TaskLarkPublication(ctx, uuid.New(), in.ActorKey, in.ActionID); !store.IsNotFound(err) {
		t.Fatal("cross-task outcome exposed", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.TaskLarkPublication(cancelled, in.TaskID, in.ActorKey, in.ActionID); err == nil || store.IsNotFound(err) {
		t.Fatal("query failure became absent/rollback", err)
	}
	var bindings, turns int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, in.TaskID).Scan(&bindings, &turns); err != nil || bindings != 1 || turns != 2 {
		t.Fatal("read-only recovery repeated C", bindings, turns, err)
	}
}
