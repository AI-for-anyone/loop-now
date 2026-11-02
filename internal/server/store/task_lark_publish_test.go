package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

func larkPublishFixture(t *testing.T) (*store.Store, store.TaskLarkFenceReservation, taskmodel.NodeRun, uuid.UUID) {
	t.Helper()
	return larkPublishFixtureWithScopes(t, []string{"offline_access", "im:message", "im:message.send_as_user", "im:message.group_msg:get_as_user"})
}

func larkPublishFixtureWithScopes(t *testing.T, scopes []string) (*store.Store, store.TaskLarkFenceReservation, taskmodel.NodeRun, uuid.UUID) {
	t.Helper()
	s, in := larkSwitchReadyFixture(t, scopes)
	ctx := context.Background()
	if _, err := reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	old := r.Current()
	raw, _ := json.Marshal(old)
	var next taskmodel.NodeRun
	json.Unmarshal(raw, &next)
	next.ID, next.Attempt, next.Status = uuid.New(), old.Attempt+1, "queued"
	next.Output, next.Question, next.Sleep, next.EndedAt = nil, nil, nil, nil
	turn := uuid.New()
	next.RunIDs = []uuid.UUID{turn}
	return s, in, next, turn
}

func larkSwitchReadyFixture(t *testing.T, scopes []string) (*store.Store, store.TaskLarkFenceReservation) {
	t.Helper()
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	publisher := in.Members[0]
	session := filepath.Join(t.TempDir(), "original.jsonl")
	if err := os.WriteFile(session, []byte("original retained session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// SQLITE-PORT(G5): sha256/encode
	if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2,snapshot_sha=$4,session_state='active',session_file=$3 WHERE id=$1`, publisher.RuntimeID, larkSwitchSnapshot(t, in.TaskID, publisher.AgentID, true), session, sha256Hex(larkSwitchSnapshot(t, in.TaskID, publisher.AgentID, true))); err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetTask(ctx, in.TaskID)
	account := larkSwitchTargetWithScopes(t, s, r.View.Task.ProjectID, time.Now().Add(time.Hour), scopes)
	capture := &store.TaskLarkSwitchCapture{}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var err error
		capture.Target, err = s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, *account.View.CurrentVersionID, account.View.Revision)
		if err != nil {
			return nil, err
		}
		for _, member := range in.Members {
			original, err := s.CaptureTaskLarkMemberTx(ctx, m, member)
			if err != nil {
				return nil, err
			}
			capture.Members = append(capture.Members, original)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	in.Capture = capture
	return s, in
}

func TestTaskLarkPublishUnknownGrantedScopesStaysUnknown(t *testing.T) {
	s, in, next, turn := larkPublishFixtureWithScopes(t, nil)
	ctx := context.Background()
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err != nil {
		t.Fatal("unknown actual scopes rejected C", err)
	}
	var unknown bool
	if err = s.Pool.QueryRow(ctx, `SELECT captured_request->'Target'->'Identity'->'grantedScopes'='null' FROM task_lark_switch_actions WHERE actor_key=$1 AND action_id=$2`, in.ActorKey, in.ActionID).Scan(&unknown); err != nil || !unknown {
		t.Fatal("switch captured requested scopes instead of actual unknown", err)
	}
}

func TestTaskLarkPublishAtomicMembersAndSingleContinuation(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	before := map[uuid.UUID][]byte{}
	for _, member := range in.Members {
		var raw []byte
		if err := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, member.RuntimeID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		before[member.RuntimeID] = raw
	}
	commit := func() (bool, error) {
		created := false
		_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			revision, published, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
			created = published
			if err == nil && revision != 1 {
				t.Fatal("wrong first binding revision")
			}
			return nil, err
		})
		return created, err
	}
	if created, err := commit(); err != nil || !created {
		t.Fatal("final publish", created, err)
	}
	if created, err := commit(); err != nil || created {
		t.Fatal("C replay duplicated continuation", created, err)
	}
	r, err := s.GetTask(ctx, in.TaskID)
	if err != nil || r.Current().ID != next.ID || len(r.View.Nodes) != 2 || r.View.Nodes[0].ID != in.NodeRunID || r.View.Nodes[0].Status != "blocked" || r.Current().Attempt != 2 || r.Current().RuntimeID != in.Members[0].RuntimeID {
		t.Fatal("new attempt lost original task/history", err)
	}
	var bindings, members, turns, fences int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns WHERE task_id=$1),(SELECT count(*) FROM task_lark_switch_fences)`, in.TaskID).Scan(&bindings, &members, &turns, &fences); err != nil || bindings != 1 || members != 2 || turns != 2 || fences != 0 {
		t.Fatal("partial group or duplicate followup turn", bindings, members, turns, fences, err)
	}
	// Later current changes do not rebind this committed overlay.
	if _, err = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL,revision=revision+1 WHERE id=$1`, in.Capture.Target.AccountID); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		for _, member := range in.Capture.Members {
			binding, revision, err := s.TaskLarkEffectiveBindingTx(ctx, m, member)
			if err != nil || revision != 1 || binding == nil || binding.VersionID != in.Capture.Target.VersionID || binding.AgentID != member.AgentID {
				t.Fatal("member followed account current instead of fixed capture", err)
			}
			var after []byte
			if err := m.Tx.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, member.RuntimeID).Scan(&after); err != nil || !bytes.Equal(before[member.RuntimeID], after) {
				t.Fatal("overlay rewrote original sealed snapshot")
			}
		}
		if binding, revision, err := s.TaskLarkEffectiveBindingTx(ctx, m, store.TaskLarkMemberCapture{RuntimeID: uuid.New(), AgentID: uuid.New()}); err != nil || revision != 0 || binding != nil {
			t.Fatal("nonmember received task-wide Lark identity", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskLarkPublishExcludesOtherAuthorizedTaskRuntime(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	record, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, store.ProjectScope(record.View.Task.ProjectID), "unselected-lark-agent", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.New()
	original := larkSwitchSnapshot(t, in.TaskID, agent.ID, true)
	// SQLITE-PORT(G5): sha256/encode
	if _, err = s.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha) VALUES($1,$2,$3,$4,$5,$6)`, runtime, in.TaskID, agent.ID, agent.Name, original, sha256Hex(original)); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		// This is a real third member with original Lark permission, not a
		// random nonexistent UUID. Permission does not enroll it in the group.
		member, err := s.CaptureTaskLarkMemberTx(ctx, m, store.TaskLarkFenceMember{AgentID: agent.ID, RuntimeID: runtime, Role: "followup"})
		if err != nil {
			return nil, err
		}
		if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, runtime); err != nil || fenced {
			t.Fatal("fence escaped explicit two members", err)
		}
		if _, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`)); err != nil {
			return nil, err
		}
		binding, revision, err := s.TaskLarkEffectiveBindingTx(ctx, m, member)
		if err != nil || binding != nil || revision != 0 {
			t.Fatal("authorized outsider received group overlay", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var after []byte
	var queued, members int
	if err = s.Pool.QueryRow(ctx, `SELECT snapshot,(SELECT count(*) FROM task_turns WHERE runtime_id=$1),(SELECT count(*) FROM task_lark_binding_members WHERE runtime_id=$1) FROM task_instances WHERE id=$1`, runtime).Scan(&after, &queued, &members); err != nil || !bytes.Equal(after, original) || queued != 0 || members != 0 {
		t.Fatal("unselected original snapshot/runtime changed", queued, members, err)
	}
}

func TestTaskLarkPublishLateSecondMemberBusyHasNoPartialOverlay(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,status,origin,text,user_message_id,assistant_message_id,queue_seq) VALUES($1,$2,$3,'queued','inbox_reminder','late old work',$4,$5,(SELECT coalesce(max(queue_seq),0)+1 FROM task_turns))`, uuid.New(), in.TaskID, in.Members[1].RuntimeID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err == nil {
		t.Fatal("late second-member queue did not fail final CAS")
	}
	var bindings, members, continuation int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns WHERE id=$1)`, turn).Scan(&bindings, &members, &continuation); err != nil || bindings != 0 || members != 0 || continuation != 0 {
		t.Fatal("failed C left partial overlay", bindings, members, continuation, err)
	}
}

func TestTaskLarkPublishFinalCASFailuresPreserveOriginalBinding(t *testing.T) {
	for _, failure := range []string{"target_deleted", "target_current_changed", "grant_unavailable", "publisher_snapshot_corrupt", "followup_snapshot_corrupt", "publisher_session_changed", "followup_prepared_late", "reservation_version_changed", "commit_trigger_failure"} {
		t.Run(failure, func(t *testing.T) {
			s, in, next, turn := larkPublishFixture(t)
			ctx := context.Background()
			var err error
			switch failure {
			case "target_deleted":
				_, err = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET deleted_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, in.Capture.Target.AccountID)
			case "target_current_changed":
				_, err = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL WHERE id=$1`, in.Capture.Target.AccountID)
			case "grant_unavailable":
				_, err = s.Pool.Exec(ctx, `UPDATE lark_cli_grants SET refresh_state='unavailable' WHERE version_id=$1`, in.Capture.Target.VersionID)
			case "publisher_snapshot_corrupt", "followup_snapshot_corrupt":
				member := in.Members[0]
				if failure == "followup_snapshot_corrupt" {
					member = in.Members[1]
				}
				_, err = s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, member.RuntimeID, []byte(`{"broken":true}`))
			case "reservation_version_changed":
				_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
					m.Record.View.Task.Title = "new authoritative version"
					return nil, nil
				})
			case "publisher_session_changed", "followup_prepared_late":
				path := filepath.Join(t.TempDir(), "late-session.jsonl")
				if err = os.WriteFile(path, []byte("different valid local session\n"), 0600); err != nil {
					t.Fatal(err)
				}
				member := in.Members[0]
				if failure == "followup_prepared_late" {
					member = in.Members[1]
				}
				_, err = s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='active',session_file=$2 WHERE id=$1`, member.RuntimeID, path)
			case "commit_trigger_failure":
				_, err = s.Pool.Exec(ctx, `CREATE TRIGGER reject_switch_final_task_save BEFORE UPDATE ON task_records BEGIN SELECT RAISE(ABORT, 'synthetic_final_save_failure'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
				return nil, err
			})
			if err == nil {
				t.Fatal("final CAS/failure injection accepted")
			}
			var bindings, members, continuation, fences int
			if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns WHERE id=$1),(SELECT count(*) FROM task_lark_switch_fences)`, turn).Scan(&bindings, &members, &continuation, &fences); err != nil || bindings != 0 || members != 0 || continuation != 0 || fences != 2 {
				t.Fatal("failed/uncertain C lost coordination or published partial state", bindings, members, continuation, fences, err)
			}
			action, err := s.TaskLarkAction(ctx, in.TaskID, in.ActorKey, in.ActionID)
			if err != nil || action.Status != "pending" {
				t.Fatal("failed transaction falsely advertised success", err)
			}
		})
	}
}
