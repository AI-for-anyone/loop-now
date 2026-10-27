package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInboxQuestionRealHTTPStatesAndContract(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.platformAgent("提问方")
	sender := e.agentCaller(a.ID)
	create := func(text string) apitypes.InboxMessage {
		return decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": text, "options": []string{"继续", "停止"}}))
	}
	q := create("first")
	if q.Question == nil || q.Question.State != "open" || q.Question.Options[0].ID != "1" {
		t.Fatal("question absent")
	}
	path := "/inbox/messages/" + q.ID.String() + "/reply"
	sender.expectError(422, "validation_failed", "POST", path, map[string]any{"optionId": "1"})
	// A later ordinary message in the thread is not the answer target.
	ordinary := decode[apitypes.InboxMessage](t, sender.expect(201, "POST", path, map[string]any{"text": "补充说明"}))
	e.expect(201, "POST", "/inbox/messages/"+ordinary.ID.String()+"/reply", map[string]any{"text": "普通消息"})
	original, err := e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "open" || original.ReadAt != nil {
		t.Fatal("ordinary reply answered or read original", err)
	}
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", path, map[string]any{"optionId": "2"}))
	if answer.Text != "选择：停止" || answer.AnswerTo == nil || answer.AnswerTo.MessageID != q.ID {
		t.Fatal("answer projection")
	}
	if original, err = e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || original.ReadAt == nil {
		t.Fatal("console answer left the question unread", err)
	}
	e.console().expectError(409, "already_answered", "POST", path, map[string]any{"optionId": "1"})
	e.expect(201, "POST", path, map[string]any{"text": "已回答后的普通文字"})
	expired := create("expires")
	if _, err = e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	e.console().expectError(409, "question_expired", "POST", "/inbox/messages/"+expired.ID.String()+"/reply", map[string]any{"optionId": "1"})
	closed := create("closed")
	if err = e.st.InboxTx(ctx, func(tx pgx.Tx) error {
		state, err := store.CloseInboxQuestion(ctx, tx, closed.ID, "task_cancel")
		if err == nil && state != "closed" {
			t.Fatal("wrong close state")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.console().expectError(409, "question_closed", "POST", "/inbox/messages/"+closed.ID.String()+"/reply", map[string]any{"optionId": "1"})
	e.expect(201, "POST", "/inbox/messages/"+closed.ID.String()+"/reply", map[string]any{"text": "普通回复保留"})
	for _, values := range [][]string{{}, {"same", "same"}, {" "}} {
		sender.expect(422, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "bad", "options": values}, "X-Contract-Invalid-Request", "1")
	}
	sender.expect(422, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "bad", "options": nil}, "X-Contract-Invalid-Request", "1")
	// No callback/provider replacement: real platform notice is persisted once.
	if err = e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, expired.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("expiry notification duplicate/missing", count, err)
	}
}

