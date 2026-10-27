package app_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/redistest"
)

// Actual temporary App, TEST PG/Redis and fakePi RPC. No real models, database
// providers, existing service/task operations or shared helper modifications.
func taskConsoleApp(t *testing.T) (*world, *server) {
	t.Helper()
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	w.cfg.PiIdleTimeout = 100 * time.Millisecond
	s := w.start(nil)
	c := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(201, "POST", "/projects/"+c.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "strict-node synthetic model account", "credential": map[string]string{"type": "api_key", "key": "synthetic-task-console-key"}})
	t.Logf("strict App schema=%s redisPrefix=%s console=%s", w.schema, w.cfg.RedisKeyPrefix, s.url)
	return w, s
}

func taskConsoleRoute(task, node uuid.UUID) string {
	return "/tasks/" + task.String() + "/nodes/" + node.String()
}
func taskConsoleInput(n taskmodel.NodeRun, action uuid.UUID, text, mode string) map[string]any {
	b := map[string]any{"actionId": action, "expectedAgentId": n.AgentID, "expectedRuntimeId": n.RuntimeID, "text": text}
	if mode != "" {
		b["whenBusy"] = mode
	}
	return b
}
func taskConsoleSend(t *testing.T, s *server, task uuid.UUID, n taskmodel.NodeRun, action uuid.UUID, text, mode string) taskmodel.TaskNodeMessageReceipt {
	t.Helper()
	return decode[taskmodel.TaskNodeMessageReceipt](t, s.must(201, "POST", taskConsoleRoute(task, n.ID)+"/messages", taskConsoleInput(n, action, text, mode)))
}
func taskConsoleWaitRun(t *testing.T, s *server, run uuid.UUID, status string) store.TaskTurn {
	t.Helper()
	ctx := context.Background()
	for until := time.Now().Add(10 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		r, e := s.a.Store.TaskTurn(ctx, run)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status == status {
			return r
		}
		if r.Status == "failed" || r.Status == "interrupted" {
			t.Fatalf("strict run reached %s before %s; safeCode=%v", r.Status, status, r.ErrorCode)
		}
	}
	t.Fatal("strict run wait timeout: " + status)
	return store.TaskTurn{}
}

