package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sleepWaitTurn(t *testing.T, s *server, runtime, exclude uuid.UUID) store.TaskTurn {
	t.Helper()
	limit := time.Now().Add(8 * time.Second)
	for time.Now().Before(limit) {
		var id uuid.UUID
		if e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT id FROM task_turns WHERE runtime_id=$1 AND id<>$2 AND status='running' ORDER BY queue_seq DESC LIMIT 1`, runtime, exclude).Scan(&id); e == nil {
			turn, e := s.a.Store.TaskTurn(context.Background(), id)
			if e != nil {
				t.Fatal(e)
			}
			return turn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual task turn did not run")
	return store.TaskTurn{}
}
func sleepNativePrompts(t *testing.T, path string) int {
	t.Helper()
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return sleepNativeData(data)
}
func sleepNativeData(data []byte) int {
	count := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
			count++
		}
	}
	return count
}
func sleepTask(t *testing.T, s *server, description string) (taskmodel.Detail, auth.Principal) {
	t.Helper()
	ctx := context.Background()
	_, manager := s.createAgent("Manager")
	_, agent := s.createAgent("Worker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "acceptance sleep", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: description, AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	d := decode[taskmodel.Detail](t, raw)
	return d, auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: d.Task.ID, RuntimeID: d.Nodes[0].RuntimeID}
}

func TestSleepAppTaskWakeCommitAndExecutionRestart(t *testing.T) {
	t.Setenv("FAKEPI_PROMPT_DELAY", "800ms")
	for _, mode := range []string{"queued", "running", "conversation-queued", "conversation-running"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			w.cfg.ExecEnvPassthrough = append(w.cfg.ExecEnvPassthrough, "FAKEPI_PROMPT_DELAY")
			s := w.start(nil)
			defer func() {
				if s != nil {
					s.stop()
				}
			}()
			ctx := context.Background()
			description := " !sleep=800ms !text=source"
			conversation := strings.HasPrefix(mode, "conversation-")
			if conversation {
				description += " !error=blocked-fixture"
			}
			d, p := sleepTask(t, s, description)
			if conversation {
				taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
				if _, e := s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/Worker#"+d.Task.ID.String(), "ordinary continuation", "queue"); e != nil {
					t.Fatal(e)
				}
			}
			source := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
			reason := " !sleep=2s !text=wake"
			receipt, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120, Reason: &reason}, 1000)
			if e != nil {
				t.Fatal(e)
			}
			waiting := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
			originalWake := *waiting.SleepingInstances[0].WakeAt
			// Stop only this owned App. Commit the real scheduler transaction through
			// an independent connection before any driver exists to claim its work.
			s.stop()
			s = nil
			st, e := store.Open(ctx, w.cfg.DatabaseURL, store.OpenOptions{SearchPath: w.schema})
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			if _, e = st.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); e != nil {
				t.Fatal(e)
			}
			if e = st.ReconcileSleeps(ctx, 100); e != nil {
				t.Fatal(e)
			}
			var wake uuid.UUID
			if e = st.Pool.QueryRow(ctx, `SELECT wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&wake); e != nil {
				t.Fatal(e)
			}
			turn, e := st.TaskTurn(ctx, wake)
			if e != nil || turn.Status != "queued" {
				t.Fatal("wake not durably queued before restart", turn, e)
			}
			s = w.start(nil)
			if strings.HasSuffix(mode, "running") {
				running := sleepWaitTurn(t, s, p.RuntimeID, source.ID)
				if running.ID != wake {
					t.Fatal("carrier identity changed")
				}
				inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
				if e != nil || inst.SessionFile == nil {
					t.Fatal(e)
				}
				limit := time.Now().Add(3 * time.Second)
				for sleepNativePrompts(t, *inst.SessionFile) != 2+boolInt(conversation) && time.Now().Before(limit) {
					time.Sleep(10 * time.Millisecond)
				}
				if n := sleepNativePrompts(t, *inst.SessionFile); n != 2+boolInt(conversation) {
					t.Fatal("wake did not reach real RPC", n)
				}
				s.stop()
				s = nil
				s = w.start(nil)
			}
			want := "completed"
			if strings.HasSuffix(mode, "running") {
				want = "interrupted"
			}
			limit := time.Now().Add(7 * time.Second)
			for time.Now().Before(limit) {
				turn, e = s.a.Store.TaskTurn(ctx, wake)
				if e != nil {
					t.Fatal(e)
				}
				if turn.Status == want {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if turn.Status != want {
				t.Fatal("wake recovery", mode, turn.Status)
			}
			time.Sleep(150 * time.Millisecond)
			var count int
			if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, p.RuntimeID).Scan(&count); e != nil || count != 1 {
				t.Fatal("duplicate timer", count, e)
			}
			inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
			if e != nil || inst.SessionFile == nil {
				t.Fatal(e)
			}
			if n := sleepNativePrompts(t, *inst.SessionFile); n != 2+boolInt(conversation) {
				t.Fatal("old execution replayed", n)
			}
			got, e := s.a.Store.GetTask(ctx, d.Task.ID)
			if e != nil {
				t.Fatal(e)
			}
			if (strings.HasSuffix(mode, "running") || conversation) && got.View.Task.Status != "blocked" {
				t.Fatal("restart changed interrupted-node semantics")
			}
			t.Logf("case=task-wake-%s sleepId=%s originalWakeAt=%s wakeRunId=%s status=%s PiPrompts=%d timers=1", mode, receipt.Sleep.ID, originalWake.Format(time.RFC3339Nano), wake, turn.Status, 2+boolInt(conversation))
			if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestSleepAppRetireAndOwnerDeletionRejectOldTimer(t *testing.T) {
	t.Run("retired-task", func(t *testing.T) {
		w := newWorld(t)
		w.cfg.SleepScanInterval = 50 * time.Millisecond
		w.cfg.SleepScanBatch = 100
		s := w.start(nil)
		defer s.stop()
		ctx := context.Background()
		d, p := sleepTask(t, s, " !sleep=800ms !text=source")
		source := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
		receipt, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
		if e != nil {
			t.Fatal(e)
		}
		waiting := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
		originalWake := *waiting.SleepingInstances[0].WakeAt
		if e = s.a.Store.SoftDeleteAgent(ctx, p.ID); e == nil {
			t.Fatal("sleep bypassed unfinished-task delete guard")
		}
		if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
			t.Fatal(e)
		}
		taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
		inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
		if e != nil || !inst.Retired {
			t.Fatal("instance not retired", e)
		}
		if _, e = os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", p.RuntimeID.String())); !os.IsNotExist(e) {
			t.Fatal("retired directory remains", e)
		}
		if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); e != nil {
			t.Fatal(e)
		}
		if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
			t.Fatal(e)
		}
		var timers int
		if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, p.RuntimeID).Scan(&timers); e != nil || timers != 0 {
			t.Fatal("retired timer revived", timers, e)
		}
		if _, e = s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 1}, 1000); e == nil {
			t.Fatal("retired source accepted")
		}
		var archive []byte
		if e = s.a.Store.Pool.QueryRow(ctx, `SELECT b.data FROM task_archives a JOIN blobs b ON b.sha256=a.blob_sha256 WHERE a.runtime_id=$1`, p.RuntimeID).Scan(&archive); e != nil {
			t.Fatal(e)
		}
		var archived struct {
			Session []byte `json:"session"`
		}
		if e = json.Unmarshal(archive, &archived); e != nil {
			t.Fatal(e)
		}
		if n := sleepNativeData(archived.Session); n != 1 {
			t.Fatal("retired history replayed", n)
		}
		var liveTokens int
		if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_tokens WHERE runtime_id=$1 AND revoked_at IS NULL`, p.RuntimeID).Scan(&liveTokens); e != nil || liveTokens != 0 {
			t.Fatal("retired live token", liveTokens, e)
		}
		t.Logf("case=retired-task sleepId=%s originalWakeAt=%s wakeRunId=null PiPrompts=1 retired=true timers=0", receipt.Sleep.ID, originalWake.Format(time.RFC3339Nano))
	})
	t.Run("deleted-owner-queued-timer", func(t *testing.T) {
		w := newWorld(t)
		w.cfg.SleepScanInterval = 50 * time.Millisecond
		w.cfg.SleepScanBatch = 100
		s := w.start(nil)
		defer func() {
			if s != nil {
				s.stop()
			}
		}()
		ctx := context.Background()
		path, a := s.createAgent("DeletedOwner")
		s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=800ms !text=source"})
		source := s.waitRunning(path)
		receipt, e := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: a.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
		if e != nil {
			t.Fatal(e)
		}
		waiting := s.waitIdle(path)
		if waiting.Sleep == nil {
			t.Fatal("no sleep")
		}
		originalWake := *waiting.Sleep.WakeAt
		sess, e := s.a.Store.GetAgentSession(ctx, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		if n := sleepNativePrompts(t, sess.SessionFile); n != 1 {
			t.Fatal(n)
		}
		s.stop()
		s = nil
		st, e := store.Open(ctx, w.cfg.DatabaseURL, store.OpenOptions{SearchPath: w.schema})
		if e != nil {
			t.Fatal(e)
		}
		defer st.Close()
		if _, e = st.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); e != nil {
			t.Fatal(e)
		}
		if e = st.ReconcileSleeps(ctx, 100); e != nil {
			t.Fatal(e)
		}
		var wake uuid.UUID
		if e = st.Pool.QueryRow(ctx, `SELECT wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&wake); e != nil {
			t.Fatal(e)
		}
		if e = st.SoftDeleteAgent(ctx, a.ID); e != nil {
			t.Fatal(e)
		}
		s = w.start(nil)
		if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
			t.Fatal(e)
		}
		time.Sleep(250 * time.Millisecond)
		run, e := s.a.Store.GetRun(ctx, wake)
		if e != nil || run.Status != "interrupted" || run.EndReason != "sleep_invalidated" {
			t.Fatal("old queued timer not invalidated", run, e)
		}
		var total int
		if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1`, a.ID).Scan(&total); e != nil || total != 2 {
			t.Fatal("deleted owner revived", total, e)
		}
		if s.a.Executor.Process(a.ID).State != "stopped" {
			t.Fatal("deleted owner started Pi")
		}
		t.Logf("case=owner-delete sleepId=%s originalWakeAt=%s wakeRunId=%s PiPrompts=1 timerStatus=interrupted", receipt.Sleep.ID, originalWake.Format(time.RFC3339Nano), wake)
	})
}

func TestSleepAppNoncurrentConversationAndWorkflowWorkWake(t *testing.T) {
	t.Setenv("FAKEPI_PROMPT_DELAY", "800ms")
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	w.cfg.ExecEnvPassthrough = append(w.cfg.ExecEnvPassthrough, "FAKEPI_PROMPT_DELAY")
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	_, m := s.createAgent("Manager")
	_, a := s.createAgent("First")
	_, b := s.createAgent("Next")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": m.ID})
	steps := []workflow.Step{}
	for i, agent := range []uuid.UUID{a.ID, b.ID} {
		id := []string{"first", "next"}[i]
		steps = append(steps, workflow.Step{ID: id, Title: id, Description: " !sleep=800ms !text=node", AgentID: agent, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}})
	}
	raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: m.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "noncurrent ordinary sleep", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "first", Steps: steps, Edges: []workflow.Edge{{ID: "next", From: "first", To: "next", Label: "next"}}}})
	if e != nil {
		t.Fatal(e)
	}
	d := decode[taskmodel.Detail](t, raw)
	nodeA := d.Nodes[0]
	pa := auth.Principal{Kind: auth.KindAgent, ID: a.ID, TaskID: d.Task.ID, RuntimeID: nodeA.RuntimeID}
	var runtimeB uuid.UUID
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_instances WHERE task_id=$1 AND agent_id=$2`, d.Task.ID, b.ID).Scan(&runtimeB); e != nil {
		t.Fatal(e)
	}
	pb := auth.Principal{Kind: auth.KindAgent, ID: b.ID, TaskID: d.Task.ID, RuntimeID: runtimeB}
	turnA := sleepWaitTurn(t, s, pa.RuntimeID, uuid.Nil)
	sleepA, e := s.a.Store.RequestSleep(ctx, pa, sleepmodel.Input{ActionID: uuid.New(), RunID: turnA.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
	if _, e = s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/Next#"+d.Task.ID.String(), "ordinary conversation before assigned node", "queue"); e != nil {
		t.Fatal(e)
	}
	turnB := sleepWaitTurn(t, s, runtimeB, uuid.Nil)
	reason := " !sleep=800ms !text=ordinary-timer"
	first, e := s.a.Store.RequestSleep(ctx, pb, sleepmodel.Input{ActionID: uuid.New(), RunID: turnB.ID, Seconds: 1, Reason: &reason}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	both := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 2 })
	if both.Task.Status != "waiting" || both.Task.CurrentNodeRunID == nil || *both.Task.CurrentNodeRunID != nodeA.ID || both.Nodes[0].Sleep == nil || both.Nodes[0].Sleep.ID != sleepA.Sleep.ID || first.Sleep.NodeRunID != nil {
		t.Fatal("ordinary sleep changed current node")
	}
	timerB := sleepWaitTurn(t, s, runtimeB, turnB.ID)
	if timerB.Origin != "sleep_wake" || timerB.NodeRunID != uuid.Nil {
		t.Fatal("ordinary timer became workflow node", timerB)
	}
	second, e := s.a.Store.RequestSleep(ctx, pb, sleepmodel.Input{ActionID: uuid.New(), RunID: timerB.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	both = taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool {
		for _, r := range d.SleepingInstances {
			if r.ID == second.Sleep.ID {
				return true
			}
		}
		return false
	})
	if both.Nodes[0].Sleep == nil || both.Nodes[0].Sleep.ID != sleepA.Sleep.ID || second.Sleep.Revision <= first.Sleep.Revision {
		t.Fatal("unrelated scope changed")
	}
	inst, e := s.a.Store.TaskInstance(ctx, runtimeB)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	beforeSession := *inst.SessionFile
	if n := sleepNativePrompts(t, beforeSession); n != 2 {
		t.Fatal("ordinary continuation replay", n)
	}
	// Wake A through real Inbox; its submitted output advances to B only after
	// A successfully settles. B's existing ordinary sleep is consumed by this
	// actual new workflow work, not by a synthetic wake call.
	if _, e = s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/First#"+d.Task.ID.String(), "continue assigned node", "queue"); e != nil {
		t.Fatal(e)
	}
	continuedA := sleepWaitTurn(t, s, pa.RuntimeID, turnA.ID)
	edge := "next"
	if _, e = s.a.Tasks.Submit(ctx, pa, d.Task.ID, nodeA.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}, EdgeID: &edge}); e != nil {
		t.Fatal(e)
	}
	next := sleepWaitTurn(t, s, runtimeB, timerB.ID)
	if next.ID == turnB.ID || next.NodeRunID == uuid.Nil || next.WakeSleepID == nil || *next.WakeSleepID != second.Sleep.ID || next.Origin == "sleep_wake" {
		t.Fatal("workflow work did not carry ordinary wake", next)
	}
	final := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.Nodes) == 2 })
	if final.Task.Status == "blocked" || final.Task.Status == "completed" || final.Nodes[1].AgentID != b.ID || final.Nodes[0].Status != "completed" {
		t.Fatal("workflow behavior changed")
	}
	var state, why, mode string
	var wake uuid.UUID
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT state,end_reason,wake_run_id,mode FROM agent_sleeps WHERE id=$1`, second.Sleep.ID).Scan(&state, &why, &wake, &mode); e != nil || state != "woken" || why != "queued_work" || wake != next.ID || mode != "task_conversation" {
		t.Fatal("wrong workflow wake", state, why, mode, e)
	}
	var executionMode string
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT execution_mode FROM task_turns WHERE id=$1`, next.ID).Scan(&executionMode); e != nil || executionMode != "task_node" {
		t.Fatal("new workflow work used ordinary driver", executionMode, e)
	}
	inst, e = s.a.Store.TaskInstance(ctx, runtimeB)
	if e != nil || inst.SessionFile == nil || *inst.SessionFile != beforeSession {
		t.Fatal("instance session changed", e)
	}
	t.Logf("case=noncurrent-ordinary/workflow sleepId=%s oldSleepId=%s workflowWakeRunId=%s previousNodeCarrier=%s PiPromptsBeforeWorkflow=2 taskNotBlocked=true", second.Sleep.ID, first.Sleep.ID, next.ID, continuedA.ID)
	if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
}

