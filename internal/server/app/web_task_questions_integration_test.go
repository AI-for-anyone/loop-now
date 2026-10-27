package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/testenv"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exclusive Web driver allocated by Main/server; shared server helpers stay read-only.
func TestWebTaskQuestionsIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_TASKS_INTEGRATION") != "1" {
		t.Skip("explicit isolated browser question integration only")
	}
	if os.Getenv("LOOP_NOW_TEST_DATABASE_URL") == "" || os.Getenv("LOOP_NOW_TEST_REDIS_URL") == "" {
		t.Fatal("explicit TEST endpoints required")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/scripts/integration/tasks")
	var w *world
	var addr, controlAddr string
	audit := map[string]any{"mode": "actual React -> route.fetch -> app.Build -> TEST PG; fakepi; ordinary question task integration"}
	// Registered first, runs after App shutdown and existing schema/prefix/temp cleanup.
	t.Cleanup(func() {
		if w == nil {
			return
		}
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		audit["appAddress"] = addr
		audit["controlAddress"] = controlAddr
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, pgtest.URL(t))
		if err != nil {
			t.Error("cleanup PG verification failed")
			return
		}
		defer conn.Close(ctx)
		var exists bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists); err != nil || exists {
			t.Error("isolated schema cleanup not verified")
		}
		audit["schemaRemoved"] = !exists && err == nil
		options, err := redis.ParseURL(w.cfg.RedisURL)
		if err != nil {
			t.Error("cleanup Redis configuration invalid")
			return
		}
		rc := redis.NewClient(options)
		defer rc.Close()
		iterator := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := iterator.Next(ctx)
		err = iterator.Err()
		audit["redisPrefixEmpty"] = err == nil && !found
		if err != nil || found {
			t.Error("isolated Redis cleanup not verified")
		}
		_, err = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(err)
		if !os.IsNotExist(err) {
			t.Error("isolated work root cleanup not verified")
		}
		closed := true
		for _, address := range []string{addr, controlAddr} {
			if address == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", address, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("temporary listener still open")
			}
		}
		audit["temporaryListenersClosed"] = closed
		data, _ := json.MarshalIndent(audit, "", "  ")
		if err = os.WriteFile(filepath.Join(evidence, "question-app-driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized integration audit")
		}
	})

	w = newWorld(t)
	helper := &taskAuthOffline{}
	helper.account.Store("web-question-unused")
	s := w.startOfflineAuth(helper)
	addr = s.a.Addr()
	t.Cleanup(s.stop)
	ctx := context.Background()
	managerPath, manager := s.createAgent("题联调Manager")
	workerPath, worker := s.createAgent("题联调Worker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	for _, path := range []string{managerPath, workerPath} {
		s.must(200, "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-question-offline"})
	}
	s.must(202, "POST", workerPath+"/messages", map[string]any{"text": "web question independent worker baseline"})
	s.waitIdle(workerPath)
	normal, e := s.a.Store.GetAgentSession(ctx, worker.ID)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(normal.SessionFile)
	if e != nil {
		t.Fatal(e)
	}
	create := func(title string, def workflow.Definition) taskmodel.Detail {
		raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title, Request: map[string]any{}, Workflow: def})
		if e != nil {
			t.Fatal(e)
		}
		return decode[taskmodel.Detail](t, raw)
	}
	step := func(id, description string) workflow.Step {
		return workflow.Step{ID: id, Title: id, Description: description, AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}
	}
	principal := func(taskID uuid.UUID, n taskmodel.NodeRun) auth.Principal {
		return auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: taskID, RuntimeID: n.RuntimeID}
	}
	ask := func(taskID uuid.UUID, n taskmodel.NodeRun, to, text string) (store.InboxMessage, error) {
		return s.a.API.Inbox.Send(ctx, principal(taskID, n), to, text, "queue", inbox.QuestionInput{Options: []string{"继续", "调整"}, NodeRunID: n.ID})
	}
	producer := step("produce", " !sleep=1s !text=web-source-output")
	producer.OutputFields = []workflow.Field{{Name: "text", Type: "string", Required: true}}
	review := step("review", " !sleep=2s !text=web-review-output")
	review.InputFields = []workflow.Field{{Name: "text", Type: "string", Required: true}}
	producerID := "produce"
	review.Inputs = map[string]workflow.Source{"text": {Kind: "node", NodeID: &producerID, Field: "text"}}
	a := create("Web真实多节点原题", workflow.Definition{StartStepID: "produce", Steps: []workflow.Step{producer, review}, Edges: []workflow.Edge{{ID: "next", From: "produce", To: "review", Label: "交给复核"}}})
	source := taskWait(t, s, a.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	}).Nodes[0]
	edge := "next"
	if _, e = s.a.Tasks.Submit(ctx, principal(a.Task.ID, source), a.Task.ID, source.ID, taskmodel.SubmitInput{ActionID: uuid.New(), EdgeID: &edge, Output: map[string]any{"text": "mapped actual source input"}}); e != nil {
		t.Fatal(e)
	}
	mapped := taskWait(t, s, a.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) == 2 && s.a.Executor.Process(d.Nodes[1].RuntimeID).State == "busy"
	})
	aq, e := ask(a.Task.ID, mapped.Nodes[1], "@console", "Web真实原题：是否继续复核？")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.a.API.Inbox.Reply(ctx, principal(a.Task.ID, mapped.Nodes[1]), aq.ID, "Web真实原题之后的普通消息", "queue"); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, a.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "waiting" })
	b := create("Web真实重试关闭", workflow.Definition{StartStepID: "work", Steps: []workflow.Step{step("work", " !sleep=5s !error=web-question-error !text=web-retry-output")}, Edges: []workflow.Edge{}})
	bn := taskWait(t, s, b.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	}).Nodes[0]
	bq, e := ask(b.Task.ID, bn, "@console", "Web旧题：等待重试关闭")
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, b.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	identity := decode[apitypes.ExternalIdentity](t, s.must(201, "POST", "/external-identities", map[string]any{"name": "web-question-reader"}))
	c := create("Web真实权限题", workflow.Definition{StartStepID: "private", Steps: []workflow.Step{step("private", " !sleep=2s !error=web-private-error !text=web-private-output")}, Edges: []workflow.Edge{}})
	cn := taskWait(t, s, c.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	}).Nodes[0]
	cq, e := ask(c.Task.ID, cn, "@"+identity.Name, "Web外部身份专属原题")
	if e != nil {
		t.Fatal(e)
	}
	control := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/new-question" {
			http.NotFound(rw, r)
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			record, err := s.a.Store.GetTask(r.Context(), b.Task.ID)
			if err != nil {
				http.Error(rw, "task read failed", 500)
				return
			}
			n := record.Current()
			if n != nil && n.ID != bn.ID && n.Status != "blocked" && n.Question == nil && s.a.Executor.Process(n.RuntimeID).State == "busy" {
				q, err := ask(b.Task.ID, *n, "@console", "Web新题：不能被旧答案解除")
				if err != nil {
					http.Error(rw, "question create failed", 500)
					return
				}
				rw.Header().Set("Content-Type", "application/json")
				json.NewEncoder(rw).Encode(map[string]string{"id": q.ID.String(), "threadId": q.ThreadID.String(), "nodeRunId": n.ID.String()})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		http.Error(rw, "new attempt not running", 409)
	}))
	controlAddr = strings.TrimPrefix(control.URL, "http://")
	t.Cleanup(control.Close)
	manifest := map[string]any{"apiBase": s.url, "controlBase": control.URL, "projectName": dc.Project.Name, "mappedTaskId": a.Task.ID.String(), "mappedQuestionId": aq.ID.String(), "mappedThreadId": aq.ThreadID.String(), "sourceNodeId": source.ID.String(), "retryTaskId": b.Task.ID.String(), "oldQuestionId": bq.ID.String(), "oldThreadId": bq.ThreadID.String(), "privateTaskId": c.Task.ID.String(), "privateQuestionId": cq.ID.String()}
	manifestPath := filepath.Join(t.TempDir(), "question-browser-manifest.json")
	data, _ := json.Marshal(manifest)
	if os.WriteFile(manifestPath, data, 0600) != nil {
		t.Fatal("manifest write failed")
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(evidence, "question-app-browser-verify.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_TASK_QUESTIONS_MANIFEST=" + manifestPath}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Run(); e != nil {
		t.Fatalf("actual question browser failed: %v; sanitized failure artifact records phase", e)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	for _, id := range []uuid.UUID{a.Task.ID, b.Task.ID, c.Task.ID} {
		record, err := s.a.Store.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !record.Ended() {
			if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
				t.Fatal(e)
			}
		}
		taskWait(t, s, id, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
	}
	var answerCount int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_questions WHERE message_id=$1 AND state='answered' AND answer_option_id='1'`, aq.ID).Scan(&answerCount); e != nil || answerCount != 1 {
		t.Fatal("original option answer PG mismatch")
	}
	var closedCount int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_questions q JOIN inbox_questions i ON i.message_id=q.question_message_id WHERE q.task_id=$1 AND i.state='closed'`, b.Task.ID).Scan(&closedCount); e != nil || closedCount != 2 {
		t.Fatal("old/new close PG mismatch", closedCount, e)
	}
	final, e := s.a.Store.GetTask(ctx, b.Task.ID)
	if e != nil || final.View.Task.Status != "cancelled" {
		t.Fatal("late reply revived task")
	}
	for _, n := range final.View.Nodes {
		if n.Question != nil && n.Question.Status != "closed" {
			t.Fatal("late reply changed closed reference")
		}
	}
	after, e := os.ReadFile(normal.SessionFile)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("task altered independent worker baseline session")
	}
	audit["originalOptionAnswerCount"] = answerCount
	audit["retryTaskClosedQuestions"] = closedCount
	audit["lateRepliesDidNotReviveTask"] = true
	audit["independentWorkerNormalSessionUnchanged"] = true
	audit["offlineRefreshes"] = helper.refreshes.Load()
	audit["passed"] = !t.Failed()
}