func TestTaskConsoleAppAcceptedSwitchStrictHistory(t *testing.T) {
	_, s := taskConsoleApp(t)
	defer s.stop()
	ctx := context.Background()
	_, manager := s.createAgent("StrictManager")
	_, worker := s.createAgent("StrictWorker")
	c := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+c.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	def := workflow.Definition{StartStepID: "a", Steps: []workflow.Step{{ID: "a", Title: "first", AgentID: worker.ID, Description: " !sleep=1s !text=first", InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}, {ID: "b", Title: "second", AgentID: worker.ID, Description: " !sleep=300ms !error=second_failed", InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{{ID: "next", From: "a", To: "b"}}}
	raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "strict target switch", Request: map[string]any{}, Workflow: def})
	if e != nil {
		t.Fatal(e)
	}
	d := decode[taskmodel.Detail](t, raw)
	running := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	n := running.Nodes[0]
	cap := decode[taskmodel.TaskNodeMessageCapability](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/message-capability", nil))
	if !cap.CanSend {
		t.Fatal("actual original model facts rejected", cap)
	}
	action := uuid.New()
	marker := "NEVER_EXECUTE_OLD_NODE_" + uuid.NewString()
	receipt := taskConsoleSend(t, s, d.Task.ID, n, action, marker, "")
	if receipt.NodeRunID != n.ID || receipt.RuntimeID != n.RuntimeID || receipt.Message.Text != marker || receipt.Message.Origin != "console_node" {
		t.Fatal("receipt target differs")
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: d.Task.ID, RuntimeID: n.RuntimeID}
	edge := "next"
	if _, e = s.a.Tasks.Submit(ctx, p, d.Task.ID, n.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}, EdgeID: &edge}); e != nil {
		t.Fatal(e)
	}
	advanced := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.Nodes) == 2 && d.Task.Status == "blocked" })
	if advanced.Nodes[1].RuntimeID != n.RuntimeID {
		t.Fatal("test did not exercise same runtime reuse")
	}
	current := advanced.Nodes[1]
	var beforeTurns int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, d.Task.ID).Scan(&beforeTurns); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		field  string
		value  any
		status int
	}{{"expectedAgentId", uuid.New(), 409}, {"expectedRuntimeId", uuid.New(), 409}, {"actionId", uuid.Nil, 422}, {"whenBusy", nil, 422}, {"options", []string{"not_an_answer"}, 422}} {
		body := taskConsoleInput(current, uuid.New(), "invalid target or field", "queue")
		body[tc.field] = tc.value
		status, b := s.call("POST", taskConsoleRoute(d.Task.ID, current.ID)+"/messages", body)
		if status != tc.status {
			t.Fatalf("%s: got %d expected%d: %s", tc.field, status, tc.status, b)
		}
	}
	var afterTurns int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, d.Task.ID).Scan(&afterTurns); e != nil || afterTurns != beforeTurns {
		t.Fatal("rejected target/input wrote a console turn", e)
	}
	missingStatus, missingBody := s.call("GET", taskConsoleRoute(d.Task.ID, uuid.New())+"/message-capability", nil)
	if missingStatus != 404 || strings.Contains(string(missingBody), "接口不存在") {
		t.Fatal("missing node confused with unsupported route")
	}
	old, e := s.a.Store.TaskTurn(ctx, receipt.RunID)
	if e != nil || old.Status != "interrupted" || old.NodeRunID != n.ID || old.ErrorCode == nil || *old.ErrorCode != "task_conflict" {
		t.Fatal("accepted old input lacks safe nonexecution history", e)
	}
	inst, e := s.a.Store.TaskInstance(ctx, n.RuntimeID)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	native, e := os.ReadFile(*inst.SessionFile)
	if e != nil || strings.Contains(string(native), marker) {
		t.Fatal("old text entered the reused runtime session", e)
	}
	replay := taskConsoleSend(t, s, d.Task.ID, n, action, marker, "queue")
	if replay.RunID != receipt.RunID {
		t.Fatal("stable receipt rebound after current changed")
	}
	status, b := s.call("POST", taskConsoleRoute(d.Task.ID, n.ID)+"/messages", taskConsoleInput(n, uuid.New(), "new stale request", "queue"))
	if status != 409 || !strings.Contains(string(b), "node_changed") {
		t.Fatalf("old target should reject: %d %s", status, b)
	}
	cap = decode[taskmodel.TaskNodeMessageCapability](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/message-capability", nil))
	if cap.CanSend {
		t.Fatal("historical capability enabled")
	}
	s.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
	status, _ = s.call("POST", taskConsoleRoute(d.Task.ID, n.ID)+"/messages", taskConsoleInput(n, uuid.New(), "ended strict", "queue"))
	if status != 409 {
		t.Fatal("ended strict request accepted")
	}
	legacy, e := s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/StrictWorker#"+d.Task.ID.String(), "legacy ended task ordinary route", "queue")
	if e != nil || legacy.To.ID != manager.ID || legacy.To.RuntimeID != uuid.Nil {
		t.Fatal("ordinary Inbox ended-task Manager routing regressed", e)
	}
	t.Log("actual accepted-before-dispatch same-runtime step switch kept original interrupted history; native text absent, same-action original receipt, stale/terminal reject")
}

