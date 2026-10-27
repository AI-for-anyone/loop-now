package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func inboxReceipt(t *testing.T, raw []byte) taskmodel.TaskNodeInboxReceipt {
	t.Helper()
	var out taskmodel.TaskNodeInboxReceipt
	if e := json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func receiptMessage(t *testing.T, r taskmodel.TaskNodeInboxReceipt) uuid.UUID {
	t.Helper()
	var m struct {
		ID uuid.UUID `json:"id"`
	}
	if e := json.Unmarshal(r.Message, &m); e != nil {
		t.Fatal(e)
	}
	return m.ID
}

func TestTaskNodeInboxAtomicBatchReadReply(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	var originalSnapshot []byte
	if e := s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, initial.RuntimeID).Scan(&originalSnapshot); e != nil {
		t.Fatal(e)
	}
	input := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: initial.RuntimeID, Text: "real Inbox payload", WhenBusy: "queue"}
	// Concurrent same action, one actual Inbox row and one queued strict reminder.
	var wg sync.WaitGroup
	results := make(chan []byte, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, input, 65536)
			if e != nil {
				errs <- e
			} else {
				results <- raw
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var first []byte
	for raw := range results {
		if first == nil {
			first = raw
		} else {
			var a, b any
			json.Unmarshal(first, &a)
			json.Unmarshal(raw, &b)
			if string(mustJSON(a)) != string(mustJSON(b)) {
				t.Fatal("different replay")
			}
		}
	}
	rec := inboxReceipt(t, first)
	mid := receiptMessage(t, rec)
	var count int
	if e := s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate inbox", count, e)
	}
	input.ActionID = uuid.New()
	input.Text = "second message same batch"
	raw, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, input, 65536)
	if e != nil {
		t.Fatal(e)
	}
	mid2 := receiptMessage(t, inboxReceipt(t, raw))
	if e = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE inbox_scope_node_id=$1`, node).Scan(&count); e != nil || count != 1 {
		t.Fatal("reminders not merged", count, e)
	}
	page, e := svc.NodeInboxMessages(ctx, auth.Console(), task, node, "", 50)
	if e != nil || len(page.Items) != 2 || len(page.Items[0].RunLinks) != 0 {
		t.Fatal("notification treated as processing", e)
	}
	// A pre-existing workflow run reads before the reminder is claimed.
	active, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok || active.ID != initial.ID {
		t.Fatal("initial claim", e)
	}
	if e = s.MarkTaskRunning(ctx, active, 1); e != nil {
		t.Fatal(e)
	}
	party := store.Party{Kind: "agent", ID: agent, RuntimeID: initial.RuntimeID}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, mid, uuid.Nil); !apperr.Is(e, "validation_failed") {
		t.Fatal("missing explicit run accepted", e)
	}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, mid, uuid.New()); e == nil {
		t.Fatal("unknown run accepted")
	}
	m, handled, changed, e := s.ReadTaskNodeInbox(ctx, party, mid, active.ID)
	if e != nil || !handled || !changed || m.ReadAt == nil {
		t.Fatal("real read", e)
	}
	at := *m.ReadAt
	m, _, changed, e = s.ReadTaskNodeInbox(ctx, party, mid, active.ID)
	if e != nil || changed || !m.ReadAt.Equal(at) {
		t.Fatal("reread moved receipt", e)
	}
	reply, handled, e := s.ReplyTaskNodeInbox(ctx, party, mid, active.ID, "explicit Inbox reply", "")
	if e != nil || !handled || reply.InReplyTo == nil || *reply.InReplyTo != mid || reply.ThreadID != mid || reply.To.Kind != "console" {
		t.Fatal("reply binding", e)
	}
	page, e = svc.NodeInboxMessages(ctx, auth.Console(), task, node, "", 1)
	if e != nil || page.NextCursor == nil || len(page.Items) != 1 {
		t.Fatal("page", e)
	}
	rest, e := svc.NodeInboxMessages(ctx, auth.Console(), task, node, *page.NextCursor, 50)
	if e != nil || len(rest.Items) != 2 {
		t.Fatal("next page", e)
	}
	// Neither the inbox nor read/reply action auto-completes a workflow node.
	if _, e = s.UpdateTaskTurn(ctx, task, active.ID, "completed", "working output", nil, nil); e != nil {
		t.Fatal(e)
	}
	reminder, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok || reminder.ID == initial.ID {
		t.Fatal("strict batch claim", e)
	}
	var links int
	if e = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_node_inbox_run_links WHERE run_id=$1 AND role='notification'`, reminder.ID).Scan(&links); e != nil || links != 2 {
		t.Fatal("batch links", links, e)
	}
	untouched, e := s.GetInboxMessage(ctx, mid2, 160)
	if e != nil || untouched.ReadAt != nil {
		t.Fatal("notification marked message read", e)
	}
	r, e := s.GetTask(ctx, task)
	if e != nil || r.Current().ID != node || r.Current().Attempt != 1 {
		t.Fatal("implicit workflow change", e)
	}
	var retainedSnapshot []byte
	if e = s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, initial.RuntimeID).Scan(&retainedSnapshot); e != nil || !bytes.Equal(originalSnapshot, retainedSnapshot) {
		t.Fatal("Inbox actions changed captured snapshot bytes", e)
	}
	t.Log("actual TEST: 8 concurrent action replays, 2 Inbox/1 notification batch, existing-work-run read/reply, independent unread and pagination")
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestTaskNodeInboxTargetChangeAndOrdinaryIsolation(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: initial.RuntimeID, Text: "never retarget", WhenBusy: "queue"}
	first, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil {
		t.Fatal(e)
	}
	mid := receiptMessage(t, inboxReceipt(t, first))
	// A separate ordinary reminder can coexist at the same priority/runtime.
	_, e = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		_, e := s.EnqueueTaskTurn(ctx, m, "ordinary Inbox reminder", "inbox_reminder", 1, nil)
		return nil, e
	})
	if e != nil {
		t.Fatal("ordinary/strict index isolation", e)
	}
	_, e = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		if e := s.InterruptTaskTurns(ctx, m, node); e != nil {
			return nil, e
		}
		next := *m.Record.Current()
		next.ID = uuid.New()
		next.Attempt++
		next.RunIDs = []uuid.UUID{}
		m.Record.View.Nodes = append(m.Record.View.Nodes, next)
		m.Record.View.Task.CurrentNodeRunID = &next.ID
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil || string(replay) != string(first) {
		t.Fatal("historical receipt lost", e)
	}
	cached, e := svc.NodeInboxAction(ctx, auth.Console(), task, node, in.ActionID)
	if e != nil || string(cached) != string(first) {
		t.Fatal("unknown outcome GET", e)
	}
	in.ActionID = uuid.New()
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); !apperr.Is(e, "task_conflict") {
		t.Fatal("stale new send", e)
	}
	party := store.Party{Kind: "agent", ID: agent, RuntimeID: initial.RuntimeID}
	list, e := s.ListInboxReceived(ctx, party, store.InboxListQuery{Limit: 50, Preview: 160})
	if e != nil || len(list.Messages) != 0 {
		t.Fatal("new node sees old Inbox", e)
	}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, mid, initial.ID); e == nil {
		t.Fatal("stale read")
	}
	if _, _, e = s.ReplyTaskNodeInbox(ctx, party, mid, initial.ID, "late", ""); e == nil {
		t.Fatal("stale reply")
	}
	if _, _, e = s.TaskNodeInboxThread(ctx, party, mid, initial.ID, false); e == nil {
		t.Fatal("thread bypass")
	}
	page, e := svc.NodeInboxMessages(ctx, auth.Console(), task, node, "", 50)
	if e != nil || len(page.Items) != 1 || page.Items[0].NotExecuted == nil {
		t.Fatal("console historical nonexecution", e)
	}
	var all int
	if e = s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&all); e != nil || all != 1 {
		t.Fatal("retarget side effect", all, e)
	}
	// Even if local prerequisites later become valid, a recorded nonexecution
	// cannot be revived by direct-ID read while summary filtering still hides it.
	_, e = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.CurrentNodeRunID = &node
		m.Record.View.Task.Status = "running"
		m.Record.Current().Status = "running"
		_, e := m.Tx.Exec(ctx, `UPDATE task_turns SET status='running',execution_mode='task_node' WHERE id=$1`, initial.ID)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, mid, initial.ID); e == nil {
		t.Fatal("direct-ID revived recorded nonexecution")
	}
	if _, _, e = s.ReplyTaskNodeInbox(ctx, store.ConsoleParty, mid, uuid.Nil, "revive old thread", ""); e == nil {
		t.Fatal("reply revived recorded nonexecution")
	}
	if e = s.MigrateDownTo(ctx, 18); e == nil {
		t.Fatal("lossy019 Down allowed")
	}
	t.Log("strict/ordinary reminder coexist; original-action GET/replay after attempt switch; Agent list/read/thread/reply all reject; console history and019 Down guard")
}

