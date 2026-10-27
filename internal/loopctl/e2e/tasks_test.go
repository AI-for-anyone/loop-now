package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

// testTaskClient签发隔离测试schema中的手动测试令牌。不是Pi自主执行CLI的证据。
func testTaskClient(t *testing.T, s *server, agentOrRuntime string) *loopctl {
	t.Helper()
	tok, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.InsertAgentToken(context.Background(), tok.ID, uuid.MustParse(agentOrRuntime), s.a.InstanceID, tok.Hash); err != nil {
		t.Fatal(err)
	}
	return newLoopctl(t, map[string]string{"LOOPCTL_AGENT_URL": "http://" + s.a.Addr() + "/agent/v1", "LOOPCTL_AGENT_TOKEN": tok.Plain})
}

func taskCLIInput(t *testing.T, l *loopctl, input any, args ...string) map[string]any {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	l.stdin = string(b)
	return l.run(append(args, "--from-file", "-")...).ok(t)
}

func waitCLITask(t *testing.T, s *server, id string, fn func(taskmodel.Detail) bool) taskmodel.Detail {
	t.Helper()
	for until := time.Now().Add(20 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		r, err := s.a.Store.GetTask(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		if fn(r.View) {
			return r.View
		}
		if r.View.Task.Status == "blocked" {
			reason := ""
			if r.View.Task.Reason != nil {
				reason = *r.View.Task.Reason
			}
			t.Fatalf("task blocked while awaiting execution: reason=%s", reason)
		}
	}
	t.Fatal("task condition timeout")
	return taskmodel.Detail{}
}

func taskCLIRequest(agent, description string) taskmodel.CreateInput {
	// 留空格让fakepi按独立token识别!sleep，不与“节点说明：”连成一个token。
	return taskmodel.CreateInput{ActionID: uuid.New(), Title: "CLI通用任务", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "执行", Description: " " + description, AgentID: uuid.MustParse(agent), InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "file", Type: "artifact", Required: true}}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}}
}

func TestTaskCLIWithFakePi(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	agent := s.createAgent("任务Manager")
	s.console(200, "PUT", "/projects/"+s.defaultProject()+"/manager", map[string]any{"agentId": agent})
	manager := testTaskClient(t, s, agent)
	manager.run("task", "agents").ok(t)
	input := taskCLIRequest(agent, "!sleep=2s")
	created := taskCLIInput(t, manager, input, "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	// 同一action/body重发返回同一任务，不额外创建。
	replayed := taskCLIInput(t, manager, input, "task", "create")
	if replayed["task"].(map[string]any)["id"] != id {
		t.Fatal("duplicate creation")
	}
	page := manager.run("task", "list", "--limit", "1").ok(t)
	if len(page["tasks"].([]any)) != 1 {
		t.Fatal(page)
	}
	running := waitCLITask(t, s, id, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	n := running.Nodes[0]
	node := testTaskClient(t, s, n.RuntimeID.String())
	current := node.run("task", "current").ok(t)
	if current["task"].(map[string]any)["id"] != id {
		t.Fatal(current)
	}
	file := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(file, []byte("retained CLI artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := node.run("task", "artifact", "put", id, n.ID.String(), "--action-id", uuid.NewString(), "--file", file).ok(t)
	submitted := taskCLIInput(t, node, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{"file": map[string]any{"artifactId": artifact["id"]}}, "edgeId": nil}, "task", "submit", id, n.ID.String())
	if submitted["status"] != "submitted" {
		t.Fatal(submitted)
	}
	before := manager.run("task", "get", id).ok(t)
	if before["task"].(map[string]any)["status"] == "completed" {
		t.Fatal("advanced before fakepi round settled")
	}
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	manager.run("task", "artifact", "list", id).ok(t)
	out := filepath.Join(t.TempDir(), "download.txt")
	manager.run("task", "artifact", "get", id, artifact["id"].(string), "--out", out).ok(t)
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "retained CLI artifact" {
		t.Fatal("artifact did not survive directory cleanup", err)
	}
	node.run("task", "current").fails(t, "unauthenticated", 5)
}

func TestTaskCLIBlockRetryCancel(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	agent := s.createAgent("重试Manager")
	s.console(200, "PUT", "/projects/"+s.defaultProject()+"/manager", map[string]any{"agentId": agent})
	manager := testTaskClient(t, s, agent)
	created := taskCLIInput(t, manager, taskCLIRequest(agent, "!sleep=5s"), "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	running := waitCLITask(t, s, id, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	n := running.Nodes[0]
	node := testTaskClient(t, s, n.RuntimeID.String())
	node.stdin = "需要重试"
	node.run("task", "fail", id, n.ID.String(), "--action-id", uuid.NewString(), "--reason-file", "-").ok(t)
	note := filepath.Join(t.TempDir(), "note")
	os.WriteFile(note, []byte("继续原会话"), 0o600)
	d := manager.run("task", "retry", id, "--action-id", uuid.NewString(), "--expected-node-run", n.ID.String(), "--note-file", note, "--yes").ok(t)
	nextID := d["task"].(map[string]any)["currentNodeRunId"].(string)
	if nextID == n.ID.String() {
		t.Fatal("retry reused node run ID")
	}
	// 旧节点的新提交必须拒绝，不能偷偷作用于刚重试的新节点。
	node.stdin = `{"actionId":"` + uuid.NewString() + `","output":{},"edgeId":null}`
	r := node.run("task", "submit", id, n.ID.String(), "--from-file", "-")
	if r.Code != 8 && r.Code != 5 {
		t.Fatalf("old node accepted: %s", r.Stdout)
	}
	manager.run("task", "cancel", id, "--action-id", uuid.NewString(), "--yes").ok(t)
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
}
