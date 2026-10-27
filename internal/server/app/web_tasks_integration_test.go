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
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/testenv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Web-owned driver allocated by server. Existing server helpers are read-only.
func TestWebTasksIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_TASKS_INTEGRATION") != "1" {
		t.Skip("explicit isolated real-browser task integration only")
	}
	if os.Getenv("LOOP_NOW_TEST_DATABASE_URL") == "" || os.Getenv("LOOP_NOW_TEST_REDIS_URL") == "" {
		t.Fatal("explicit TEST endpoints required; no local configuration fallback")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/scripts/integration/tasks")
	var w *world
	var addr, controlAddr string
	audit := map[string]any{"mode": "actual React -> route.fetch -> app.Build -> TEST PG; fakepi; non-question tasks only"}
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
		if err = os.WriteFile(filepath.Join(evidence, "app-driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized integration audit")
		}
	})

	w = newWorld(t)
	helper := &taskAuthOffline{}
	helper.account.Store("web-tasks-offline-unused")
	s := w.startOfflineAuth(helper)
	addr = s.a.Addr()
	t.Cleanup(s.stop)
	ctx := context.Background()
	path, agent := s.createAgent("Web任务隔离Manager")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	s.must(200, "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-web-tasks-only"})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "offline fakepi normal baseline"})
	s.waitIdle(path)
	normal, e := s.a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(normal.SessionFile)
	if e != nil {
		t.Fatal(e)
	}
	create := func(title, description string, output []workflow.Field) taskmodel.Detail {
		raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title, Request: map[string]any{"seed": "isolated browser input"}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "隔离执行节点", Description: description, AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: output, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
		if e != nil {
			t.Fatal(e)
		}
		return decode[taskmodel.Detail](t, raw)
	}
	archived := create("Web隔离已归档任务", " !sleep=3s !text=web-archived-message !usage=7,11,0,0", []workflow.Field{{Name: "file", Type: "artifact", Required: true}})
	running := taskWait(t, s, archived.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.Nodes) > 0 && len(d.Nodes[0].RunIDs) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	node := running.Nodes[0]
	principal := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: archived.Task.ID, RuntimeID: node.RuntimeID}
	raw, e := s.a.Tasks.Publish(ctx, principal, archived.Task.ID, node.ID, uuid.New(), "web-retained.txt", "text/plain", []byte("web isolated retained artifact"))
	if e != nil {
		t.Fatal(e)
	}
	artifact := decode[taskmodel.Artifact](t, raw)
	if _, e = s.a.Tasks.Submit(ctx, principal, archived.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"file": map[string]any{"artifactId": artifact.ID.String()}}}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, archived.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	blocked := create("Web隔离阻塞任务", " !sleep=1s !text=web-blocked-message", []workflow.Field{})
	taskWait(t, s, blocked.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	manifest := map[string]any{"apiBase": s.url, "projectName": dc.Project.Name, "archivedTaskId": archived.Task.ID.String(), "archivedTitle": archived.Task.Title, "blockedTaskId": blocked.Task.ID.String(), "blockedTitle": blocked.Task.Title, "artifactId": artifact.ID.String()}
	manifestPath := filepath.Join(t.TempDir(), "tasks-browser-manifest.json")
	data, _ := json.Marshal(manifest)
	if os.WriteFile(manifestPath, data, 0600) != nil {
		t.Fatal("manifest write failed")
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(evidence, "app-browser-verify.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_TASKS_MANIFEST=" + manifestPath}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Run(); e != nil {
		t.Fatalf("browser task integration failed: %v; see sanitized failure artifact", e)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	final := taskWait(t, s, blocked.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
	if len(final.Nodes) != 2 || final.Nodes[0].RuntimeID != final.Nodes[1].RuntimeID {
		t.Fatal("retry instance isolation mismatch")
	}
	after, e := os.ReadFile(normal.SessionFile)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("task altered normal session")
	}
	for _, runtimeID := range []uuid.UUID{node.RuntimeID, final.Nodes[0].RuntimeID} {
		if _, e = os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", runtimeID.String())); !os.IsNotExist(e) {
			t.Fatal("task runtime directory not reclaimed")
		}
		var n int
		if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_archives WHERE runtime_id=$1`, runtimeID).Scan(&n); e != nil || n != 1 {
			t.Fatal("task archive not retained")
		}
	}
	var turns int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, blocked.Task.ID).Scan(&turns); e != nil || turns != 2 {
		t.Fatal("retry turn count mismatch")
	}
	audit["taskCount"] = 2
	audit["retryTurns"] = turns
	audit["taskDirectoriesRemoved"] = true
	audit["taskArchivesRetained"] = true
	audit["normalSessionUnchanged"] = true
	audit["offlineRefreshes"] = helper.refreshes.Load()
	audit["passed"] = !t.Failed()
	if helper.refreshes.Load() != 0 {
		t.Fatal("unexpected auth refresh")
	}
}