func TestSleepAppPlatformCompletionNoticeWakesManager(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	d, p := sleepTask(t, s, " !sleep=2s !text=worker")
	var manager uuid.UUID
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT manager_agent_id FROM projects WHERE id=(SELECT project_id FROM task_records WHERE id=$1)`, d.Task.ID).Scan(&manager); e != nil {
		t.Fatal(e)
	}
	path := "/agents/" + manager.String()
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=600ms !text=manager-source"})
	source := s.waitRunning(path)
	receipt, e := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager}, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	waiting := s.waitIdle(path)
	if waiting.Sleep == nil {
		t.Fatal("manager not sleeping")
	}
	turn := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	if _, e = s.a.Tasks.Submit(ctx, p, d.Task.ID, d.Nodes[0].ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" })
	limit := time.Now().Add(8 * time.Second)
	var wake uuid.UUID
	for time.Now().Before(limit) {
		e = s.a.Store.Pool.QueryRow(ctx, `SELECT wake_run_id FROM agent_sleeps WHERE id=$1 AND state='woken' AND end_reason='queued_work'`, receipt.Sleep.ID).Scan(&wake)
		if e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("platform notice did not wake manager", e)
	}
	s.waitIdle(path)
	run, e := s.a.Store.GetRun(ctx, wake)
	if e != nil || run.Origin != "inbox_reminder" || run.WakeSleepID == nil || *run.WakeSleepID != receipt.Sleep.ID || run.Status != "completed" {
		t.Fatal("notice carrier", run, e)
	}
	var notices, timers int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages i JOIN task_notifications n ON i.id=n.message_id WHERE n.task_id=$1 AND i.from_kind='platform' AND i.to_agent_id=$2`, d.Task.ID, manager).Scan(&notices); e != nil || notices != 1 {
		t.Fatal("platform notice duplicate", notices, e)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='sleep_wake'`, manager).Scan(&timers); e != nil || timers != 0 {
		t.Fatal("extra manager timer", timers, e)
	}
	sess, e := s.a.Store.GetAgentSession(ctx, manager)
	if e != nil {
		t.Fatal(e)
	}
	if n := sleepNativePrompts(t, sess.SessionFile); n != 2 {
		t.Fatal("notice not actual RPC or replay", n)
	}
	t.Logf("case=platform-completion sleepId=%s originalWakeAt=%s wakeRunId=%s workerRun=%s PiPrompts=2 notices=1 timers=0", receipt.Sleep.ID, waiting.Sleep.WakeAt.Format(time.RFC3339Nano), wake, turn.ID)
}

func TestSleepAppProjectDeletionCancelsOldSleep(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "DeleteSleepingProject"}))
	models := decode[apitypes.ModelList](t, s.must(200, "GET", "/model-providers/anthropic/models", nil))
	a := decode[apitypes.Agent](t, s.must(201, "POST", "/projects/"+project.ID.String()+"/agents", map[string]any{"name": "Sleeper", "model": map[string]string{"provider": "anthropic", "modelId": models.Models[0].ID}}))
	path := "/agents/" + a.ID.String()
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=800ms !text=source"})
	source := s.waitRunning(path)
	p := auth.Principal{Kind: auth.KindAgent, ID: a.ID}
	receipt, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	waiting := s.waitIdle(path)
	if waiting.Sleep == nil {
		t.Fatal("no sleep")
	}
	sess, e := s.a.Store.GetAgentSession(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if n := sleepNativePrompts(t, sess.SessionFile); n != 1 {
		t.Fatal(n)
	}
	s.must(204, "DELETE", "/projects/"+project.ID.String(), nil)
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	limit := time.Now().Add(4 * time.Second)
	for s.a.Executor.Process(a.ID).State != "stopped" && time.Now().Before(limit) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.a.Executor.Process(a.ID).State != "stopped" {
		t.Fatal("deleted project Pi remains")
	}
	var state, reason string
	var wake *uuid.UUID
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT state,end_reason,wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state, &reason, &wake); e != nil || state != "cancelled" || reason != "owner_deleted" || wake != nil {
		t.Fatal("project sleep not cancelled", state, reason, e)
	}
	var total int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1`, a.ID).Scan(&total); e != nil || total != 1 {
		t.Fatal("project timer revived", total, e)
	}
	if _, e = s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 1}, 1000); e == nil {
		t.Fatal("deleted project source accepted")
	}
	t.Logf("case=project-delete sleepId=%s originalWakeAt=%s wakeRunId=null PiPrompts=1 timers=0", receipt.Sleep.ID, waiting.Sleep.WakeAt.Format(time.RFC3339Nano))
}

