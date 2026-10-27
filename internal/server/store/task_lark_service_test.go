package store_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkServicePhaseACapturesAndSerializesRealTransactions(t *testing.T) {
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	// Caller-controlled actor/hash/metadata cannot replace trusted capture.
	in.ActorKey = "forged-agent"
	in.Capture.Target.Identity.OpenID = "forged-user"
	in.Capture.Members[0].SessionFile = nil
	var wg sync.WaitGroup
	created := make(chan bool, 6)
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, newAction, err := svc.ReserveLarkSwitch(ctx, auth.Console(), in)
			if err == nil && (state.Status != "pending" || state.ActionID != in.ActionID) {
				t.Error("wrong original action state")
			}
			created <- newAction
			errs <- err
		}()
	}
	wg.Wait()
	close(created)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	newCount := 0
	for v := range created {
		if v {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatal("phase A did not serialize action", newCount)
	}
	var actor string
	var raw []byte
	var actions, fences, bindings, turns int
	if err := s.Pool.QueryRow(ctx, `SELECT actor_key,captured_request FROM task_lark_switch_actions WHERE action_id=$1`, in.ActionID).Scan(&actor, &raw); err != nil {
		t.Fatal(err)
	}
	var capture store.TaskLarkSwitchCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	if actor == in.ActorKey || capture.Target.Identity.OpenID != "synthetic-user" || capture.Target.Identity.GrantedScopes != nil || capture.Members[0].SessionFile == nil && capture.Members[0].Role == "publisher" {
		t.Fatal("caller metadata replaced real task capture")
	}
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, in.TaskID).Scan(&actions, &fences, &bindings, &turns); err != nil || actions != 1 || fences != 2 || bindings != 0 || turns != 1 {
		t.Fatal("A created partial/repeated publication", actions, fences, bindings, turns, err)
	}
	changed := in
	changed.ExpectedTaskVersion++
	if _, _, err := svc.ReserveLarkSwitch(ctx, auth.Console(), changed); !apperr.Is(err, "task_conflict") {
		t.Fatal("same action changed body accepted", err)
	}
	// A replay recovers its original reservation even if account current changed
	// afterwards; it must not recapture a replacement or issue another action.
	if _, err := s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL,revision=revision+1 WHERE id=$1`, in.Capture.Target.AccountID); err != nil {
		t.Fatal(err)
	}
	if state, created, err := svc.ReserveLarkSwitch(ctx, auth.Console(), in); err != nil || created || state.Status != "pending" {
		t.Fatal("replay followed account current", err)
	}
}

func TestTaskLarkServicePhaseARejectsIdentityAndStaleExpectation(t *testing.T) {
	for _, name := range []string{"external", "agent", "snapshot_cas", "binding_cas", "target_revision", "missing_capture"} {
		t.Run(name, func(t *testing.T) {
			s, in := larkSwitchReadyFixture(t, nil)
			ctx := context.Background()
			svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
			defer svc.Stop(ctx)
			p := auth.Console()
			switch name {
			case "external":
				p = auth.External(uuid.New(), "external")
			case "agent":
				p = auth.Principal{Kind: auth.KindAgent, ID: in.Members[0].AgentID, RuntimeID: in.Members[0].RuntimeID, TaskID: in.TaskID}
			case "snapshot_cas":
				in.Capture.Members[0].BaseSnapshotSHA = "wrong-sha"
			case "binding_cas":
				in.Capture.Members[1].PreviousRevision++
			case "target_revision":
				in.Capture.Target.AccountRevision++
			case "missing_capture":
				in.Capture = nil
			}
			if _, _, err := svc.ReserveLarkSwitch(ctx, p, in); err == nil {
				t.Fatal("invalid phase A accepted")
			}
			var actions, fences int
			if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences)`).Scan(&actions, &fences); err != nil || actions != 0 || fences != 0 {
				t.Fatal("rejected A left effects", actions, fences, err)
			}
		})
	}
}