func TestTaskNodeInboxBlockedConversationMode(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, source := taskConsoleRecord(t, s)
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	active, ok, e := s.ClaimTaskTurn(ctx, task, source.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, active, 1); e != nil {
		t.Fatal(e)
	}
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if e = os.WriteFile(session, []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		if _, e := m.Tx.Exec(ctx, `UPDATE task_turns SET status='failed',error_code='pi_error' WHERE id=$1`, active.ID); e != nil {
			return nil, e
		}
		if _, e := m.Tx.Exec(ctx, `UPDATE task_instances SET session_state='active',session_file=$2 WHERE id=$1`, source.RuntimeID, session); e != nil {
			return nil, e
		}
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: source.RuntimeID, Text: "diagnose original exception"}
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); !apperr.Is(e, "task_conflict") {
		t.Fatal("unknown block opened", e)
	}
	_, e = s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.RecordTaskConsoleEligibilityTx(ctx, m, source.ID)
	})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil {
		t.Fatal(e)
	}
	mid := receiptMessage(t, inboxReceipt(t, raw))
	turn, ok, e := s.ClaimTaskInboxTurn(ctx, task, source.RuntimeID)
	if e != nil || !ok || turn.ExecutionMode != "task_conversation" {
		t.Fatal("blocked claim", e)
	}
	if e = s.MarkTaskRunning(ctx, turn, 1); e != nil {
		t.Fatal(e)
	}
	party := store.Party{Kind: "agent", ID: agent, RuntimeID: source.RuntimeID}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, mid, turn.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.ReplyTaskNodeInbox(ctx, party, mid, turn.ID, "diagnosis", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateTaskTurn(ctx, task, turn.ID, "completed", "work output", nil, nil); e != nil {
		t.Fatal(e)
	}
	r, e := s.GetTask(ctx, task)
	if e != nil || r.Current().Status != "blocked" || r.Current().Attempt != 1 {
		t.Fatal("chat resumed workflow", e)
	}
	t.Log("unknown legacy block rejected; source marker admits original-session conversation; actual read/reply preserve blocked and attempt")
}