func TestSleepAppQueuedTaskTimerCannotSurviveRetirement(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	d, p := sleepTask(t, s, " !sleep=800ms !text=source")
	source := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	receipt, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	waiting := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
	// Deterministically reject only the timer's claim transaction. Cancellation
	// and cleanup remain the real App paths and are not replaced by callbacks.
	if _, e = s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION hold_timer_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.origin='sleep_wake' AND NEW.status='starting' THEN RAISE EXCEPTION 'offline timer claim barrier'; END IF; RETURN NEW; END $$; CREATE TRIGGER hold_timer_claim BEFORE UPDATE ON task_turns FOR EACH ROW EXECUTE FUNCTION hold_timer_claim()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	var wake uuid.UUID
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&wake); e != nil {
		t.Fatal(e)
	}
	turn, e := s.a.Store.TaskTurn(ctx, wake)
	if e != nil || turn.Status != "queued" {
		t.Fatal("claim barrier absent", turn, e)
	}
	if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
	if _, e = s.a.Store.Pool.Exec(ctx, `DROP TRIGGER hold_timer_claim ON task_turns; DROP FUNCTION hold_timer_claim()`); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	time.Sleep(200 * time.Millisecond)
	inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || !inst.Retired {
		t.Fatal("not retired", e)
	}
	turn, e = s.a.Store.TaskTurn(ctx, wake)
	if e != nil || turn.Status != "interrupted" {
		t.Fatal("queued timer revived after retirement", turn, e)
	}
	var archived struct {
		Session []byte `json:"session"`
	}
	var data []byte
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT b.data FROM task_archives a JOIN blobs b ON b.sha256=a.blob_sha256 WHERE a.runtime_id=$1`, p.RuntimeID).Scan(&data); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &archived); e != nil {
		t.Fatal(e)
	}
	if n := sleepNativeData(archived.Session); n != 1 {
		t.Fatal("retired queued timer reached Pi", n)
	}
	var timers, liveTokens int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, p.RuntimeID).Scan(&timers); e != nil || timers != 1 {
		t.Fatal("replacement timer after retirement", timers, e)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_tokens WHERE runtime_id=$1 AND revoked_at IS NULL`, p.RuntimeID).Scan(&liveTokens); e != nil || liveTokens != 0 {
		t.Fatal("live retired token", liveTokens, e)
	}
	if _, e = os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", p.RuntimeID.String())); !os.IsNotExist(e) {
		t.Fatal("retired directory remains", e)
	}
	t.Logf("case=retired-queued-timer sleepId=%s originalWakeAt=%s wakeRunId=%s status=interrupted PiPrompts=1 liveTokens=0", receipt.Sleep.ID, waiting.SleepingInstances[0].WakeAt.Format(time.RFC3339Nano), wake)
}