func TestTaskQuestionRealStorageCloseRollbackAndLateOptions(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
	node := d.Nodes[0]
	instance := e.agentCaller(node.RuntimeID)
	q := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "继续任务吗", "options": []string{"继续", "停止"}, "nodeRunId": node.ID}))
	instance.expectError(409, "task_conflict", "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "第二题", "options": []string{"x"}, "nodeRunId": node.ID})
	detail := decode[taskmodel.Detail](t, e.expect(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if detail.Nodes[0].Question == nil || !detail.Nodes[0].Question.CanAnswer || detail.Nodes[0].Question.ID != q.ID {
		t.Fatal("real source question view not answerable")
	}
	if _, err := e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "blocked"
		m.Record.Current().Status = "blocked"
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`CREATE FUNCTION fail_question_task_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic task save fault'; END $$`, `CREATE TRIGGER fail_question_task_write BEFORE UPDATE ON task_records FOR EACH ROW EXECUTE FUNCTION fail_question_task_write()`} {
		if _, err := e.st.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	action := uuid.New()
	body := map[string]any{"actionId": action, "expectedNodeRunId": node.ID, "note": "retry"}
	e.expect(500, "POST", "/tasks/"+d.Task.ID.String()+"/retry", body)
	original, err := e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "open" {
		t.Fatal("rollback left closed question", err)
	}
	var pending int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1`, q.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("rollback left outbox", pending, err)
	}
	if _, err = e.st.Pool.Exec(ctx, `DROP TRIGGER fail_question_task_write ON task_records`); err != nil {
		t.Fatal(err)
	}
	retried := decode[taskmodel.Detail](t, e.expect(200, "POST", "/tasks/"+d.Task.ID.String()+"/retry", body))
	next := retried.Nodes[len(retried.Nodes)-1]
	original, err = e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "closed" {
		t.Fatal("persistent close absent", err)
	}
	e.console().expectError(409, "question_closed", "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
	instance.expectError(409, "task_conflict", "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "旧节点新题", "options": []string{"x"}, "nodeRunId": node.ID})
	newQ := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "新问题", "options": []string{"x"}, "nodeRunId": next.ID}))
	e.expect(200, "POST", "/tasks/"+d.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
	e.console().expectError(409, "question_closed", "POST", "/inbox/messages/"+newQ.ID.String()+"/reply", map[string]any{"optionId": "1"})
	forwarded := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+newQ.ID.String()+"/reply", map[string]any{"text": "迟到普通文字"}))
	if forwarded.To.Address == nil || strings.Contains(*forwarded.To.Address, "#") {
		t.Fatal("late ordinary text not forwarded to Manager")
	}
	if err = e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := e.st.GetTask(ctx, d.Task.ID)
	if err != nil || record.View.Task.Status != "cancelled" {
		t.Fatal("late question revived task", err)
	}
}

func TestTaskQuestionAnswerAndExpiryOneRecoveryTurn(t *testing.T) {
	for _, mode := range []string{"answer", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			e, _, d := taskEnv(t)
			ctx := context.Background()
			node := d.Nodes[0]
			instance := e.agentCaller(node.RuntimeID)
			e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
			if _, err := e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, node.RuntimeID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
				m.Record.View.Task.Status = "running"
				m.Record.Current().Status = "running"
				return nil, nil
			}); err != nil {
				t.Fatal(err)
			}
			q := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "question", "options": []string{"yes"}, "nodeRunId": node.ID}))
			if mode == "answer" {
				e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "回答在原轮settled前"})
			} else {
				if _, err := e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, q.ID); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || (got.ReadAt != nil) != (mode == "answer") {
				t.Fatal("only the console's text answer marks the question read", err)
			}
			var turns int
			if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='inbox_reminder'`, node.RuntimeID).Scan(&turns); err != nil || turns != 1 {
				t.Fatal("callback/expiry doubled or lost recovery", turns, err)
			}
			record, err := e.st.GetTask(ctx, d.Task.ID)
			if err != nil || record.Current().Question.Status == "open" {
				t.Fatal("task reference not atomically updated", err)
			}
			var platform int
			if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, q.ID).Scan(&platform); err != nil || platform != map[string]int{"answer": 0, "expiry": 1}[mode] {
				t.Fatal("wrong expiry notice", platform, err)
			}
		})
	}
}

