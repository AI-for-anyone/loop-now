package store_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"sync"
	"testing"

	"loop-now/internal/server/store"
)

func TestTaskLarkRecoveryRecordIsOriginalAndPending(t *testing.T) {
	s, in, _, _ := larkPublishFixture(t)
	ctx := context.Background()
	items, e := s.PendingTaskLarkRecovery(ctx)
	if e != nil || len(items) != 1 || items[0].ProcessRecord != nil || len(items[0].RuntimeIDs) != 2 {
		t.Fatal("missing record must stay unknown", e)
	}
	save := func(raw string) error {
		_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			return nil, s.SaveTaskLarkRecoveryTx(ctx, m, in.ActorKey, in.ActionID, json.RawMessage(raw))
		})
		return e
	}
	for _, bad := range []string{`null`, `[]`, `{}`} {
		if save(bad) == nil {
			t.Fatal("invalid recovery accepted")
		}
	}
	// Synthetic internal record proves storage only, not OS process death.
	if e = save(`{"version":1,"synthetic":"original"}`); e != nil {
		t.Fatal(e)
	}
	if e = save(`{"synthetic":"original","version":1}`); e != nil {
		t.Fatal("same record replay", e)
	}
	if save(`{"version":1,"synthetic":"replacement"}`) == nil {
		t.Fatal("original process receipt replaced")
	}
	items, e = s.PendingTaskLarkRecovery(ctx)
	if e != nil || len(items) != 1 || items[0].ActionID != in.ActionID || items[0].ActorKey != in.ActorKey {
		t.Fatal(e)
	}
	var record map[string]any
	if json.Unmarshal(items[0].ProcessRecord, &record) != nil || record["synthetic"] != "original" {
		t.Fatal("original record lost")
	}
	_, e = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.FailTaskLarkFenceTx(ctx, m, in.ActorKey, in.ActionID, "synthetic_known_safe")
	})
	if e != nil {
		t.Fatal(e)
	}
	if save(`{"version":1,"synthetic":"original"}`) == nil {
		t.Fatal("terminal action recovery mutated")
	}
	items, e = s.PendingTaskLarkRecovery(ctx)
	if e != nil || len(items) != 0 {
		t.Fatal("terminal action remains pending", e)
	}
}

