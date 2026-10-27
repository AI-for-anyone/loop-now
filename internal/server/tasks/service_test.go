package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
)

func fixture(t *testing.T, loop bool) (*Service, uuid.UUID, auth.Principal) {
	t.Helper()
	ctx := context.Background()
	st := pgtest.NewStore(t)
	dc, e := st.EnsureDefaults(ctx, "组织", "项目")
	if e != nil {
		t.Fatal(e)
	}
	a, e := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "执行者", "example", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.SetProjectManager(ctx, dc.Project.ID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	id, runtime := uuid.New(), uuid.New()
	now := time.Now().UTC()
	def := workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "处理", AgentID: a.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "answer", Type: "string", Required: true}}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}
	if loop {
		limit := 1
		def.Edges = []workflow.Edge{{ID: "again", From: "work", To: "work", MaxTraversals: &limit}}
	}
	inst := store.TaskInstance{ID: runtime, TaskID: id, AgentID: a.ID, AgentName: a.Name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}
	node := freshNode(id, def.Steps[0], inst, 1, 1)
	record := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: dc.Project.ID, Title: "任务", Status: "preparing", CreatedAt: now, UpdatedAt: now, Version: 1}, Request: map[string]any{}, Workflow: def, Nodes: []taskmodel.NodeRun{node}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	setCurrent(&record, node)
	_, e = st.CreateTask(ctx, dc.Project.ID, a.ID, store.TaskAction{ActorKey: "fixture", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return record, []store.TaskInstance{inst}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, runtime)
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) { m.Record.View.Task.Status = "running"; return nil, nil })
	if e != nil {
		t.Fatal(e)
	}
	s := New(st, nil, &stubExecutor{}, Options{MaxVisits: 20, ReconcileInterval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, id, auth.Principal{Kind: auth.KindAgent, ID: a.ID, TaskID: id, RuntimeID: runtime}
}
func current(t *testing.T, s *Service, id uuid.UUID) taskmodel.Record {
	t.Helper()
	r, e := s.Store.GetTask(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func submit(t *testing.T, s *Service, id uuid.UUID, p auth.Principal, edge *string) {
	t.Helper()
	r := current(t, s, id)
	_, e := s.Submit(context.Background(), p, id, r.Current().ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"answer": "saved"}, EdgeID: edge})
	if e != nil {
		t.Fatal(e)
	}
}
func turn(t *testing.T, s *Service, id uuid.UUID) store.TaskTurn {
	t.Helper()
	ctx := context.Background()
	r := current(t, s, id)
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		_, e := s.Store.EnqueueTaskTurn(ctx, m, "do work", "task_node", 1, nil)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	v, ok, e := s.Store.ClaimTaskTurn(ctx, id, r.Current().RuntimeID)
	if e != nil || !ok {
		t.Fatal(e, ok)
	}
	return v
}
func TestSubmitWaitsAndAbnormalPreservesResult(t *testing.T) {
	s, id, p := fixture(t, false)
	v := turn(t, s, id)
	submit(t, s, id, p, nil)
	r := current(t, s, id)
	if r.View.Task.Status != "running" || r.Current().Status != "submitted" {
		t.Fatal("submission advanced before round end")
	}
	if e := s.settle(context.Background(), v, executor.Result{Outcome: executor.OutcomeFailed}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.View.Task.Status != "blocked" || r.Current().Output["answer"] != "saved" || len(successful(&r)) != 0 {
		t.Fatal("failed submission became successful")
	}
	old := r.Current().ID
	_, e := s.Retry(context.Background(), auth.Console(), id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: old, Note: "again"})
	if e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.Current().RuntimeID != p.RuntimeID || r.Current().Attempt != 2 || r.Current().Visit != 1 || r.Current().Output != nil {
		t.Fatal("retry lost instance or inherited output")
	}
	if e = s.settle(context.Background(), v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	late := current(t, s, id)
	if late.Current().ID == old {
		t.Fatal("late round revived old attempt")
	}
}
func TestLoopCountsOnlySuccessfulTraversal(t *testing.T) {
	s, id, p := fixture(t, true)
	edge := "again"
	v := turn(t, s, id)
	submit(t, s, id, p, &edge)
	if e := s.settle(context.Background(), v, executor.Result{Outcome: "failed"}); e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.EdgeCounts[edge] != 0 {
		t.Fatal("failed round counted traversal")
	}
	_, e := s.Retry(context.Background(), auth.Console(), id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: r.Current().ID})
	if e != nil {
		t.Fatal(e)
	}
	// Retry interrupted the old active turn; emulate its completed interruption before claiming the new one.
	_, e = s.Store.UpdateTaskTurn(context.Background(), id, v.ID, "interrupted", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	v, ok, e := s.Store.ClaimTaskTurn(context.Background(), id, p.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e, ok)
	}
	submit(t, s, id, p, &edge)
	if e = s.settle(context.Background(), v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.EdgeCounts[edge] != 1 || r.Current().Visit != 2 {
		t.Fatal("successful loop traversal lost")
	}
	_, _ = s.Store.UpdateTaskTurn(context.Background(), id, v.ID, "completed", "", nil, nil)
	v, ok, e = s.Store.ClaimTaskTurn(context.Background(), id, p.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e, ok)
	}
	submit(t, s, id, p, &edge)
	if e = s.settle(context.Background(), v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.View.Task.Status != "blocked" || r.EdgeCounts[edge] != 1 || r.Current().Output == nil {
		t.Fatal("loop cap did not block with saved result")
	}
}
func TestCancelTerminalAndLateSettlement(t *testing.T) {
	s, id, p := fixture(t, false)
	v := turn(t, s, id)
	submit(t, s, id, p, nil)
	in := taskmodel.CancelInput{ActionID: uuid.New()}
	first, e := s.Cancel(context.Background(), auth.Console(), id, in)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Cancel(context.Background(), auth.Console(), id, in)
	if e != nil || string(first) != string(second) {
		t.Fatal("cancel replay unstable", e)
	}
	if e = s.settle(context.Background(), v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.View.Task.Status != "cancelled" {
		t.Fatal("late settlement revived task")
	}
}
func TestRestartBlocksSubmittedNodeWithoutReplay(t *testing.T) {
	s, id, p := fixture(t, false)
	_ = turn(t, s, id)
	submit(t, s, id, p, nil)
	if e := s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	s.Stop(context.Background())
	r := current(t, s, id)
	if r.View.Task.Status != "blocked" || r.Current().Output == nil {
		t.Fatal("restart lost submission or replayed")
	}
	var n int
	if e := s.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE task_id=$1 AND status='interrupted'`, id).Scan(&n); e != nil || n != 1 {
		t.Fatal(e, n)
	}
}
func TestOpenQuestionRequiresPersistentClose(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		block(m.Record, "wait")
		m.Record.Current().Question = &taskmodel.Question{ID: uuid.New(), Status: "open", CanAnswer: true}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	_, e = s.Retry(ctx, auth.Console(), id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: r.Current().ID})
	if e == nil {
		t.Fatal("closed question only in task view without persistence")
	}
	r = current(t, s, id)
	if r.Current().Question.Status != "open" {
		t.Fatal("failed close changed history")
	}
}
func TestCompleteAfterRoundAndIdempotentSettlement(t *testing.T) {
	s, id, p := fixture(t, false)
	v := turn(t, s, id)
	submit(t, s, id, p, nil)
	if e := s.settle(context.Background(), v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.View.Task.Status != "completed" || r.Current().Status != "completed" || r.View.Cleanup != "pending" {
		b, _ := json.Marshal(r)
		t.Fatal(string(b))
	}
	before := r.View.Task.Version
	if e := s.settle(context.Background(), v, executor.Result{Outcome: "failed"}); e != nil {
		t.Fatal(e)
	}
	if current(t, s, id).View.Task.Version != before {
		t.Fatal("duplicate settlement changed terminal task")
	}
}

type stubExecutor struct{}

func (*stubExecutor) Execute(context.Context, executor.Request, executor.Sink) (executor.Result, error) {
	panic("unexpected execution")
}
func (*stubExecutor) Interrupt(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (*stubExecutor) Release(context.Context, uuid.UUID) error              { return nil }
func (*stubExecutor) Process(uuid.UUID) executor.ProcessInfo {
	return executor.ProcessInfo{State: "stopped"}
}
func (*stubExecutor) Shutdown(context.Context) error { return nil }

func TestCleanupArchivesBeforeDeletingAndPreservesOnFailure(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	instances, e := s.Store.TaskInstances(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	inst := instances[0]
	root := t.TempDir()
	s.Source = &Source{Store: s.Store, WorkRoot: root, MaxSessionBytes: 4096, FreezeNormal: func(context.Context, uuid.UUID) error { return nil }}
	dir := filepath.Join(root, "agents", inst.ID.String(), "sessions")
	if e = os.MkdirAll(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "session.jsonl")
	if e = os.WriteFile(file, []byte("broken"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.RecordTaskSession(ctx, inst.ID, file); e != nil {
		t.Fatal(e)
	}
	_, e = s.Cancel(ctx, auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cleanup(ctx, id); e == nil {
		t.Fatal("broken archive accepted")
	}
	if _, e = os.Stat(dir); e != nil {
		t.Fatal("failed archive removed only copy", e)
	}
	r := current(t, s, id)
	if r.View.Cleanup != "failed" {
		t.Fatal("archive failure not visible")
	}
	data := []byte("{\"type\":\"session\",\"id\":\"fixture\"}\n{\"type\":\"message\"}\n")
	if e = os.WriteFile(file, data, 0o600); e != nil {
		t.Fatal(e)
	}
	if e = s.cleanup(ctx, id); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("successful cleanup retained directory", e)
	}
	var archived []byte
	if e = s.Store.Pool.QueryRow(ctx, `SELECT b.data FROM task_archives a JOIN blobs b ON b.sha256=a.blob_sha256 WHERE a.runtime_id=$1`, inst.ID).Scan(&archived); e != nil {
		t.Fatal(e)
	}
	var payload struct{ Session []byte }
	if e = json.Unmarshal(archived, &payload); e != nil || !bytes.Equal(payload.Session, data) {
		t.Fatal("archive lost full session", e)
	}
	if r = current(t, s, id); r.View.Cleanup != "archived" {
		t.Fatal("cleanup not marked archived")
	}
}

func TestQuestionOneOpenWaitingAndLateAnswerDoesNotWakeNewAttempt(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	v := turn(t, s, id)
	r := current(t, s, id)
	nodeID := r.Current().ID
	messageID := uuid.New()
	questionID := uuid.New()
	created := 0
	create := func(tx pgx.Tx) (taskmodel.Question, error) {
		created++
		msg := store.NewInboxMessage{ID: messageID, From: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, To: store.ConsoleParty, Text: "请选择"}
		e := store.InsertInboxMessage(ctx, tx, msg)
		return taskmodel.Question{ID: questionID, ThreadID: messageID, MessageID: messageID, Text: "请选择", Status: "open", CanAnswer: true}, e
	}
	if _, e := s.OpenQuestion(ctx, p, nodeID, create); e != nil {
		t.Fatal(e)
	}
	if _, e := s.OpenQuestion(ctx, p, nodeID, create); e == nil || created != 1 {
		t.Fatal("second open question was created")
	}
	if e := s.settle(ctx, v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.View.Task.Status != "waiting" || r.Current().Question.Status != "open" {
		t.Fatal("question did not wait")
	}
	if e := s.DeliverInbox(ctx, store.NewInboxMessage{ID: uuid.New(), From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, Text: "ordinary message", WhenBusy: "queue"}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.Current().Question.Status != "open" {
		t.Fatal("ordinary message answered question")
	}
	if e := s.blockTask(ctx, id, "retry requested"); e != nil {
		t.Fatal(e)
	}
	closed := 0
	s.Source = &Source{CloseQuestion: func(context.Context, pgx.Tx, uuid.UUID, string) (string, error) { closed++; return "closed", nil }}
	_, e := s.Retry(ctx, auth.Console(), id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: nodeID})
	if e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	newNode := r.Current().ID
	if newNode == nodeID || closed != 1 {
		t.Fatal("retry failed to close old question")
	}
	before := r.View.Task.Version
	if e = s.QuestionChanged(ctx, id, questionID, "answered"); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.Current().ID != newNode || r.View.Task.Version != before || r.Current().Question != nil {
		t.Fatal("late old question affected new attempt")
	}
}

func TestManagerHandoffAndStaleRetry(t *testing.T) {
	s, id, old := fixture(t, false)
	ctx := context.Background()
	r := current(t, s, id)
	a, e := s.Store.CreateAgent(ctx, store.ProjectScope(r.View.Task.ProjectID), "新Manager", "example", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.SetProjectManager(ctx, r.View.Task.ProjectID, a.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.blockTask(ctx, id, "blocked"); e != nil {
		t.Fatal(e)
	}
	nodeID := r.Current().ID
	if _, e = s.Retry(ctx, old, id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: nodeID}); !apperr.Is(e, apperr.CodeForbidden) {
		t.Fatal("old Manager retained retry permission", e)
	}
	next := auth.Principal{Kind: auth.KindAgent, ID: a.ID}
	if _, e = s.Retry(ctx, next, id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: nodeID}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Retry(ctx, next, id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: nodeID}); !apperr.Is(e, apperr.CodeTaskConflict) {
		t.Fatal("stale retry accepted", e)
	}
}
func TestSubmitReplayAfterAdvanceAndConcurrentCancel(t *testing.T) {
	s, id, p := fixture(t, true)
	ctx := context.Background()
	v := turn(t, s, id)
	r := current(t, s, id)
	oldNode := r.Current().ID
	edge := "again"
	in := taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"answer": "saved"}, EdgeID: &edge}
	first, e := s.Submit(ctx, p, id, oldNode, in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.settle(ctx, v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	replay, e := s.Submit(ctx, p, id, oldNode, in)
	if e != nil || string(first) != string(replay) {
		t.Fatal("successful action cannot replay after advancing", e)
	}
	r = current(t, s, id)
	if _, e = s.Submit(ctx, p, id, r.Current().ID, in); !apperr.Is(e, apperr.CodeTaskConflict) {
		t.Fatal("action ID reused on different node", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Go(func() {
		_, e := s.Submit(ctx, p, id, r.Current().ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"answer": "concurrent"}, EdgeID: &edge})
		results <- e
	})
	wg.Go(func() {
		_, e := s.Cancel(ctx, auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()})
		results <- e
	})
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil && !apperr.Is(e, apperr.CodeTaskConflict) {
			t.Fatal(e)
		}
	}
	r = current(t, s, id)
	if r.View.Task.Status != "cancelled" {
		t.Fatal("concurrent submit revived cancellation")
	}
}

func TestQuestionResolutionBeforeRoundEndKeepsWakeup(t *testing.T) {
	for _, status := range []string{"answered", "expired", "closed"} {
		t.Run(status, func(t *testing.T) {
			s, id, p := fixture(t, false)
			ctx := context.Background()
			v := turn(t, s, id)
			r := current(t, s, id)
			nodeID := r.Current().ID
			questionID, messageID := uuid.New(), uuid.New()
			_, e := s.OpenQuestion(ctx, p, nodeID, func(tx pgx.Tx) (taskmodel.Question, error) {
				e := store.InsertInboxMessage(ctx, tx, store.NewInboxMessage{ID: messageID, From: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, To: store.ConsoleParty, Text: "choose"})
				return taskmodel.Question{ID: questionID, ThreadID: messageID, MessageID: messageID, Status: "open", CanAnswer: true}, e
			})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.QuestionChanged(ctx, id, questionID, status); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Store.UpdateTaskTurn(ctx, id, v.ID, "completed", "asked", nil, nil); e != nil {
				t.Fatal(e)
			}
			if e = s.settle(ctx, v, executor.Result{Outcome: "completed"}); e != nil {
				t.Fatal(e)
			}
			r = current(t, s, id)
			if r.View.Task.Status == "blocked" {
				t.Fatal("early answer lost wakeup")
			}
			wake, ok, e := s.Store.ClaimTaskTurn(ctx, id, p.RuntimeID)
			if e != nil || !ok || wake.Origin != "inbox_reminder" {
				t.Fatal("answer notification not available", e, ok)
			}
			submit(t, s, id, p, nil)
			if e = s.settle(ctx, wake, executor.Result{Outcome: "completed"}); e != nil {
				t.Fatal(e)
			}
			r = current(t, s, id)
			if r.View.Task.Status != "completed" {
				t.Fatal("answered node could not complete")
			}
		})
	}
}

func TestQuestionAnswerPermissionDependsOnViewer(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.Current().Question = &taskmodel.Question{ID: uuid.New(), ThreadID: uuid.New(), MessageID: uuid.New(), Status: "open", CanAnswer: true}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	unknown, e := s.Get(ctx, auth.Console(), id)
	if e != nil || unknown.Nodes[0].Question.CanAnswer {
		t.Fatal("unconfigured question adapter granted answer permission", e)
	}
	s.Source = &Source{ViewQuestion: func(_ context.Context, viewer auth.Principal, q taskmodel.Question) (taskmodel.Question, error) {
		q.CanAnswer = viewer.Kind == auth.KindConsole
		return q, nil
	}}
	human, e := s.Get(ctx, auth.Console(), id)
	if e != nil || !human.Nodes[0].Question.CanAnswer {
		t.Fatal("console answer permission missing", e)
	}
	agent, e := s.Get(ctx, p, id)
	if e != nil || agent.Nodes[0].Question.CanAnswer {
		t.Fatal("console permission leaked to Agent", e)
	}
	s.Source.ViewQuestion = func(_ context.Context, _ auth.Principal, q taskmodel.Question) (taskmodel.Question, error) {
		q.MessageID = uuid.New()
		return q, nil
	}
	if _, e = s.Get(ctx, auth.Console(), id); e == nil {
		t.Fatal("question adapter replaced original target")
	}
}

func TestNodeMessagesPaginateAndDoNotMixSharedInstanceVisits(t *testing.T) {
	s, id, p := fixture(t, true)
	ctx := context.Background()
	v := turn(t, s, id)
	r := current(t, s, id)
	firstNode := r.Current().ID
	edge := "again"
	submit(t, s, id, p, &edge)
	if _, e := s.Store.UpdateTaskTurn(ctx, id, v.ID, "completed", "first visit", nil, nil); e != nil {
		t.Fatal(e)
	}
	if e := s.settle(ctx, v, executor.Result{Outcome: "completed"}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	secondNode := r.Current().ID
	second, ok, e := s.Store.ClaimTaskTurn(ctx, id, p.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e, ok)
	}
	if _, e = s.Store.UpdateTaskTurn(ctx, id, second.ID, "running", "second visit", nil, nil); e != nil {
		t.Fatal(e)
	}
	page, e := s.Store.TaskNodeMessages(ctx, id, firstNode, "", 1)
	if e != nil || len(page.Messages) != 1 || page.Messages[0].Role != "user" || page.NextCursor == nil {
		t.Fatal("first node first page invalid", e)
	}
	next, e := s.Store.TaskNodeMessages(ctx, id, firstNode, *page.NextCursor, 1)
	if e != nil || len(next.Messages) != 1 || next.Messages[0].Text != "first visit" || next.NextCursor != nil {
		t.Fatal("first node second page invalid", e)
	}
	other, e := s.Store.TaskNodeMessages(ctx, id, secondNode, "", 100)
	if e != nil || len(other.Messages) != 2 {
		t.Fatal("second node messages invalid", e)
	}
	for _, message := range other.Messages {
		if message.NodeRunID != secondNode || message.RunID == v.ID {
			t.Fatal("visits mixed shared session history")
		}
	}
}

func TestMissingRequiredInputBlocksBeforeDispatch(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		step := &m.Record.View.Workflow.Steps[0]
		step.InputFields = []workflow.Field{{Name: "required", Type: "string", Required: true}}
		step.Inputs = map[string]workflow.Source{"required": {Kind: "request", Field: "missing"}}
		return nil, s.prepareNode(ctx, m, "")
	})
	if e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.View.Task.Status != "blocked" || r.Current().Reason == nil {
		t.Fatal("missing input not blocked")
	}
	var count int
	if e = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, id).Scan(&count); e != nil || count != 0 {
		t.Fatal("missing input dispatched a turn", e, count)
	}
}

func TestRetryPreservesQuestionAnsweredDuringClose(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		block(m.Record, "blocked")
		m.Record.Current().Question = &taskmodel.Question{ID: uuid.New(), ThreadID: uuid.New(), MessageID: uuid.New(), Status: "open"}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	old := r.Current().ID
	s.Source = &Source{CloseQuestion: func(context.Context, pgx.Tx, uuid.UUID, string) (string, error) { return "answered", nil }}
	if _, e = s.Retry(ctx, auth.Console(), id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: old}); e != nil {
		t.Fatal(e)
	}
	r = current(t, s, id)
	if r.View.Nodes[0].Question.Status != "answered" || r.Current().ID == old {
		t.Fatal("retry overwrote answered history")
	}
}
