package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

func taskTextRequest(agent, description string) taskmodel.CreateInput {
	in := taskCLIRequest(agent, description)
	in.Workflow.Steps[0].OutputFields = []workflow.Field{{Name: "result", Type: "string", Required: true}}
	return in
}

func currentCLINode(t *testing.T, d taskmodel.Detail) taskmodel.NodeRun {
	t.Helper()
	for _, n := range d.Nodes {
		if d.Task.CurrentNodeRunID != nil && n.ID == *d.Task.CurrentNodeRunID {
			return n
		}
	}
	t.Fatal("task has no current node")
	return taskmodel.NodeRun{}
}

func waitRunningCLITask(t *testing.T, s *server, id string) taskmodel.Detail {
	t.Helper()
	return waitCLITask(t, s, id, func(d taskmodel.Detail) bool {
		if d.Task.Status != "running" {
			return false
		}
		n := currentCLINode(t, d)
		return len(n.RunIDs) > 0 && s.a.Executor.Process(n.RuntimeID).State == "busy"
	})
}

// 执行器在结束轮次/停进程时才持久化SessionFile；忙时检查实际磁盘，不假设DB已记路径。
func waitCLISessionPath(t *testing.T, w *world, runtimeID uuid.UUID) string {
	t.Helper()
	pattern := filepath.Join(w.cfg.WorkRoot, "agents", runtimeID.String(), "sessions", "*.jsonl")
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() && st.Size() > 0 {
				resolved, e := filepath.EvalSymlinks(p)
				if e != nil {
					t.Fatal(e)
				}
				return resolved
			}
		}
	}
	t.Fatal("fakepi did not persist a session file")
	return ""
}

func newTaskManager(t *testing.T, s *server, name string) (string, *loopctl) {
	t.Helper()
	id := s.createAgent(name)
	s.console(200, "PUT", "/projects/"+s.defaultProject()+"/manager", map[string]any{"agentId": id})
	return id, testTaskClient(t, s, id)
}

func cancelCLITask(t *testing.T, s *server, manager *loopctl, id string) {
	t.Helper()
	manager.run("task", "cancel", id, "--action-id", uuid.NewString(), "--yes").ok(t)
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
}