func TestTaskNodeInboxSleepLineageAndQueueWake(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: initial.RuntimeID, Text: "strict sleeping input", WhenBusy: "queue"}
	if _, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); e != nil {
		t.Fatal(e)
	}
	if _, e := s.UpdateTaskTurn(ctx, task, initial.ID, "completed", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	turn, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, turn, 1); e != nil {
		t.Fatal(e)
	}
	session := filepath.Join(t.TempDir(), "sleep-session.jsonl")
	if e = os.WriteFile(session, []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool.Exec(ctx, `UPDATE task_instances SET session_state='active',session_file=$2 WHERE id=$1`, initial.RuntimeID, session); e != nil {
		t.Fatal(e)
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: agent, TaskID: task, RuntimeID: initial.RuntimeID}
	finish := func(run uuid.UUID) {
		t.Helper()
		_, e := s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
			if _, e := s.UpdateTaskTurnTx(ctx, m, run, "completed", "", nil, nil); e != nil {
				return nil, e
			}
			_, e := s.FinishSleepTx(ctx, m.Tx, m, run, "completed")
			return nil, e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	first, e := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	finish(turn.ID)
	if _, e = s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	wake, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok || wake.Origin != "sleep_wake" || wake.NodeRunID != node {
		t.Fatal("first wake lineage", e)
	}
	if e = s.MarkTaskRunning(ctx, wake, 1); e != nil {
		t.Fatal(e)
	}
	second, e := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: wake.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal("second sleep lost strict lineage", e)
	}
	finish(wake.ID)
	in.ActionID = uuid.New()
	in.Text = "queue wakes original sleep"
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, second.Sleep.ID).Scan(&state); e != nil || state != "woken" {
		t.Fatal("valid queue failed wake", state, e)
	}
	carrier, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok || carrier.NodeRunID != node || carrier.Origin != "inbox_reminder" {
		t.Fatal("queue wake target", e)
	}
	var strict bool
	if e = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_console_message_lineage WHERE run_id=$1 AND node_run_id=$2 AND execution_mode='task_node')`, carrier.ID, node).Scan(&strict); e != nil || !strict {
		t.Fatal("wake lineage lost", e)
	}
	t.Log("actual TEST Store: first/second strict sleep, persisted deadline advancement, queue wakes original scope; no wall-clock/production timeout changes")
}

type taskInboxInterruptRecorder struct {
	executor.Executor
	calls [][2]uuid.UUID
}

func (x *taskInboxInterruptRecorder) Interrupt(_ context.Context, runtime, run uuid.UUID) error {
	x.calls = append(x.calls, [2]uuid.UUID{runtime, run})
	return nil
}

func TestTaskNodeInboxPreciseInterruptAndReplay(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	calls := &taskInboxInterruptRecorder{}
	svc := tasks.New(s, nil, calls, tasks.Options{}, quietLogger())
	active, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, active, 1); e != nil {
		t.Fatal(e)
	}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: initial.RuntimeID, Text: "queue before interrupt", WhenBusy: "queue"}
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); e != nil {
		t.Fatal(e)
	}
	if len(calls.calls) != 0 {
		t.Fatal("queue interrupted active execution")
	}
	in.ActionID = uuid.New()
	in.ExpectedRuntimeID = uuid.New()
	in.WhenBusy = "interrupt"
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); e == nil {
		t.Fatal("invalid target accepted")
	}
	if len(calls.calls) != 0 {
		t.Fatal("failed request interrupted")
	}
	in.ExpectedRuntimeID = initial.RuntimeID
	raw, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil {
		t.Fatal(e)
	}
	if len(calls.calls) != 1 || calls.calls[0] != [2]uuid.UUID{initial.RuntimeID, active.ID} {
		t.Fatal("interrupt not captured original active run")
	}
	if _, e = s.UpdateTaskTurn(ctx, task, active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	next, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, next, 1); e != nil {
		t.Fatal(e)
	}
	replay, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil || string(replay) != string(raw) || len(calls.calls) != 1 {
		t.Fatal("replay interrupted a newer run", e)
	}
	in.Text = "changed same action"
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); !apperr.Is(e, "task_conflict") {
		t.Fatal("same action changed content", e)
	}
	if len(calls.calls) != 1 {
		t.Fatal("conflict caused interrupt")
	}
	t.Log("actual TEST transaction: queue retains current run; bad target zero interrupt; interrupt captures old run; replay/conflict cannot interrupt newer run")
}

func TestTaskNodeInboxInterruptedNotificationKeepsUnreadMessage(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	if _, e := s.UpdateTaskTurn(ctx, task, initial.ID, "completed", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: initial.RuntimeID, Text: "A remains unread", WhenBusy: "queue"}
	raw, e := svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536)
	if e != nil {
		t.Fatal(e)
	}
	aID := receiptMessage(t, inboxReceipt(t, raw))
	a, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, a, 1); e != nil {
		t.Fatal(e)
	}
	in.ActionID = uuid.New()
	in.Text = "B interrupts notification A"
	in.WhenBusy = "interrupt"
	if _, e = svc.SendNodeInboxMessage(ctx, auth.Console(), task, node, in, 65536); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateTaskTurn(ctx, task, a.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	party := store.Party{Kind: "agent", ID: agent, RuntimeID: initial.RuntimeID}
	page, e := s.ListInboxReceived(ctx, party, store.InboxListQuery{Limit: 50, Preview: 160})
	if e != nil || len(page.Messages) != 2 {
		t.Fatal("interrupted notification hid valid unread message", e)
	}
	b, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.MarkTaskRunning(ctx, b, 1); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = s.ReadTaskNodeInbox(ctx, party, aID, b.ID); e != nil {
		t.Fatal("new actual run cannot read A", e)
	}
	records, e := svc.NodeInboxMessages(ctx, auth.Console(), task, node, "", 50)
	if e != nil || records.Items[0].NotExecuted != nil {
		t.Fatal("false node_changed retained", e)
	}
	found := false
	for _, l := range records.Items[0].RunLinks {
		found = found || l.Role == "read" && l.RunID == b.ID
	}
	if !found {
		t.Fatal("actual B read not linked to A")
	}
	t.Log("notification interruption alone does not expire Inbox; same-node B run reads A with real many-to-many link")
}

func TestTaskNodeInboxMigrationRoundTripWithoutNewData(t *testing.T) {
	ctx := context.Background()
	// 026 cannot be rolled back, so the 019 round trip runs on a 025 schema
	// with task rows written in their own column shape.
	s := taskConsoleStoreAt(t, 25)
	task, runtime := rawTaskAt25(t, s)
	for _, origin := range []string{"task_node", "inbox_reminder"} {
		mustExec(t, s, `INSERT INTO task_turns(id,task_id,runtime_id,user_message_id,assistant_message_id,status,origin,text)VALUES($1,$2,$3,$4,$5,'completed',$6,'old turn')`, uuid.New(), task, runtime, uuid.New(), uuid.New(), origin)
	}
	if e := s.MigrateDownTo(ctx, 18); e != nil {
		t.Fatal("empty-new-data Down", e)
	}
	var count int
	if e := s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1`, runtime).Scan(&count); e != nil || count != 2 {
		t.Fatal("old turns changed", e)
	}
	if e := s.MigrateUpTo(ctx, 25); e != nil {
		t.Fatal("Up again", e)
	}
	if e := s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_node_inbox_messages`).Scan(&count); e != nil || count != 0 {
		t.Fatal("old history converted to Inbox", e)
	}
	t.Log("actual TEST019 Down/Up preserves old ordinary and task turns; no history backfill")
}

// rawTaskAt25 writes one running task with an opaque pre-026 snapshot.
func rawTaskAt25(t *testing.T, s *store.Store) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	d, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.ProjectScope(d.Project.ID), "node-agent", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	task, runtime := uuid.New(), uuid.New()
	mustExec(t, s, `INSERT INTO task_records(id,project_id,status,document,version)VALUES($1,$2,'running','{"view":{"task":{"status":"running","version":1}}}',1)`, task, d.Project.ID)
	mustExec(t, s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot)VALUES($1,$2,$3,$4,$5)`, runtime, task, a.ID, a.Name, []byte(`{"schemaVersion":4,"opaque":"pre-026 sealed snapshot"}`))
	return task, runtime
}
