package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/taskruntime"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkSourceFenceAndFixedOverlay(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	src := &tasks.Source{Store: s}
	src.EnableLarkOverlay()
	publisher := in.Members[0]
	if _, err := src.ExecutionSnapshot(ctx, publisher.RuntimeID); err == nil {
		t.Fatal("Source passed durable A fence")
	}
	var original []byte
	if err := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, publisher.RuntimeID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A subsequent account change cannot repair or redirect a task snapshot.
	if _, err = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL,revision=revision+1 WHERE id=$1`, in.Capture.Target.AccountID); err != nil {
		t.Fatal(err)
	}
	out, err := src.ExecutionSnapshot(ctx, publisher.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	if out.LarkBinding == nil || out.LarkBinding.VersionID != in.Capture.Target.VersionID || out.LarkBinding.AgentID != publisher.AgentID || out.CredentialIdentity != "frozen-original;lark-overlay:1:"+in.Capture.Target.RuntimeVariantVersion+":"+in.Capture.Target.VersionID.String() {
		t.Fatal("Source lost fixed member identity/revision")
	}
	if _, retained := out.CLIPayloads["lark-cli"]; retained {
		t.Fatal("overlay retained legacy bot secret payload")
	}
	if out.ModelEnv["OPENAI_API_KEY"] != "synthetic-key" || out.ModelID != "fixture" || out.AgentID != publisher.RuntimeID || out.TaskID != in.TaskID {
		t.Fatal("Source changed model or task runtime")
	}
	var after []byte
	if err = s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, publisher.RuntimeID).Scan(&after); err != nil || !bytes.Equal(original, after) {
		t.Fatal("Source rewrote snapshot", err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"lost_original_permission", larkSwitchSnapshot(t, in.TaskID, publisher.AgentID, false)},
		{"replaced_original_snapshot", larkSwitchSnapshot(t, in.TaskID, publisher.AgentID, true)},
		{"corrupt_original", []byte(`{"broken":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2,snapshot_sha=encode(sha256($2),'hex') WHERE id=$1`, publisher.RuntimeID, tc.raw); err != nil {
				t.Fatal(err)
			}
			if _, err := src.ExecutionSnapshot(ctx, publisher.RuntimeID); err == nil {
				t.Fatal("overlay repaired invalid original snapshot")
			}
		})
	}
}

func TestTaskLarkSourceLatePrepareCannotPublishAcrossOverlay(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	var member store.TaskLarkFenceMember
	for _, m := range in.Members {
		if m.Role == "followup" {
			member = m
		}
	}
	inst, err := s.TaskInstance(ctx, member.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	// Deterministic normal-fork callbacks are a test barrier, not fake native
	// handoff proof. This original normal session is empty; no Pi/git is used.
	src := &tasks.Source{Store: s, WorkRoot: t.TempDir(), MaxSessionBytes: 1 << 20, PauseNormal: func(context.Context, uuid.UUID) (func(), error) {
		once.Do(func() { close(started) })
		<-release
		return func() {}, nil
	}, FreezeNormal: func(context.Context, uuid.UUID) error { return nil }}
	src.EnableLarkOverlay()
	done := make(chan error, 1)
	go func() { done <- src.Prepare(ctx, inst) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("prepare did not capture original revision")
	}
	if _, err = reserveLarkFence(ctx, s, in); err != nil {
		close(release)
		t.Fatal(err)
	}
	r, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	next := *r.Current()
	next.ID = uuid.New()
	next.Attempt++
	next.Status = "queued"
	next.Question, next.Output, next.EndedAt = nil, nil, nil
	run := uuid.New()
	next.RunIDs = []uuid.UUID{run}
	_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, run, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; !apperr.Is(err, "task_conflict") {
		t.Fatal("stale prepare wrote across committed overlay", err)
	}
	current, err := s.TaskInstance(ctx, inst.ID)
	if err != nil || current.Prepared || current.ForkSource != nil || !bytes.Equal(current.Snapshot, inst.Snapshot) {
		t.Fatal("C or late prepare forked followup", err)
	}
	// A later explicit workflow preparation starts afresh at the committed rev.
	src.PauseNormal = func(context.Context, uuid.UUID) (func(), error) { return func() {}, nil }
	if err = src.Prepare(ctx, current); err != nil {
		t.Fatal("current revision preparation rejected", err)
	}
}

func TestTaskLarkSourceUnselectedSchema7RetainsBaseBinding(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	publisher := in.Members[0]
	inst, err := s.TaskInstance(ctx, publisher.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := base.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	cfg := base.Configuration()
	record, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	project := record.View.Task.ProjectID
	cfg.Prompts.ProjectID = &project
	cfg.Prompts.ProjectVersion = 1
	target := in.Capture.Target
	creds.LarkBinding = &clicredential.LarkBinding{ProjectID: project, AccountID: target.AccountID, VersionID: target.VersionID, AgentID: publisher.AgentID, Brand: target.Identity.Brand, AppID: target.Identity.AppID, OpenID: target.Identity.OpenID, RuntimeVariantVersion: target.RuntimeVariantVersion}
	delete(creds.CLI, "lark-cli")
	captured, err := taskruntime.Capture(base.Scope(), uuid.New(), taskruntime.CaptureInput{Configuration: cfg, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := captured.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2,snapshot_sha=encode(sha256($2),'hex') WHERE id=$1`, inst.ID, raw); err != nil {
		t.Fatal(err)
	}
	src := &tasks.Source{Store: s}
	src.EnableLarkOverlay()
	out, err := src.ExecutionSnapshot(ctx, inst.ID)
	if err != nil || out.LarkBinding == nil || *out.LarkBinding != *creds.LarkBinding || out.CredentialIdentity != cfg.CredentialIdentity || len(out.CLIPayloads["lark-cli"].Files) != 0 || len(out.CLIPayloads["lark-cli"].Env) != 0 {
		t.Fatal("unselected base binding replaced or secret payload created", err)
	}
}
