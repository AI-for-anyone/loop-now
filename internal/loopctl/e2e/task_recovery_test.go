package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/server/taskmodel"
)

func injectedTaskClient(t *testing.T, w *world, taskID, runtimeID string) *loopctl {
	t.Helper()
	l := newLoopctl(t, w.agentEnv())
	who := l.run("whoami").ok(t)
	if who["taskId"] != taskID || who["runtimeId"] != runtimeID {
		t.Fatal("fakepi injected token is not scoped to expected task runtime")
	}
	return l
}

func countTaskTurns(t *testing.T, s *server, id string) int {
	t.Helper()
	var n int
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE task_id=$1`, uuid.MustParse(id)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTaskCLIRecoveryDoesNotReplayAndReplacesProcessTokens(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		name := "running"
		if submitted {
			name = "submitted-before-settled"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, false)
			s := w.start(nil)
			active := s
			t.Cleanup(func() {
				if active != nil {
					active.stop()
				}
			})
			managerID, manager := newTaskManager(t, s, "恢复Manager")
			worker := s.createAgent("恢复执行者")
			created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=10s"), "task", "create")
			id := created["task"].(map[string]any)["id"].(string)
			n := currentCLINode(t, waitRunningCLITask(t, s, id))
			oldProcess := s.a.Executor.Process(n.RuntimeID)
			node := injectedTaskClient(t, w, id, n.RuntimeID.String())
			session := waitCLISessionPath(t, w, n.RuntimeID)
			if submitted {
				taskCLIInput(t, node, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{"result": "saved before restart"}, "edgeId": nil}, "task", "submit", id, n.ID.String())
			}
			before := countTaskTurns(t, s, id)
			if before != 1 {
				t.Fatal("unexpected initial turn count", before)
			}
			// 只停止本测试的临时App，保持相同测试schema/地址/工作根，绝不操作8780旧服务。
			s.stop()
			active = nil
			restarted := w.start(nil)
			active = restarted
			node.run("task", "current").fails(t, "unauthenticated", 5)
			manager.run("task", "get", id).fails(t, "unauthenticated", 5)
			freshManager := testTaskClient(t, restarted, managerID)
			blocked := waitCLITask(t, restarted, id, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
			inst, err := restarted.a.Store.TaskInstance(context.Background(), n.RuntimeID)
			if err != nil || inst.SessionFile == nil || *inst.SessionFile != session {
				got := "<nil>"
				if inst.SessionFile != nil {
					got = *inst.SessionFile
				}
				t.Fatalf("stop/restart did not retain persisted session: expected=%s actual=%s err=%v", session, got, err)
			}
			if blocked.Task.CurrentNodeRunID == nil || *blocked.Task.CurrentNodeRunID != n.ID || len(blocked.Nodes) != 1 {
				t.Fatal("restart changed node without explicit retry")
			}
			if submitted && blocked.Nodes[0].Output["result"] != "saved before restart" {
				t.Fatal("restart lost saved output")
			}
			if !submitted && blocked.Nodes[0].Output != nil {
				t.Fatal("restart fabricated an output")
			}
			freshManager.run("task", "get", id).ok(t)
			history := freshManager.run("task", "history", id, n.ID.String()).ok(t)
			found := false
			for _, v := range history["messages"].([]any) {
				m := v.(map[string]any)
				if m["role"] == "assistant" && m["errorCode"] == "server_restarted" && m["errorMessage"] != nil {
					found = true
				}
			}
			if !found {
				t.Fatal("restart error was not preserved in TaskMessage JSON")
			}
			for i := 0; i < 3; i++ {
				if countTaskTurns(t, restarted, id) != before || restarted.a.Executor.Process(n.RuntimeID).State != "stopped" {
					t.Fatal("restart automatically replayed task execution")
				}
				time.Sleep(w.cfg.TaskReconcileInterval)
			}
			// 只有本次明确的Manager retry才产生新node/turn；复用原runtime与会话。
			freshManager.run("task", "retry", id, "--action-id", uuid.NewString(), "--expected-node-run", n.ID.String(), "--yes").ok(t)
			resumed := currentCLINode(t, waitRunningCLITask(t, restarted, id))
			if resumed.ID == n.ID || resumed.RuntimeID != n.RuntimeID || resumed.Attempt != 2 {
				t.Fatal("retry did not preserve runtime and advance dispatch")
			}
			inst, err = restarted.a.Store.TaskInstance(context.Background(), n.RuntimeID)
			if err != nil || inst.SessionFile == nil || *inst.SessionFile != session {
				t.Fatal("retry replaced original session", err)
			}
			freshNode := injectedTaskClient(t, w, id, n.RuntimeID.String())
			if freshNode.vars[conn.EnvAgentToken] == node.vars[conn.EnvAgentToken] {
				t.Fatal("Pi restart reused old token")
			}
			if restarted.a.Executor.Process(n.RuntimeID).PID == oldProcess.PID {
				t.Fatal("test did not restart Pi process")
			}
			node.run("task", "current").fails(t, "unauthenticated", 5)
			current := freshNode.run("task", "current").ok(t)
			if current["task"].(map[string]any)["currentNodeRunId"] != resumed.ID.String() || countTaskTurns(t, restarted, id) != 2 {
				t.Fatal("explicit retry missing or extra automatic task execution")
			}
			cancelCLITask(t, restarted, freshManager, id)
			freshNode.run("task", "current").fails(t, "unauthenticated", 5)
			t.Log("5.1/R3: old injected process tokens rejected, task blocked without replay, error JSON retained, explicit retry uses same runtime/session and new token")
		})
	}
}