func TestTaskCLIMultipleTasksIsolateAgentWorkspaceAndSession(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	_, manager := newTaskManager(t, s, "隔离Manager")
	worker := s.createAgent("共享执行者")
	// 建立常态历史和磁盘积累，任务可分叉历史但不能复制积累文件。
	s.console(202, "POST", "/agents/"+worker+"/messages", map[string]any{"text": "normal baseline"})
	s.waitIdle(worker)
	normal, err := s.a.Store.GetAgentSession(context.Background(), uuid.MustParse(worker))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := os.ReadFile(normal.SessionFile)
	if err != nil {
		t.Fatal(err)
	}
	normalRoot := filepath.Join(w.cfg.WorkRoot, "agents", worker, "workspace")
	if err = os.WriteFile(filepath.Join(normalRoot, "normal-only.txt"), []byte("normal"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	nodes := []taskmodel.NodeRun{}
	clients := []*loopctl{}
	for i := 0; i < 2; i++ {
		created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=10s"), "task", "create")
		id := created["task"].(map[string]any)["id"].(string)
		ids = append(ids, id)
		d := waitRunningCLITask(t, s, id)
		n := currentCLINode(t, d)
		nodes = append(nodes, n)
		clients = append(clients, testTaskClient(t, s, n.RuntimeID.String()))
		if clients[i].run("task", "current").ok(t)["task"].(map[string]any)["id"] != id {
			t.Fatal("current task crossed instances")
		}
	}
	if nodes[0].RuntimeID == nodes[1].RuntimeID {
		t.Fatal("two tasks shared runtime")
	}
	instances := make([]string, 2)
	for i, n := range nodes {
		instances[i] = waitCLISessionPath(t, w, n.RuntimeID)
		root := filepath.Join(w.cfg.WorkRoot, "agents", n.RuntimeID.String(), "workspace")
		if _, e := os.Stat(filepath.Join(root, "normal-only.txt")); !os.IsNotExist(e) {
			t.Fatal("normal accumulated file copied into task", e)
		}
	}
	if instances[0] == instances[1] || instances[0] == normal.SessionFile || instances[1] == normal.SessionFile {
		t.Fatal("sessions not isolated")
	}
	aRoot := filepath.Join(w.cfg.WorkRoot, "agents", nodes[0].RuntimeID.String(), "workspace")
	if err = os.WriteFile(filepath.Join(aRoot, "task-a-only.txt"), []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	bRoot := filepath.Join(w.cfg.WorkRoot, "agents", nodes[1].RuntimeID.String(), "workspace")
	if _, err = os.Stat(filepath.Join(bRoot, "task-a-only.txt")); !os.IsNotExist(err) {
		t.Fatal("task A file leaked into B")
	}
	after, err := os.ReadFile(normal.SessionFile)
	if err != nil || !bytes.Equal(after, baseline) {
		t.Fatal("normal history changed by task execution", err)
	}
	// A 的令牌不能向 B 提交或上传，即便 Agent 配置 ID 相同。
	clients[0].stdin = `{"actionId":"` + uuid.NewString() + `","output":{"result":"wrong instance"},"edgeId":null}`
	clients[0].run("task", "submit", ids[1], nodes[1].ID.String(), "--from-file", "-").fails(t, "forbidden", 6)
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("artifact A"), 0o600)
	a := clients[0].run("task", "artifact", "put", ids[0], nodes[0].ID.String(), "--action-id", uuid.NewString(), "--file", file).ok(t)
	clients[1].run("task", "artifact", "get", ids[0], a["id"].(string), "--out", filepath.Join(t.TempDir(), "forbidden")).fails(t, "forbidden", 6)
	for i, id := range ids {
		cancelCLITask(t, s, manager, id)
		clients[i].run("task", "current").fails(t, "unauthenticated", 5)
	}
	after, err = os.ReadFile(normal.SessionFile)
	if err != nil || !bytes.Equal(after, baseline) {
		t.Fatal("task cleanup damaged normal session", err)
	}
	t.Log("4.1: two tasks share Agent configuration but isolate token, runtime, workspace, session and artifact access")
}

func TestTaskCLIAdvancesFixedFlowAndRejectsStaleDispatch(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	_, manager := newTaskManager(t, s, "流转Manager")
	worker := s.createAgent("节点执行者")
	in := taskTextRequest(worker, "!sleep=2s")
	in.Workflow.Steps = append(in.Workflow.Steps, workflow.Step{ID: "next", Title: "下一步", Description: " !sleep=2s", AgentID: uuid.MustParse(worker), InputFields: []workflow.Field{{Name: "previous", Type: "string", Required: true}}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{"previous": {Kind: "node", NodeID: ptr("work"), Field: "result"}}})
	in.Workflow.Edges = []workflow.Edge{{ID: "continue", From: "work", To: "next", Label: "继续"}}
	created := taskCLIInput(t, manager, in, "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	first := currentCLINode(t, waitRunningCLITask(t, s, id))
	client := testTaskClient(t, s, first.RuntimeID.String())
	firstSession := waitCLISessionPath(t, w, first.RuntimeID)
	action := uuid.NewString()
	body := map[string]any{"actionId": action, "output": map[string]any{"result": "first successful result"}, "edgeId": "continue"}
	receipt := taskCLIInput(t, client, body, "task", "submit", id, first.ID.String())
	if receipt["status"] != "submitted" {
		t.Fatal(receipt)
	}
	if manager.run("task", "get", id).ok(t)["task"].(map[string]any)["currentNodeRunId"] != first.ID.String() {
		t.Fatal("advanced before round settled")
	}
	d := waitCLITask(t, s, id, func(d taskmodel.Detail) bool {
		return len(d.Nodes) == 2 && d.Task.CurrentNodeRunID != nil && *d.Task.CurrentNodeRunID != first.ID && s.a.Executor.Process(first.RuntimeID).State == "busy"
	})
	next := currentCLINode(t, d)
	if next.RuntimeID != first.RuntimeID || next.Inputs["previous"] != "first successful result" || d.Nodes[0].Status != "completed" {
		t.Fatal("flow did not reuse runtime and latest successful result")
	}
	nextInst, err := s.a.Store.TaskInstance(context.Background(), next.RuntimeID)
	if err != nil || nextInst.SessionFile == nil || *nextInst.SessionFile != firstSession {
		got := "<nil>"
		if nextInst.SessionFile != nil {
			got = *nextInst.SessionFile
		}
		t.Fatalf("next node replaced task session: expected=%s actual=%s err=%v", firstSession, got, err)
	}
	client.stdin = `{"actionId":"` + uuid.NewString() + `","output":{"result":"late"},"edgeId":"continue"}`
	client.run("task", "submit", id, first.ID.String(), "--from-file", "-").fails(t, "task_conflict", 8)
	// 同一旧动作的安全重放可返回缓存回执，但不能重新派发或改当前节点。
	taskCLIInput(t, client, body, "task", "submit", id, first.ID.String())
	if manager.run("task", "get", id).ok(t)["task"].(map[string]any)["currentNodeRunId"] != next.ID.String() {
		t.Fatal("idempotent replay moved task")
	}
	taskCLIInput(t, client, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{}, "edgeId": nil}, "task", "submit", id, next.ID.String())
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	t.Log("4.1: saved-before-settled, next-node flow, session reuse, successful input mapping, stale action rejection and idempotent replay")
}

func ptr(s string) *string { return &s }

func TestTaskCLISubmittedFailureAndCurrentManagerTakeover(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	_, oldManager := newTaskManager(t, s, "原Manager")
	worker := s.createAgent("异常执行者")
	created := taskCLIInput(t, oldManager, taskTextRequest(worker, "!sleep=2s !text=stream_before_failure !error=controlled_failure"), "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	node := currentCLINode(t, waitRunningCLITask(t, s, id))
	client := testTaskClient(t, s, node.RuntimeID.String())
	taskCLIInput(t, client, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{"result": "saved for inspection"}, "edgeId": nil}, "task", "submit", id, node.ID.String())
	blocked := waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if blocked.Nodes[0].Output["result"] != "saved for inspection" || blocked.Nodes[0].Status != "blocked" || len(blocked.Nodes) != 1 {
		t.Fatal("abnormal round consumed submitted result")
	}
	page := oldManager.run("task", "history", id, node.ID.String()).ok(t)
	found := false
	for _, item := range page["messages"].([]any) {
		m := item.(map[string]any)
		if m["role"] == "assistant" && m["status"] == "failed" {
			found = m["errorCode"] != nil && m["errorMessage"] != nil
		}
	}
	if !found {
		t.Fatal("failed reply lost R3 error fields")
	}
	newID := s.createAgent("接管Manager")
	s.console(200, "PUT", "/projects/"+s.defaultProject()+"/manager", map[string]any{"agentId": newID})
	newManager := testTaskClient(t, s, newID)
	oldManager.run("task", "list").fails(t, "forbidden", 6)
	oldManager.run("task", "agents").fails(t, "forbidden", 6)
	oldCreate, _ := json.Marshal(taskTextRequest(worker, "!sleep=2s"))
	oldManager.stdin = string(oldCreate)
	oldManager.run("task", "create", "--from-file", "-").fails(t, "forbidden", 6)
	oldManager.run("task", "retry", id, "--action-id", uuid.NewString(), "--expected-node-run", node.ID.String(), "--yes").fails(t, "forbidden", 6)
	oldManager.run("task", "cancel", id, "--action-id", uuid.NewString(), "--yes").fails(t, "forbidden", 6)
	newManager.run("task", "list").ok(t)
	newManager.run("task", "get", id).ok(t)
	note := filepath.Join(t.TempDir(), "note")
	os.WriteFile(note, []byte("检查保留结果后继续"), 0o600)
	retried := newManager.run("task", "retry", id, "--action-id", uuid.NewString(), "--expected-node-run", node.ID.String(), "--note-file", note, "--yes").ok(t)
	newNodeID := retried["task"].(map[string]any)["currentNodeRunId"].(string)
	if newNodeID == node.ID.String() {
		t.Fatal("takeover retry reused old node ID")
	}
	running := waitRunningCLITask(t, s, id)
	n := currentCLINode(t, running)
	if n.RuntimeID != node.RuntimeID || n.Attempt != 2 || n.Output != nil {
		t.Fatal("retry lost instance or reused failed output")
	}
	page = newManager.run("task", "history", id, n.ID.String()).ok(t)
	noteSeen := false
	for _, v := range page["messages"].([]any) {
		m := v.(map[string]any)
		if m["role"] == "user" && strings.Contains(m["text"].(string), "检查保留结果后继续") {
			noteSeen = true
		}
	}
	if !noteSeen {
		t.Fatal("retry explanation did not reach original task session")
	}
	cancelCLITask(t, s, newManager, id)
	t.Log("4.1/R3: abnormal round blocks with saved output and precise error; new Manager retries/cancels, old Manager denied")
}