func TestTaskLarkRecoveryConsumeOriginalPreCommitCAS(t *testing.T) {
	for _, scenario := range []string{"valid-concurrent", "other-action", "other-member", "other-record", "native-null", "missing-fence", "published", "different-failure", "commit-failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, in, next, turn := larkPublishFixture(t)
			ctx := context.Background()
			// Storage/transaction proof only: synthetic records do not claim an OS
			// owner or native group has died. The real manager validates that first.
			record := json.RawMessage(`{"local":{"synthetic":true},"native":{"counter":9007199254740993}}`)
			_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				m.Unchanged()
				return nil, s.SaveTaskLarkRecoveryTx(ctx, m, in.ActorKey, in.ActionID, record)
			})
			if e != nil {
				t.Fatal(e)
			}
			ids := [2]uuid.UUID{in.Members[0].RuntimeID, in.Members[1].RuntimeID}
			action := in.ActionID
			expected := record
			switch scenario {
			case "other-action":
				action = uuid.New()
			case "other-member":
				ids[1] = uuid.New()
			case "other-record":
				expected = json.RawMessage(`{"local":{"synthetic":true},"native":{"counter":9007199254740992}}`)
			case "native-null":
				expected = json.RawMessage(`{"local":{"synthetic":true},"native":null}`)
			case "missing-fence":
				_, e = s.Pool.Exec(ctx, `DELETE FROM task_lark_switch_fences WHERE runtime_id=$1`, ids[1])
			case "published":
				_, e = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
					m.Unchanged()
					_, _, e := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "original QA", json.RawMessage(`{"status":"succeeded"}`))
					return nil, e
				})
			case "different-failure":
				_, e = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
					return nil, s.FailTaskLarkFenceTx(ctx, m, in.ActorKey, in.ActionID, "some_other_failure")
				})
			case "commit-failure":
				_, e = s.Pool.Exec(ctx, `CREATE FUNCTION reject_lark_recovery_save() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic recovery save failure'; END $$; CREATE TRIGGER reject_lark_recovery_save BEFORE UPDATE ON task_records FOR EACH ROW EXECUTE FUNCTION reject_lark_recovery_save()`)
			}
			if e != nil {
				t.Fatal(e)
			}
			before, e := s.GetTask(ctx, in.TaskID)
			if e != nil {
				t.Fatal(e)
			}
			consume := func() error {
				_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
					m.Unchanged()
					return nil, s.ConsumeTaskLarkRecoveryTx(ctx, m, in.ActorKey, action, ids, expected)
				})
				return e
			}
			if scenario == "valid-concurrent" {
				var wg sync.WaitGroup
				results := make(chan error, 4)
				for i := 0; i < 4; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); results <- consume() }()
				}
				wg.Wait()
				close(results)
				for e := range results {
					if e != nil {
						t.Fatal(e)
					}
				}
			} else if e = consume(); e == nil {
				t.Fatal("invalid/stale/unknown recovery consumed")
			}
			var status string
			var reason *string
			var fences, groups int
			if e = s.Pool.QueryRow(ctx, `SELECT status,failure_reason,(SELECT count(*) FROM task_lark_switch_fences WHERE task_id=$1),(SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1) FROM task_lark_switch_actions WHERE task_id=$1 AND actor_key=$2 AND action_id=$3`, in.TaskID, in.ActorKey, in.ActionID).Scan(&status, &reason, &fences, &groups); e != nil {
				t.Fatal(e)
			}
			after, e := s.GetTask(ctx, in.TaskID)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "valid-concurrent" {
				if status != "failed" || reason == nil || *reason != "startup_handoff_recovered" || fences != 0 || groups != 0 || after.View.Task.Version != before.View.Task.Version+1 || after.View.Task.Status != "blocked" {
					t.Fatal("pre-C abort not atomic/idempotent", status, reason, fences, groups)
				}
			} else {
				if after.View.Task.Version != before.View.Task.Version {
					t.Fatal("rejected recovery mutated task")
				}
				wantFences := 2
				if scenario == "missing-fence" {
					wantFences = 1
				}
				if scenario == "published" || scenario == "different-failure" {
					wantFences = 0
				}
				if fences != wantFences {
					t.Fatal("rejected recovery removed fences", fences)
				}
				if scenario == "published" {
					if status != "succeeded" || groups != 1 {
						t.Fatal("recovery changed committed C")
					}
				} else if scenario == "different-failure" {
					if status != "failed" || reason == nil || *reason != "some_other_failure" {
						t.Fatal("recovery overwrote another failure")
					}
				} else if status != "pending" || groups != 0 {
					t.Fatal("invalid recovery published/failed original action", status, groups)
				}
			}
		})
	}
}

func TestTaskLarkRecoveryRecordPreservesNativeCounterPrecision(t *testing.T) {
	s, in, _, _ := larkPublishFixture(t)
	ctx := context.Background()
	save := func(raw string) error {
		_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			return nil, s.SaveTaskLarkRecoveryTx(ctx, m, in.ActorKey, in.ActionID, json.RawMessage(raw))
		})
		return e
	}
	if e := save(`{"local":{},"native":{"counter":9007199254740993}}`); e != nil {
		t.Fatal(e)
	}
	if e := save(`{"native":{"counter":9007199254740993},"local":{}}`); e != nil {
		t.Fatal("same record was not idempotent", e)
	}
	if save(`{"local":{},"native":{"counter":9007199254740992}}`) == nil {
		t.Fatal("different large counter became same float record")
	}
}
