package e2e_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/taskmodel"
)

func TestTaskCLIInboxAddressIsolationAndEndedForward(t *testing.T) {
	w := newWorld(t, false)
	w.cfg.PiMaxProcesses = 4 // Two task runtimes plus ordinary sender/Manager notification processes.
	s := w.start(nil)
	defer s.stop()
	_, manager := newTaskManager(t, s, "原通信Manager")
	worker := s.createAgent("任务收件执行者")
	senderID := s.createAgent("通信发起者")
	sender := testTaskClient(t, s, senderID)
	normal := testTaskClient(t, s, worker)
	ids := []string{}
	nodes := []taskmodel.NodeRun{}
	clients := []*loopctl{}
	for i := 0; i < 2; i++ {
		created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=10s"), "task", "create")
		id := created["task"].(map[string]any)["id"].(string)
		n := currentCLINode(t, waitRunningCLITask(t, s, id))
		ids = append(ids, id)
		nodes = append(nodes, n)
		clients = append(clients, testTaskClient(t, s, n.RuntimeID.String()))
	}
	address := "默认项目/任务收件执行者#" + ids[0]
	sent := sender.run("inbox", "send", address, "--text", "ordinary task message").ok(t)
	messageID := sent["id"].(string)
	if sent["to"].(map[string]any)["address"] != "默认组织/"+address || sent["whenBusy"] != "queue" {
		t.Fatal("task address/default queue not preserved")
	}
	// The active task gets a queued reminder, but ordinary Inbox cannot submit/complete its node.
	current := currentCLINode(t, waitRunningCLITask(t, s, ids[0]))
	if current.ID != nodes[0].ID || current.Output != nil || current.Question != nil {
		t.Fatal("ordinary Inbox changed workflow result or question")
	}
	unread := clients[0].run("inbox", "list", "--unread").ok(t)["messages"].([]any)
	if len(unread) != 1 || unread[0].(map[string]any)["id"] != messageID {
		t.Fatal("task runtime inbox scope incorrect")
	}
	if other := clients[1].run("inbox", "list").ok(t)["messages"].([]any); len(other) != 0 {
		t.Fatal("same Agent's other task saw message")
	}
	clients[1].run("inbox", "read", messageID).fails(t, "not_found", 7)
	normal.run("inbox", "read", messageID).fails(t, "not_found", 7)
	read := clients[0].run("inbox", "read", messageID).ok(t)
	if read["text"] != "ordinary task message" || read["readAt"] == nil {
		t.Fatal("task Inbox read missing content/read state")
	}
	if again := clients[0].run("inbox", "read", messageID).ok(t); again["readAt"] != read["readAt"] {
		t.Fatal("repeat read changed timestamp")
	}
	reply := clients[0].run("inbox", "reply", messageID, "--text", "task reply").ok(t)
	if reply["threadId"] != sent["threadId"] || reply["from"].(map[string]any)["address"] != "默认组织/"+address {
		t.Fatal("task reply lost runtime/thread identity")
	}
	if received := sender.run("inbox", "read", reply["id"].(string)).ok(t); received["text"] != "task reply" {
		t.Fatal("sender did not receive task reply")
	}
	// Interrupt switches the same runtime to the new Inbox turn, without replaying the node.
	interrupt := sender.run("inbox", "send", "默认项目/任务收件执行者#"+ids[1], "--text", "switch to message", "--when-busy", "interrupt").ok(t)
	if interrupt["whenBusy"] != "interrupt" {
		t.Fatal("explicit interrupt lost")
	}
	blocked := waitCLITask(t, s, ids[1], func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if len(blocked.Nodes) != 1 || blocked.Nodes[0].ID != nodes[1].ID || blocked.Nodes[0].Output != nil || blocked.Nodes[0].Question != nil {
		t.Fatal("Inbox interrupt submitted or replaced the workflow node")
	}
	var nodeTurns, interrupted, reminderTurns int
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE origin='task_node'),count(*) FILTER(WHERE origin='task_node' AND status='interrupted'),count(*) FILTER(WHERE origin='inbox_reminder') FROM task_turns WHERE task_id=$1`, uuid.MustParse(ids[1])).Scan(&nodeTurns, &interrupted, &reminderTurns); err != nil {
		t.Fatal(err)
	}
	if nodeTurns != 1 || interrupted != 1 || reminderTurns != 1 {
		t.Fatal("interrupt replayed original execution or lost notification")
	}
	clients[1].run("inbox", "read", interrupt["id"].(string)).ok(t)
	// Both targets end before Manager takeover; old instance tokens are revoked by archive.
	for i, id := range ids {
		cancelCLITask(t, s, manager, id)
		clients[i].run("inbox", "list").fails(t, "unauthenticated", 5)
	}
	newID, newManager := newTaskManager(t, s, "接管通信Manager")
	forwarded := sender.run("inbox", "send", address, "--text", "after task end").ok(t)
	if forwarded["to"].(map[string]any)["id"] != newID || forwarded["to"].(map[string]any)["address"] != "默认组织/默认项目/接管通信Manager" || !strings.Contains(forwarded["text"].(string), "[原收件人："+address+"]") {
		t.Fatal("ended recipient did not forward to current Manager with original address")
	}
	newManager.run("inbox", "read", forwarded["id"].(string)).ok(t)
	// Replying in the old task thread also resolves the ended target to current Manager.
	lateReply := sender.run("inbox", "reply", reply["id"].(string), "--text", "late thread reply").ok(t)
	if lateReply["to"].(map[string]any)["id"] != newID || lateReply["threadId"] == sent["threadId"] || !strings.Contains(lateReply["text"].(string), ids[0]) {
		t.Fatal("old thread reply revived task recipient")
	}
	newManager.run("inbox", "read", lateReply["id"].(string)).ok(t)
	// No current Manager is an explicit conflict; never silently deliver to console.
	s.console(200, "DELETE", "/projects/"+s.defaultProject()+"/manager", nil)
	sender.run("inbox", "send", address, "--text", "no Manager").fails(t, "task_conflict", 8)
	sender.run("inbox", "send", "默认项目/任务收件执行者#"+uuid.NewString(), "--text", "unknown task").fails(t, "not_found", 7)
	s.waitIdle(newID)
	t.Log("ordinary Inbox: task # address, runtime isolation/read/reply, queue/interrupt without replay or implicit submit, ended current-Manager forwarding, late thread forwarding, revoked task tokens and explicit missing-Manager conflict")
}

func TestTaskCLICompletedInboxRecipientForwards(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	_, manager := newTaskManager(t, s, "完成任务Manager")
	worker := s.createAgent("完成收件执行者")
	sender := testTaskClient(t, s, s.createAgent("完成后发件者"))
	created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=2s"), "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	node := currentCLINode(t, waitRunningCLITask(t, s, id))
	client := testTaskClient(t, s, node.RuntimeID.String())
	taskCLIInput(t, client, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{"result": "done"}, "edgeId": nil}, "task", "submit", id, node.ID.String())
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	forwarded := sender.run("inbox", "send", "默认项目/完成收件执行者#"+id, "--text", "post completion").ok(t)
	if forwarded["to"].(map[string]any)["address"] != "默认组织/默认项目/完成任务Manager" || !strings.Contains(forwarded["text"].(string), id) {
		t.Fatal("completed recipient did not forward with original target")
	}
	manager.run("inbox", "read", forwarded["id"].(string)).ok(t)
	if current := manager.run("task", "get", id).ok(t)["task"].(map[string]any)["status"]; current != "completed" {
		t.Fatal("post-completion message revived task")
	}
	client.run("inbox", "list").fails(t, "unauthenticated", 5)
	t.Log("completed task address forwards ordinary Inbox to Manager after archive without revival")
}