func TestTaskConsoleAppBlockedSleepLineageAndRecovery(t *testing.T) {
	w, s := taskConsoleApp(t)
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	ctx := context.Background()
	d, p := sleepTask(t, s, " !sleep=800ms !text=long_original_execution_stream !error=original_execution_failed")
	n := d.Nodes[0]
	sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	question, e := s.a.API.Inbox.Send(ctx, p, "默认项目/Manager", "Manager-only question", "queue", inbox.QuestionInput{Options: []string{"yes", "no"}, NodeRunID: n.ID})
	if e != nil {
		t.Fatal(e)
	}
	blocked := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if blocked.Nodes[0].Question == nil || blocked.Nodes[0].Question.ID != question.ID {
		t.Fatal("original question absent")
	}
	cap := decode[taskmodel.TaskNodeMessageCapability](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/message-capability", nil))
	if !cap.CanSend {
		t.Fatal("actual executed failure/original session not eligible", cap)
	}
	receipt := taskConsoleSend(t, s, d.Task.ID, n, uuid.New(), " !sleep=800ms !text=first_strict_conversation", "queue")
	firstRun := taskConsoleWaitRun(t, s, receipt.RunID, "running")
	if firstRun.ExecutionMode != "task_conversation" || firstRun.NodeRunID != n.ID {
		t.Fatal("blocked message mode/node changed")
	}
	reason := " !sleep=800ms !text=strict_timer_continuation"
	first, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: firstRun.ID, Seconds: 120, Reason: &reason}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	sleeping := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
	if sleeping.Task.Status != "blocked" || sleeping.Nodes[0].Attempt != 1 || sleeping.SleepingInstances[0].NodeRunID == nil || *sleeping.SleepingInstances[0].NodeRunID != n.ID {
		t.Fatal("strict conversation sleep lost original blocked node")
	}
	wakeAt := *sleeping.SleepingInstances[0].WakeAt
	s.stop()
	s = nil
	s = w.start(nil)
	restored := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if len(restored.SleepingInstances) != 1 || !restored.SleepingInstances[0].WakeAt.Equal(wakeAt) || restored.Task.Status != "blocked" {
		t.Fatal("restart changed original sleep deadline/state")
	}
	// TEST-only advancement of the already persisted deadline, not a new source.
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, first.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	wake := sleepWaitTurn(t, s, p.RuntimeID, firstRun.ID)
	if wake.Origin != "sleep_wake" || wake.NodeRunID != n.ID || wake.ExecutionMode != "task_conversation" {
		t.Fatal("deadline continuation lost strict lineage")
	}
	second, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: wake.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.SleepingInstances) == 1 && d.SleepingInstances[0].ID == second.Sleep.ID
	})
	work := taskConsoleSend(t, s, d.Task.ID, n, uuid.New(), " !sleep=150ms !text=queue_wakes_second_sleep", "")
	taskConsoleWaitRun(t, s, work.RunID, "completed")
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, second.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	view := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if view.Task.Status != "blocked" || view.Nodes[0].Attempt != 1 || view.Nodes[0].Question == nil || view.Nodes[0].Question.Status != "open" || view.Nodes[0].Question.CanAnswer {
		t.Fatal("chat/sleep changed blocked/attempt/Manager question permission")
	}
	var timers int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='sleep_wake'`, d.Task.ID).Scan(&timers); e != nil || timers != 1 {
		t.Fatal("second sleep old timer revived", e)
	}
	page := decode[taskmodel.MessagePage](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/messages?limit=100", nil))
	for _, run := range []uuid.UUID{receipt.RunID, wake.ID, work.RunID} {
		user, assistant := false, false
		for _, m := range page.Messages {
			if m.RunID == run {
				if m.NodeRunID != n.ID {
					t.Fatal("reply belongs to wrong node")
				}
				user = user || m.Role == "user"
				assistant = assistant || m.Role == "assistant"
			}
		}
		if !user || !assistant {
			t.Fatal("original node lacks paired user/reply", run)
		}
	}
	failedChat := taskConsoleSend(t, s, d.Task.ID, n, uuid.New(), " !text=conversation_failure !error=chat_execution_failed", "queue")
	for until := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		turn, err := s.a.Store.TaskTurn(ctx, failedChat.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Status == "failed" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("conversation failure did not settle its own run")
		}
	}
	failedView := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if failedView.Task.Status != "blocked" || len(failedView.Nodes) != 1 || failedView.Nodes[0].Attempt != 1 || failedView.Nodes[0].Question == nil || failedView.Nodes[0].Question.Status != "open" {
		t.Fatal("failed conversation settled workflow or original question")
	}
	thirdMessage := taskConsoleSend(t, s, d.Task.ID, n, uuid.New(), " !sleep=500ms !text=strict_sleep_before_explicit_retry", "queue")
	thirdRun := taskConsoleWaitRun(t, s, thirdMessage.RunID, "running")
	third, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: thirdRun.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.SleepingInstances) == 1 && d.SleepingInstances[0].ID == third.Sleep.ID
	})
	s.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/retry", map[string]any{"actionId": uuid.New(), "expectedNodeRunId": n.ID})
	var oldSleepState string
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, third.Sleep.ID).Scan(&oldSleepState); e != nil || oldSleepState != "cancelled" {
		t.Fatal("retry kept old-node strict sleep live", e)
	}
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, third.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='sleep_wake'`, d.Task.ID).Scan(&timers); e != nil || timers != 1 {
		t.Fatal("retry stale timer woke new attempt", e)
	}
	status, b := s.call("POST", taskConsoleRoute(d.Task.ID, n.ID)+"/messages", taskConsoleInput(n, uuid.New(), "old attempt", "queue"))
	if status != 409 {
		t.Fatalf("old attempt accepted: %d %s", status, b)
	}
	if _, e = s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: work.RunID, Seconds: 120}, 1000); e == nil {
		t.Fatal("expired run requested new sleep")
	}
	s.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
	t.Log(fmt.Sprintf("actual strict blocked conversation + original Manager question, first/second sleep, original wakeAt App restart, deadline/queue wake, paired original-node replies, retry/old-run rejection; timerCount=%d", timers))
}

