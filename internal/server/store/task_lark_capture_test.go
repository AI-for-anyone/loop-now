package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

func larkSwitchSnapshot(t *testing.T, task, agent uuid.UUID, permitted bool) []byte {
	t.Helper()
	scope, _ := taskruntime.TaskScope(agent, task)
	clis := []string{}
	payloads := map[string]taskruntime.CredentialPayload{}
	if permitted {
		clis = append(clis, "lark-cli")
		payloads["lark-cli"] = taskruntime.CredentialPayload{Env: map[string]string{"LARK_APP_ID": "synthetic-app", "LARK_APP_SECRET": "synthetic-secret"}}
	}
	snap, err := taskruntime.Capture(scope, uuid.New(), taskruntime.CaptureInput{
		Configuration: taskruntime.Configuration{AgentID: agent, ConfigVersion: 1, CredentialIdentity: "frozen-original", Provider: "openai", ModelID: "fixture", CLIs: clis, Prompts: &prompts.Layers{Format: prompts.FormatV1, AgentVersion: 1}},
		Credentials:   taskruntime.Credentials{Model: map[string]string{"OPENAI_API_KEY": "synthetic-key"}, CLI: payloads},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTaskLarkCaptureOriginalScopeAndPermission(t *testing.T) {
	s := taskConsoleStore(t)
	task, _, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, []byte("synthetic original session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	member := store.TaskLarkFenceMember{AgentID: agent, RuntimeID: turn.RuntimeID, Role: "publisher"}
	var badCredentials map[string]any
	json.Unmarshal(larkSwitchSnapshot(t, task, agent, true), &badCredentials)
	badCredentials["credentials"].(map[string]any)["model"] = map[string]any{"1-invalid": "synthetic"}
	tampered, _ := json.Marshal(badCredentials)
	for _, tc := range []struct {
		name string
		raw  []byte
		ok   bool
	}{
		{"original-lark", larkSwitchSnapshot(t, task, agent, true), true},
		{"no-original-permission", larkSwitchSnapshot(t, task, agent, false), false},
		{"other-task", larkSwitchSnapshot(t, uuid.New(), agent, true), false},
		{"other-agent", larkSwitchSnapshot(t, task, uuid.New(), true), false},
		{"corrupt", []byte(`{"broken":true}`), false},
		{"invalid-original-credentials", tampered, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2,snapshot_sha=encode(sha256($2),'hex'),prepared=true,session_state='active',session_file=$3 WHERE id=$1`, turn.RuntimeID, tc.raw, session); err != nil {
				t.Fatal(err)
			}
			_, err := s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
				m.Unchanged()
				got, err := s.CaptureTaskLarkMemberTx(ctx, m, member)
				if err == nil {
					digest := sha256.Sum256(tc.raw)
					if got.BaseSnapshotSHA != hex.EncodeToString(digest[:]) || got.OriginalCredentialIdentity != "frozen-original" {
						t.Fatal("original capture facts changed")
					}
				}
				return nil, err
			})
			if (err == nil) != tc.ok {
				t.Fatalf("accepted=%v want=%v", err == nil, tc.ok)
			}
			var after []byte
			if err := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, turn.RuntimeID).Scan(&after); err != nil || !bytes.Equal(tc.raw, after) {
				t.Fatal("capture rewrote original snapshot")
			}
		})
	}
	valid := larkSwitchSnapshot(t, task, agent, true)
	if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2,snapshot_sha=encode(sha256($2),'hex'),prepared=false,session_state='none',session_file=NULL WHERE id=$1`, turn.RuntimeID, valid); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"publisher", "followup"} {
		member.Role = role
		_, err := s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			_, err := s.CaptureTaskLarkMemberTx(ctx, m, member)
			return nil, err
		})
		// This runtime has a real historical turn. Marking it unprepared does
		// not turn a lost session into a never-started future followup.
		if err == nil {
			t.Fatalf("unprepared member role %s accepted=%v", role, err == nil)
		}
	}
	var prepared bool
	if err := s.Pool.QueryRow(ctx, `SELECT prepared FROM task_instances WHERE id=$1`, turn.RuntimeID).Scan(&prepared); err != nil || prepared {
		t.Fatal("capture prepared or forked future followup")
	}
}

func TestTaskLarkCapturePreparedNeverRunFollowup(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	member := in.Members[1]
	if member.Role != "followup" {
		t.Fatal("fixture must be future member")
	}
	for _, tc := range []struct {
		name, state string
		file        *string
		ok          bool
	}{
		{"directory-prepared-only", "none", nil, true},
		{"lost-session", "missing", nil, false},
		{"active-without-file", "active", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state=$2,session_file=$3 WHERE id=$1`, member.RuntimeID, tc.state, tc.file); e != nil {
				t.Fatal(e)
			}
			_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				m.Unchanged()
				out, e := s.CaptureTaskLarkMemberTx(ctx, m, member)
				if e == nil && !out.Prepared {
					t.Fatal("capture erased original prepared")
				}
				return nil, e
			})
			if (e == nil) != tc.ok {
				t.Fatalf("accepted=%v expected=%v: %v", e == nil, tc.ok, e)
			}
		})
	}
	if _, e := s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='none',session_file=NULL WHERE id=$1`, member.RuntimeID); e != nil {
		t.Fatal(e)
	}
	var originalNode uuid.UUID
	if e := s.Pool.QueryRow(ctx, `SELECT (document->'view'->'task'->>'currentNodeRunId')::uuid FROM task_records WHERE id=$1`, in.TaskID).Scan(&originalNode); e != nil {
		t.Fatal(e)
	}
	_, e := s.Pool.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,text) VALUES($1,$2,$3,$4,$5,$6,'completed','inbox_reminder','original history')`, uuid.New(), in.TaskID, member.RuntimeID, originalNode, uuid.New(), uuid.New())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, e := s.CaptureTaskLarkMemberTx(ctx, m, member)
		return nil, e
	})
	if e == nil {
		t.Fatal("historical turn became future absent session")
	}
}