type sleepFairnessLog struct{ failures atomic.Int32 }

func (w *sleepFairnessLog) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("sleep_reconcile_failed")) {
		w.failures.Add(1)
	}
	return len(data), nil
}

func TestSleepAppBatchOnePersistentFailureDoesNotStarve(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 30 * time.Millisecond
	w.cfg.SleepScanBatch = 1
	log := &sleepFairnessLog{}
	w.logTo = log
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	type fixture struct {
		path  string
		agent uuid.UUID
		sleep sleepmodel.Record
	}
	scopes := []fixture{}
	for _, name := range []string{"FailingFirst", "HealthyLater"} {
		path, a := s.createAgent(name)
		s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=600ms !text=source"})
		source := s.waitRunning(path)
		receipt, e := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: a.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120}, 1000)
		if e != nil {
			t.Fatal(e)
		}
		waiting := s.waitIdle(path)
		if waiting.Sleep == nil {
			t.Fatal("source did not activate sleep")
		}
		scopes = append(scopes, fixture{path, a.ID, receipt.Sleep})
	}
	bad, healthy := scopes[0], scopes[1]
	if _, e := s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION reject_first_sleep_app() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='`+bad.sleep.ID.String()+`'::uuid THEN RAISE EXCEPTION 'offline persistent candidate failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_first_sleep_app BEFORE UPDATE ON agent_sleeps FOR EACH ROW WHEN (OLD.state='sleeping' AND NEW.state='woken') EXECUTE FUNCTION reject_first_sleep_app()`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-CASE WHEN id=$1 THEN interval '10 seconds' ELSE interval '5 seconds' END WHERE id IN ($1,$2)`, bad.sleep.ID, healthy.sleep.ID); e != nil {
		t.Fatal(e)
	}
	limit := time.Now().Add(6 * time.Second)
	var wake uuid.UUID
	var status string
	for time.Now().Before(limit) {
		e := s.a.Store.Pool.QueryRow(ctx, `SELECT r.id,r.status FROM runs r WHERE r.wake_sleep_id=$1`, healthy.sleep.ID).Scan(&wake, &status)
		if e == nil && status == "completed" && log.failures.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "completed" || log.failures.Load() < 2 {
		t.Fatal("persistent first scope starved actual App dispatch", status, log.failures.Load())
	}
	s.waitIdle(healthy.path)
	var badState string
	var badCount, goodCount int
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, bad.sleep.ID).Scan(&badState); e != nil || badState != "sleeping" {
		t.Fatal("failure was swallowed or sleep lost", badState, e)
	}
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE wake_sleep_id=$1),count(*) FILTER(WHERE wake_sleep_id=$2) FROM runs`, bad.sleep.ID, healthy.sleep.ID).Scan(&badCount, &goodCount); e != nil || badCount != 0 || goodCount != 1 {
		t.Fatal("fault leaked or duplicated carrier", badCount, goodCount, e)
	}
	sess, e := s.a.Store.GetAgentSession(ctx, healthy.agent)
	if e != nil {
		t.Fatal(e)
	}
	if n := sleepNativePrompts(t, sess.SessionFile); n != 2 {
		t.Fatal("healthy real RPC lost/replayed", n)
	}
	t.Logf("case=batch1-live-scanner failingSleepId=%s healthySleepId=%s wakeRunId=%s loggedFailures=%d healthyPiPrompts=2 healthyTimers=1", bad.sleep.ID, healthy.sleep.ID, wake, log.failures.Load())
	if _, e = s.a.Store.Pool.Exec(ctx, `DROP TRIGGER reject_first_sleep_app ON agent_sleeps; DROP FUNCTION reject_first_sleep_app()`); e != nil {
		t.Fatal(e)
	}
}