func TestTaskConsoleAppPreciseInterruptAndNoImplicitEligibility(t *testing.T) {
	_, s := taskConsoleApp(t)
	defer s.stop()
	ctx := context.Background()
	d, p := sleepTask(t, s, " !sleep=1s !text=original_running_round")
	n := d.Nodes[0]
	original := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	action := uuid.New()
	r := taskConsoleSend(t, s, d.Task.ID, n, action, " !sleep=500ms !text=precise_interrupt_followup", "interrupt")
	follow := taskConsoleWaitRun(t, s, r.RunID, "running")
	if follow.ExecutionMode != "task_node" {
		t.Fatal("normal interrupt silently changed execution mode")
	}
	// Both direct and Redis delayed notifications explicitly name the OLD run.
	if e := s.a.Coord.PublishInterrupt(ctx, p.RuntimeID.String(), original.ID.String()); e != nil {
		t.Fatal(e)
	}
	if e := s.a.Executor.Interrupt(ctx, p.RuntimeID, original.ID); e != nil {
		t.Fatal(e)
	}
	taskConsoleWaitRun(t, s, r.RunID, "completed")
	old, e := s.a.Store.TaskTurn(ctx, original.ID)
	if e != nil || old.Status != "interrupted" {
		t.Fatal("original active run was not interrupted", e)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	cap := decode[taskmodel.TaskNodeMessageCapability](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/message-capability", nil))
	if cap.CanSend {
		t.Fatal("completed without submit incorrectly granted failure eligibility")
	}
	replay := taskConsoleSend(t, s, d.Task.ID, n, action, " !sleep=500ms !text=precise_interrupt_followup", "interrupt")
	if replay.RunID != r.RunID {
		t.Fatal("interrupt replay created new run")
	}
	inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	if count := sleepNativePrompts(t, *inst.SessionFile); count != 2 {
		t.Fatalf("interrupt/replay native prompt count=%d want2", count)
	}
	t.Log("actual exact-run interrupt stopped original, delayed old Redis/direct interrupts left new run completed; no-output block has no eligibility; replay native calls unchanged")
}

func TestTaskConsoleAppQueuedRecoveryAfterRedisLoss(t *testing.T) {
	w, s := taskConsoleApp(t)
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	ctx := context.Background()
	d, p := sleepTask(t, s, " !text=original_error_stream !error=original_execution_failed")
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	n := d.Nodes[0]
	if e := s.a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	action := uuid.New()
	r := taskConsoleSend(t, s, d.Task.ID, n, action, " !sleep=200ms !text=durable_queued_original_node", "queue")
	queued, e := s.a.Store.TaskTurn(ctx, r.RunID)
	if e != nil || queued.Status != "queued" {
		t.Fatal("test did not preserve pre-dispatch queue", e)
	}
	s.stop()
	s = nil
	options, e := redis.ParseURL(w.cfg.RedisURL)
	if e != nil {
		t.Fatal("invalid TEST Redis configuration")
	}
	client := redis.NewClient(options)
	if e = redistest.DeletePrefix(ctx, client, w.cfg.RedisKeyPrefix); e != nil {
		client.Close()
		t.Fatal(e)
	}
	client.Close()
	s = w.start(nil)
	completed := taskConsoleWaitRun(t, s, r.RunID, "completed")
	if completed.NodeRunID != n.ID || completed.ExecutionMode != "task_conversation" {
		t.Fatal("queue restart rebound node or mode")
	}
	view := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if view.Task.Status != "blocked" || view.Nodes[0].Attempt != 1 {
		t.Fatal("queue recovery resumed workflow")
	}
	replay := taskConsoleSend(t, s, d.Task.ID, n, action, " !sleep=200ms !text=durable_queued_original_node", "queue")
	if replay.RunID != r.RunID {
		t.Fatal("queue recovery lost stable receipt")
	}
	inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	if count := sleepNativePrompts(t, *inst.SessionFile); count != 2 {
		t.Fatalf("recovered queue native prompt count=%d want2", count)
	}
	t.Log("actual queued PG receipt/lineage survived temporary App restart+own Redis prefix loss, original blocked node/session, exactly one new native prompt")
}

func TestTaskConsoleAppTerminalSleepAndQueuedInvalidation(t *testing.T) {
	for _, operation := range []string{"retry", "cancel", "cancel_sleep"} {
		t.Run(operation, func(t *testing.T) {
			endpoint := operation
			if endpoint == "cancel_sleep" {
				endpoint = "cancel"
			}
			w, s := taskConsoleApp(t)
			defer func() { s.stop() }()
			ctx := context.Background()
			d, p := sleepTask(t, s, " !text=original_failure !error=execution_failed")
			taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
			n := d.Nodes[0]
			var sleepID uuid.UUID
			if operation == "cancel_sleep" {
				conversation := taskConsoleSend(t, s, d.Task.ID, n, uuid.New(), " !sleep=500ms !text=original_node_before_terminal_sleep", "queue")
				taskConsoleWaitRun(t, s, conversation.RunID, "running")
				receipt, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: conversation.RunID, Seconds: 120}, 1000)
				if e != nil {
					t.Fatal(e)
				}
				sleepID = receipt.Sleep.ID
				taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
			}
			// Pause only this App's driver to deterministically exercise the
			// accepted-before-change, not-yet-claimed window.
			if e := s.a.Tasks.Stop(ctx); e != nil {
				t.Fatal(e)
			}
			action := uuid.New()
			text := "MUST_NOT_DISPATCH_AFTER_" + operation + "_" + uuid.NewString()
			// In the terminal case preserve the active sleep until cancellation;
			// accepting another message would itself wake it.
			var queued taskmodel.TaskNodeMessageReceipt
			if sleepID == uuid.Nil {
				queued = taskConsoleSend(t, s, d.Task.ID, n, action, text, "queue")
			}
			body := map[string]any{"actionId": uuid.New()}
			if operation == "retry" {
				body["expectedNodeRunId"] = n.ID
			}
			s.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/"+endpoint, body)
			if sleepID != uuid.Nil {
				var state string
				if e := s.a.Store.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, sleepID).Scan(&state); e != nil || state != "cancelled" {
					t.Fatal("terminal kept strict sleep active", state, e)
				}
				if _, e := s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, sleepID); e != nil {
					t.Fatal(e)
				}
				if e := s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
					t.Fatal(e)
				}
				var timers int
				if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='sleep_wake'`, d.Task.ID).Scan(&timers); e != nil || timers != 0 {
					t.Fatal("terminal late timer revived", timers, e)
				}
			}
			if sleepID == uuid.Nil {
				old, e := s.a.Store.TaskTurn(ctx, queued.RunID)
				if e != nil || old.Status != "interrupted" || old.ErrorCode == nil || *old.ErrorCode != "task_conflict" || old.NodeRunID != n.ID {
					t.Fatal("retry lost safe original nonexecution history", e)
				}
				replay := taskConsoleSend(t, s, d.Task.ID, n, action, text, "queue")
				if replay.RunID != queued.RunID {
					t.Fatal("retry rebound accepted receipt")
				}
			}
			s.stop()
			s = w.start(nil)
			if operation == "cancel" {
				old, e := s.a.Store.TaskTurn(ctx, queued.RunID)
				if e != nil || old.Status != "interrupted" {
					t.Fatal("cancelled queued message revived after restart", e)
				}
			}
			if operation == "retry" {
				taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return len(d.Nodes) == 2 && d.Task.Status == "blocked" })
				inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
				if e != nil || inst.SessionFile == nil {
					t.Fatal(e)
				}
				native, e := os.ReadFile(*inst.SessionFile)
				if e != nil || strings.Contains(string(native), text) {
					t.Fatal("accepted old message dispatched in new attempt", e)
				}
				s.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
			}
		})
	}
}

func TestTaskConsoleAppPreparationFailureStaysUnavailable(t *testing.T) {
	w, s := taskConsoleApp(t)
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	ctx := context.Background()
	if e := s.a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	d, p := sleepTask(t, s, " !text=must_not_execute")
	// Explicit new TEST task corruption before preparation; no old task touched.
	if _, e := s.a.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, p.RuntimeID, []byte(`invalid-test-snapshot`)); e != nil {
		t.Fatal(e)
	}
	s.stop()
	s = nil
	s = w.start(nil)
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	n := d.Nodes[0]
	cap := decode[taskmodel.TaskNodeMessageCapability](t, s.must(200, "GET", taskConsoleRoute(d.Task.ID, n.ID)+"/message-capability", nil))
	if cap.CanSend || cap.Reason == nil {
		t.Fatal("preparation failure enabled chat")
	}
	status, _ := s.call("POST", taskConsoleRoute(d.Task.ID, n.ID)+"/messages", taskConsoleInput(n, uuid.New(), "do not run", "queue"))
	if status != 409 {
		t.Fatal("preparation failure accepted send")
	}
	var markers, consoleTurns int
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_console_chat_eligibility WHERE task_id=$1`, d.Task.ID).Scan(&markers); e != nil {
		t.Fatal(e)
	}
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, d.Task.ID).Scan(&consoleTurns); e != nil {
		t.Fatal(e)
	}
	if markers != 0 || consoleTurns != 0 {
		t.Fatal("preparation failure minted cause or send side effect")
	}
	t.Log("actual pre-dispatch preparation failure stayed blocked/unavailable, no cause marker/console turn; no text-reason inference")
}
