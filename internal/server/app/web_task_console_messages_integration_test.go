package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// Web owns only this new file. Shared helpers and production code are read-only.
func TestWebTaskConsoleMessagesIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_TASK_CONSOLE_INTEGRATION") != "1" {
		t.Skip("explicit isolated TEST App/fakePi browser integration")
	}
	db, e := url.Parse(pgtest.URL(t))
	if e != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("TEST PG required")
	}
	ro, e := redis.ParseURL(redistest.URL(t))
	if e != nil || ro.DB != 10 {
		t.Fatal("TEST Redis DB10 required")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/improve-task-console-interaction/app")
	if os.MkdirAll(evidence, 0700) != nil {
		t.Fatal("evidence directory failed")
	}
	var w *world
	var address string
	audit := map[string]any{"boundary": "actual React -> forwarded temporary app.Build/TEST PG/Redis/fakePi; no provider/model/current services", "webOrigin": "http://127.0.0.1:4320"}
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		c, err := pgx.Connect(ctx, pgtest.URL(t))
		if err != nil {
			t.Error("PG cleanup read failed")
			return
		}
		defer c.Close(ctx)
		var exists bool
		err = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists)
		audit["schemaRemoved"] = err == nil && !exists
		if err != nil || exists {
			t.Error("schema cleanup failed")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("Redis cleanup failed")
		}
		_, err = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(err)
		if !os.IsNotExist(err) {
			t.Error("work root remains")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4320"} {
			if addr == "" {
				continue
			}
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				closed = false
				t.Error("temporary listener remains")
			}
		}
		audit["listenersClosed"] = closed
		raw, _ := json.MarshalIndent(audit, "", "  ")
		if os.WriteFile(filepath.Join(evidence, "driver-results.json"), raw, 0600) != nil {
			t.Error("audit write failed")
		}
	})
	w = newWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4320"}
	w.cfg.PiMaxProcesses = 4
	s := w.start(nil)
	address = s.a.Addr()
	t.Log("WEB_TASK_APP_READY: compiled isolated app.Build started; public App source editing window released")
	t.Cleanup(s.stop)
	ctx := context.Background()
	managerPath, manager := s.createAgent("WebStrictManager")
	workerPath, worker := s.createAgent("WebStrictWorker")
	_ = managerPath
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "web-strict-synthetic-model", "credential": map[string]any{"type": "api_key", "key": "synthetic-web-strict-only"}})
	s.must(202, "POST", workerPath+"/messages", map[string]any{"text": "!text=web-strict-normal-baseline"})
	s.waitIdle(workerPath)
	normal, err := s.a.Store.GetAgentSession(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(normal.SessionFile)
	if err != nil {
		t.Fatal("normal baseline read failed")
	}
	step := func(id, description string) workflow.Step {
		return workflow.Step{ID: id, Title: id, Description: description, AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}
	}
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "Web strict isolated node conversation", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{step("work", " !sleep=6s !text=web-strict-initial"), step("next", " !sleep=6s !text=web-strict-next")}, Edges: []workflow.Edge{{ID: "next", From: "work", To: "next", Label: "continue"}}}})
	if err != nil {
		t.Fatal(err)
	}
	created := decode[taskmodel.Detail](t, raw)
	t.Cleanup(func() {
		_, _ = s.a.Tasks.Cancel(context.Background(), auth.Console(), created.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()})
	})
	running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && len(d.Nodes[0].RunIDs) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	node := running.Nodes[0]
	question, err := s.a.API.Inbox.Send(ctx, auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}, manager.ID.String(), "Synthetic Manager-only original question", "queue", inbox.QuestionInput{Options: []string{"continue", "adjust"}, NodeRunID: node.ID})
	if err != nil {
		t.Fatal("question fixture failed", err)
	}
	manifest := map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4320", "projectName": dc.Project.Name, "taskId": created.Task.ID, "nodeRunId": node.ID, "agentId": worker.ID, "runtimeId": node.RuntimeID, "questionId": question.ID}
	manifestPath := filepath.Join(t.TempDir(), "task-console-manifest.json")
	raw, _ = json.Marshal(manifest)
	if os.WriteFile(manifestPath, raw, 0600) != nil {
		t.Fatal("manifest failed")
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/task-console-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_TASK_CONSOLE_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	counts := map[string]int{}
	for name, query := range map[string]string{"consoleTurns": `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, "consoleLineage": `SELECT count(*) FROM task_console_message_lineage WHERE task_id=$1`, "foreignNodeConsoleTurns": `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node' AND node_run_id<>$2`, "consoleCompleted": `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node' AND status='completed'`, "consoleInterrupted": `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node' AND status='interrupted'`} {
		var n int
		var err error
		if name == "foreignNodeConsoleTurns" {
			err = s.a.Store.Pool.QueryRow(ctx, query, created.Task.ID, node.ID).Scan(&n)
		} else {
			err = s.a.Store.Pool.QueryRow(ctx, query, created.Task.ID).Scan(&n)
		}
		if err != nil {
			t.Fatal("PG proof read failed", name)
		}
		counts[name] = n
	}
	audit["pgCounts"] = counts
	audit["taskId"] = created.Task.ID
	audit["nodeRunId"] = node.ID
	audit["runtimeId"] = node.RuntimeID
	raw, _ = json.MarshalIndent(counts, "", "  ")
	_ = os.WriteFile(filepath.Join(evidence, "pg-readback.json"), raw, 0600)
	if browserErr != nil {
		t.Fatal("browser assertion failed; sanitized failure/PG evidence retained")
	}
	final := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
	if len(final.Nodes) != 1 || final.Nodes[0].ID != node.ID || final.Nodes[0].Attempt != node.Attempt {
		t.Fatal("conversation changed original node/attempt")
	}
	after, err := os.ReadFile(normal.SessionFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("task console modified normal session")
	}
	audit["normalSessionUnchanged"] = true
	if counts["consoleTurns"] != 2 || counts["consoleLineage"] != 2 || counts["foreignNodeConsoleTurns"] != 0 || counts["consoleCompleted"] != 1 || counts["consoleInterrupted"] != 1 {
		t.Fatal("strict PG outcome mismatch")
	}
	audit["passed"] = true
}