func TestInboxQuestionConfiguredPreviewAndBlockedExpiry(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	e.api.Inbox.O.PreviewChars = 7
	node := d.Nodes[0]
	instance := e.agentCaller(node.RuntimeID)
	e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
	if _, err := e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, node.RuntimeID); err != nil {
		t.Fatal(err)
	}
	q := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "非200摘要", "options": []string{"yes"}, "nodeRunId": node.ID}))
	e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "零一二三四五六七八九十一十二十三十四"})
	raw := e.expect(409, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
	var result struct {
		Error struct {
			Details struct {
				Question store.InboxQuestion `json:"question"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetInboxMessage(ctx, q.ID, 7)
	if err != nil || result.Error.Details.Question.Answer == nil || result.Error.Details.Question.Answer.TextPreview != "零一二三四五六" || result.Error.Details.Question.Answer.TextPreview != got.Question.Answer.TextPreview {
		t.Fatal("409 ignored configured preview", err)
	}
	// The current attempt can ask another question, then block before it expires.
	next := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "blocked question", "options": []string{"yes"}, "nodeRunId": node.ID}))
	if _, err = e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "blocked"
		m.Record.Current().Status = "blocked"
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = e.api.Inbox.ReconcileQuestions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	record, err := e.st.GetTask(ctx, d.Task.ID)
	if err != nil || record.View.Task.Status != "blocked" || record.Current().Status != "blocked" || record.Current().Question.Status != "expired" {
		t.Fatal("expiry retried blocked workflow", err)
	}
	var notices, turns int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, next.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("current blocked expiry notice lost", notices, err)
	}
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND trigger_inbox_message_id IN(SELECT id FROM inbox_messages WHERE in_reply_to=$2 AND from_kind='platform')`, node.RuntimeID, next.ID).Scan(&turns); err != nil || turns != 1 {
		t.Fatal("blocked expiry conversation missing/duplicated", turns, err)
	}
}

func TestInboxQuestionOutboxAfterTaskProjectDeletion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.api.Tasks = tasks.New(e.st, e.api.Config, nil, tasks.Options{MaxSteps: 10, MaxEdges: 10, MaxVisits: 20, MaxRequest: 1 << 20}, e.api.Log)
	e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
	e.api.Inbox.Tasks = e.api.Tasks
	project, err := e.st.CreateProject(ctx, e.dc.Organization.ID, "删除终态项目")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(project.ID), "Manager", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.SetProjectManager(ctx, project.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := e.api.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "ended", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	detail := decode[taskmodel.Detail](t, raw)
	node := detail.Nodes[0]
	p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: detail.Task.ID, RuntimeID: node.RuntimeID}
	q, err := e.api.Inbox.Send(ctx, p, "@console", "old", "queue", inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.api.Tasks.Cancel(ctx, auth.Console(), detail.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	other := e.platformAgent("仍需收到到期")
	sender := e.agentCaller(other.ID)
	later := decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "later", "options": []string{"yes"}}))
	if _, err = e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, later.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = e.api.Inbox.ReconcileQuestions(ctx); err != nil {
			t.Fatal("deleted terminal project blocked queue", err)
		}
	}
	var pending, notices int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND delivered_at IS NULL`, q.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("old terminal event never converged", pending, err)
	}
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, later.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("other project expiry blocked/duplicated", notices, err)
	}
	record, err := e.st.GetTask(ctx, detail.Task.ID)
	if err != nil || record.View.Task.Status != "cancelled" {
		t.Fatal("history cleanup revived deleted task", err)
	}
}

func TestInboxQuestionConcurrentFirstAnswerAndCrossTaskLocks(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	node := d.Nodes[0]
	a := auth.Principal{Kind: auth.KindAgent, ID: node.AgentID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
	raw, err := e.api.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: node.AgentID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "cross", Request: map[string]any{}, Workflow: d.Workflow})
	if err != nil {
		t.Fatal(err)
	}
	second := decode[taskmodel.Detail](t, raw)
	bn := second.Nodes[0]
	b := auth.Principal{Kind: auth.KindAgent, ID: bn.AgentID, TaskID: second.Task.ID, RuntimeID: bn.RuntimeID}
	for _, id := range []uuid.UUID{d.Task.ID, second.Task.ID} {
		if _, err = e.st.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			m.Record.View.Task.Status = "running"
			m.Record.Current().Status = "running"
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, pair := range []struct {
		from auth.Principal
		to   uuid.UUID
		node uuid.UUID
	}{{a, second.Task.ID, node.ID}, {b, d.Task.ID, bn.ID}} {
		go func(pair struct {
			from auth.Principal
			to   uuid.UUID
			node uuid.UUID
		}) {
			<-start
			_, err := e.api.Inbox.Send(ctx, pair.from, "默认项目/Manager#"+pair.to.String(), "cross question", "queue", inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: pair.node})
			results <- err
		}(pair)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal("cross-task transaction deadlocked or failed", err)
		}
	}
	// A fresh normal question gives two independent concurrent replies; exactly
	// one reply, first-answer update and outbox event must commit.
	sender := e.agentCaller(node.AgentID)
	q := decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "first wins", "options": []string{"one", "two"}}))
	statuses := make(chan int, 2)
	for _, option := range []string{"1", "2"} {
		go func(value string) {
			status, _, _ := e.console().do("POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": value})
			statuses <- status
		}(option)
	}
	counts := map[int]int{}
	counts[<-statuses]++
	counts[<-statuses]++
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatal("first answer not linearized", counts)
	}
	var answers int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE answers_message_id=$1`, q.ID).Scan(&answers); err != nil || answers != 1 {
		t.Fatal("multiple answers committed", answers, err)
	}
	// Failure after writing message/question, during recipient reminder lifecycle,
	// rolls back the entire new question and the task aggregate.
	var messages, questions int
	e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&messages)
	e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_questions`).Scan(&questions)
	for _, sql := range []string{`CREATE FUNCTION fail_question_reminder() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic reminder fault'; END $$`, `CREATE TRIGGER fail_question_reminder BEFORE INSERT ON lifecycle_events FOR EACH ROW EXECUTE FUNCTION fail_question_reminder()`} {
		if _, err = e.st.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.api.Inbox.Send(ctx, auth.Console(), "默认项目/Manager#"+d.Task.ID.String(), "rollback-all", "interrupt", inbox.QuestionInput{Options: []string{"yes"}}); err == nil {
		t.Fatal("reminder fault ignored")
	}
	var afterMessages, afterQuestions int
	e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&afterMessages)
	e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_questions`).Scan(&afterQuestions)
	if messages != afterMessages || questions != afterQuestions {
		t.Fatal("failed reminder left partial question/message")
	}
}

type pauseInboxWrite struct {
	once             sync.Once
	entered, release chan struct{}
}

func (p *pauseInboxWrite) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if len(d.Args) > 0 {
		if key, ok := d.Args[0].(string); ok && strings.HasPrefix(key, "inbox-write:") {
			p.once.Do(func() {
				close(p.entered)
				select {
				case <-p.release:
				case <-ctx.Done():
				}
			})
		}
	}
	return ctx
}
func (*pauseInboxWrite) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestInboxQuestionManagerCommitBoundaryAndNoticeIdempotency(t *testing.T) {
	for _, mode := range []string{"ordinary-reply", "closed-question-reply", "notice"} {
		t.Run(mode, func(t *testing.T) {
			e, _, d := taskEnv(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
			node := d.Nodes[0]
			principal := auth.Principal{Kind: auth.KindAgent, ID: node.AgentID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
			input := inbox.QuestionInput{}
			if mode == "closed-question-reply" {
				input = inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID}
			}
			q, err := e.api.Inbox.Send(ctx, principal, "@console", "old thread", "queue", input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.api.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
			next, err := e.st.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "NewManager", "anthropic", "claude-x", "")
			if err != nil {
				t.Fatal(err)
			}
			// No background reader may race replacing the isolated test pool tracer.
			if err = e.conv.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			original := e.st.Pool
			config := original.Config()
			trace := &pauseInboxWrite{entered: make(chan struct{}), release: make(chan struct{})}
			config.ConnConfig.Tracer = trace
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			e.st.Pool = pool
			defer func() { e.st.Pool = original; pool.Close() }()
			var once sync.Once
			release := func() { once.Do(func() { close(trace.release) }) }
			defer release()
			var messageID uuid.UUID
			if mode == "notice" {
				if err = e.st.Pool.QueryRow(ctx, `SELECT message_id FROM task_notifications WHERE task_id=$1 LIMIT 1`, d.Task.ID).Scan(&messageID); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			saved := make(chan store.InboxMessage, 1)
			go func() {
				if mode == "notice" {
					done <- e.api.Inbox.SystemNotice(ctx, node.AgentID, messageID, "")
					return
				}
				msg, err := e.api.Inbox.Reply(ctx, auth.Console(), q.ID, "late ordinary", "queue")
				saved <- msg
				done <- err
			}()
			select {
			case <-trace.entered:
			case <-ctx.Done():
				t.Fatal("routing barrier not reached")
			}
			if _, err = e.st.SetProjectManager(ctx, e.dc.Project.ID, next.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = e.st.RenameProject(ctx, e.dc.Project.ID, "改名后"); err != nil {
				t.Fatal(err)
			}
			release()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "notice" {
				msg, err := e.st.GetInboxMessage(ctx, messageID, 200)
				if err != nil || msg.To.ID != next.ID || msg.From.Kind != "platform" || !strings.Contains(msg.Text, "/p/"+url.PathEscape("改名后")+"/") {
					t.Fatal("notice used stale Manager/link", err)
				}
				for i := 0; i < 3; i++ {
					if err = e.api.Inbox.SystemNotice(ctx, node.AgentID, messageID, ""); err != nil {
						t.Fatal(err)
					}
				}
				var messages, turns int
				e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE id=$1`, messageID).Scan(&messages)
				e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE trigger_inbox_message_id=$1`, messageID).Scan(&turns)
				if messages != 1 || turns != 1 {
					t.Fatal("notice retry dispatched twice", messages, turns)
				}
			} else {
				msg := <-saved
				if msg.To.ID != next.ID || msg.AnswerTo != nil {
					t.Fatal("late reply used stale Manager or became answer")
				}
			}
		})
	}
}

func TestInboxQuestionCommitBeforeAcknowledgementDoesNotRedeliver(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	node := d.Nodes[0]
	instance := e.agentCaller(node.RuntimeID)
	if _, err := e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "running"
		m.Record.Current().Status = "running"
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	q := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "ack lost", "options": []string{"yes"}, "nodeRunId": node.ID}))
	if _, err := e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, q.ID); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`CREATE FUNCTION fail_question_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic acknowledgement failure'; END $$`, `CREATE TRIGGER fail_question_ack BEFORE UPDATE OF delivered_at ON inbox_question_outbox FOR EACH ROW EXECUTE FUNCTION fail_question_ack()`} {
		if _, err := e.st.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.api.Inbox.ReconcileQuestions(ctx); err == nil {
		t.Fatal("lost ack fixture did not fail")
	}
	var pending int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND delivered_at IS NULL`, q.ID).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("failed ack discarded durable event", err)
	}
	if _, err := e.st.Pool.Exec(ctx, `DROP TRIGGER fail_question_ack ON inbox_question_outbox`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var messages, turns int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, q.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='inbox_reminder'`, node.RuntimeID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || turns != 1 {
		t.Fatal("response loss repeated side effects", messages, turns)
	}
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND delivered_at IS NULL`, q.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("retry never converged", err)
	}
}

func TestInboxQuestionPlatformProjectionAndNoPublicIdentity(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	node := d.Nodes[0]
	if _, err := e.api.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(ctx, `SELECT message_id FROM task_notifications WHERE task_id=$1 LIMIT 1`, d.Task.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := e.api.Inbox.SystemNotice(ctx, node.AgentID, id, ""); err != nil {
		t.Fatal(err)
	}
	manager := e.agentCaller(node.AgentID)
	message := decode[apitypes.InboxMessage](t, manager.expect(200, "POST", "/inbox/messages/"+id.String()+"/read", nil))
	if message.From.Kind != "platform" || message.From.Address == nil || *message.From.Address != "@platform" {
		t.Fatal("platform mislabeled or disguised as console")
	}
	manager.expectError(403, "forbidden", "POST", "/inbox/messages/"+id.String()+"/reply", map[string]any{"text": "cannot deliver to platform"})
	e.console().expectError(404, "not_found", "POST", "/inbox/messages", map[string]any{"to": "@platform", "text": "forbidden"})
	e.expect(422, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "spoof", "from": map[string]any{"kind": "platform"}}, "X-Contract-Invalid-Request", "1")
}

func TestInboxQuestionNullNewFieldsRejectWithoutWrite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.platformAgent("null guards")
	sender := e.agentCaller(a.ID)
	q := decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "question", "options": []string{"yes"}}))
	var before int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{{"text": "must not downgrade", "optionId": nil}, {"text": "must not ignore", "nodeRunId": nil}, {"text": "bad options", "options": nil}} {
		e.expect(422, "POST", "/inbox/messages/"+q.ID.String()+"/reply", body, "X-Contract-Invalid-Request", "1")
	}
	e.expect(422, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "must not ignore", "nodeRunId": nil}, "X-Contract-Invalid-Request", "1")
	var after int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&after); err != nil || after != before {
		t.Fatal("null fields produced a message", before, after, err)
	}
	original, err := e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "open" {
		t.Fatal("null option answered question", err)
	}
	// Absent optionId remains a real text answer, not rejected as missing.
	answered := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "valid text"}))
	if answered.AnswerTo == nil || answered.AnswerTo.MessageID != q.ID {
		t.Fatal("missing optionId broke text compatibility")
	}
}

func TestInboxQuestionRealViewReferenceAndExternalRecipient(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	node := d.Nodes[0]
	sender := e.agentCaller(node.RuntimeID)
	identity, token := e.identity("question-recipient")
	external := e.external(token)
	q := decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@question-recipient", "text": "external only", "options": []string{"yes"}, "nodeRunId": node.ID}))
	ref := taskmodel.Question{ID: q.ID, MessageID: q.ID, ThreadID: q.ThreadID, Status: "open"}
	console, err := e.api.Inbox.ViewQuestion(ctx, auth.Console(), ref)
	if err != nil || console.CanAnswer {
		t.Fatal("console gained external answer permission", err)
	}
	allowed, err := e.api.Inbox.ViewQuestion(ctx, auth.Principal{Kind: auth.KindExternal, ID: identity.ID}, ref)
	if err != nil || !allowed.CanAnswer {
		t.Fatal("real receiver lost permission", err)
	}
	wrong := ref
	wrong.ThreadID = uuid.New()
	if _, err = e.api.Inbox.ViewQuestion(ctx, auth.Console(), wrong); err == nil {
		t.Fatal("wrong thread reference accepted")
	}
	wrong = ref
	wrong.MessageID = uuid.New()
	if _, err = e.api.Inbox.ViewQuestion(ctx, auth.Console(), wrong); err == nil {
		t.Fatal("wrong message reference accepted")
	}
	external.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
	original, err := e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "answered" || original.Question.Answer == nil {
		t.Fatal("external first answer unavailable", err)
	}
}

func TestTaskQuestionNextAfterPersistedExpiryBeforeCallback(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	node := d.Nodes[0]
	sender := e.agentCaller(node.RuntimeID)
	create := func(text string) apitypes.InboxMessage {
		return decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": text, "options": []string{"yes"}, "nodeRunId": node.ID}))
	}
	old := create("old question")
	if _, err := e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	// Real expiry transaction commits first; intentionally hold its reliable task
	// callback until after the Agent asks the next question in the same attempt.
	if err := e.st.ExpireInboxQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	next := create("new question")
	e.console().expectError(409, "question_expired", "POST", "/inbox/messages/"+old.ID.String()+"/reply", map[string]any{"optionId": "1"})
	if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := e.st.GetTask(ctx, d.Task.ID)
	if err != nil || record.Current().Question.ID != next.ID || record.Current().Question.Status != "open" {
		t.Fatal("late old callback changed new question", err)
	}
}

func TestInboxQuestionExpirySkipsLockedRowAndRetries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.platformAgent("到期锁测试")
	sender := e.agentCaller(a.ID)
	create := func(text string) uuid.UUID {
		return decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": text, "options": []string{"yes"}})).ID
	}
	locked, other := create("locked"), create("other")
	if _, err := e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=ANY($1)`, []uuid.UUID{locked, other}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT message_id FROM inbox_questions WHERE message_id=$1 FOR UPDATE`, locked); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = e.st.ExpireInboxQuestions(bounded); err != nil {
		t.Fatal("locked question blocked independent expiry", err)
	}
	assertState := func(id uuid.UUID, want string, events int) {
		t.Helper()
		var state string
		var count int
		if err := e.st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND state='expired') FROM inbox_questions WHERE message_id=$1`, id).Scan(&state, &count); err != nil || state != want || count != events {
			t.Fatalf("state=%s events=%d want=%s/%d: %v", state, count, want, events, err)
		}
	}
	assertState(locked, "open", 0)
	assertState(other, "expired", 1)
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = e.st.ExpireInboxQuestions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertState(locked, "expired", 1)
	assertState(other, "expired", 1)
}
