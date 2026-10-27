package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

func taskEnv(t *testing.T) (*env, caller, taskmodel.Detail) {
	t.Helper()
	e := newEnv(t)
	e.api.Tasks = tasks.New(e.st, e.api.Config, nil, tasks.Options{MaxSteps: 100, MaxEdges: 200, MaxVisits: 1000, MaxRequest: 1 << 20, MaxArtifact: 1 << 20, ReconcileInterval: time.Second}, e.api.Log)
	e.api.Inbox.Tasks = e.api.Tasks
	a := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Manager", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/manager", map[string]any{"agentId": a.ID})
	manager := e.agentCaller(a.ID)
	body := map[string]any{"actionId": uuid.New(), "title": "通用任务", "request": map[string]any{"prompt": "保持原始要求"}, "workflow": map[string]any{"startStepId": "work", "steps": []any{map[string]any{"id": "work", "title": "处理", "description": "执行", "agentId": a.ID, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}}, "edges": []any{}}}
	status, raw, header := manager.do("POST", "/tasks", body)
	if status != 201 {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	d := decode[taskmodel.Detail](t, raw)
	record, err := e.st.GetTask(context.Background(), d.Task.ID)
	if err != nil || record.CreationRequestID == nil || record.CreationRequestID.String() != header.Get("Loop-Now-Request-Id") {
		t.Fatal("task creation lost HTTP request ID", err)
	}
	if d.Task.Status != "preparing" || len(d.Nodes) != 1 {
		t.Fatal("invalid startup detail")
	}
	again := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", body))
	if again.Task.ID != d.Task.ID {
		t.Fatal("create replay duplicated task")
	}
	return e, manager, d
}
func TestTaskHTTPPermissionsMessagesArtifactAndCancel(t *testing.T) {
	e, manager, d := taskEnv(t)
	path := "/tasks/" + d.Task.ID.String()
	node := d.Nodes[0]
	manager.expect(200, "GET", path, nil)
	e.expect(200, "GET", path, nil)
	instance := e.agentCaller(node.RuntimeID)
	who := decode[apitypes.Whoami](t, instance.expect(200, "GET", "/whoami", nil))
	if who.TaskID == nil || *who.TaskID != d.Task.ID || who.RuntimeID == nil || *who.RuntimeID != node.RuntimeID {
		t.Fatal("instance token lost scope")
	}
	instance.expect(200, "GET", "/tasks/current", nil)
	manager.expectError(403, "forbidden", "POST", path+"/nodes/"+node.ID.String()+"/submit", map[string]any{"actionId": uuid.New(), "output": map[string]any{}, "edgeId": nil})
	a := e.platformAgent("其他Agent")
	other := e.agentCaller(a.ID)
	other.expectError(403, "forbidden", "GET", path, nil)
	available := decode[taskmodel.AgentList](t, manager.expect(200, "GET", "/tasks/agents", nil))
	if len(available.Agents) != 1 || available.Agents[0].ID != node.AgentID {
		t.Fatal("Manager directory leaked other scopes")
	}
	other.expectError(403, "forbidden", "GET", "/tasks/agents", nil)
	managerPage := decode[taskmodel.Page](t, manager.expect(200, "GET", "/tasks?limit=1", nil))
	if len(managerPage.Tasks) != 1 || managerPage.Tasks[0].ID != d.Task.ID {
		t.Fatal("Manager task list scope incorrect")
	}
	other.expectError(403, "forbidden", "GET", "/tasks", nil)
	_, secret := e.identity("task-external")
	external := e.external(secret)
	external.expectError(403, "forbidden", "GET", path, nil)
	page := decode[taskmodel.Page](t, e.expect(200, "GET", "/projects/"+e.dc.Project.ID.String()+"/tasks", nil))
	if len(page.Tasks) != 1 {
		t.Fatal("task listing missing")
	}
	e.expect(200, "GET", path+"/nodes/"+node.ID.String(), nil)
	messages := decode[taskmodel.MessagePage](t, instance.expect(200, "GET", path+"/nodes/"+node.ID.String()+"/messages", nil))
	if len(messages.Messages) != 0 {
		t.Fatal("unexpected node messages")
	}
	// 二进制发布经同一契约校验；成果ID属于任务而非执行目录。
	url := path + "/artifacts?actionId=" + uuid.NewString() + "&nodeRunId=" + node.ID.String() + "&name=result.txt&contentType=text%2Fplain"
	artifact := decode[taskmodel.Artifact](t, instance.expect(201, "POST", url, []byte("retained")))
	e.expect(200, "GET", path+"/artifacts/"+artifact.ID.String(), nil)
	instance.expect(200, "POST", path+"/nodes/"+node.ID.String()+"/submit", map[string]any{"actionId": uuid.New(), "output": map[string]any{}, "edgeId": nil})
	e.expect(200, "POST", path+"/cancel", map[string]any{"actionId": uuid.New()})
	instance.expectError(409, "task_conflict", "POST", path+"/nodes/"+node.ID.String()+"/submit", map[string]any{"actionId": uuid.New(), "output": map[string]any{}, "edgeId": nil})
	// 任务实例Inbox与常态隔离，即使同一个Agent配置也不能代读。
	normalMessage := sendInbox(e.console(), "默认项目/Manager", "常态")
	taskMessage := sendInbox(e.console(), "默认项目/Manager#"+d.Task.ID.String(), "已结束转Manager")
	if taskMessage.To.Address == nil || *taskMessage.To.Address != "默认组织/默认项目/Manager" {
		t.Fatal("ended task did not forward manager")
	}
	instance.expectError(404, "not_found", "POST", "/inbox/messages/"+normalMessage.ID.String()+"/read", nil)
}
func TestTaskSSECheckpointAndResume(t *testing.T) {
	e, _, d := taskEnv(t)
	path := "/tasks/" + d.Task.ID.String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/v1"+path+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line := make([]byte, 4096)
	n, err := resp.Body.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(line[:n], []byte("event: snapshot")) {
		t.Fatal("snapshot missing", string(line[:n]))
	}
	var position int64
	_, err = fmt.Sscanf(string(line[:n]), "id: %d", &position)
	if err != nil {
		t.Fatal(err)
	}
	e.expect(200, "POST", path+"/cancel", map[string]any{"actionId": uuid.New()})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	req2, _ := http.NewRequestWithContext(ctx2, "GET", e.srv.URL+"/api/v1"+path+"/events", nil)
	req2.Header.Set("Last-Event-ID", fmt.Sprint(position))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	n, err = resp2.Body.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(line[:n], []byte("event: snapshot")) || !bytes.Contains(line[:n], []byte("event: task.updated")) {
		t.Fatal("resume duplicated snapshot or lost update", string(line[:n]))
	}
}

func TestTaskSnapshotKeepsOldCredentialsAfterConfigRemoval(t *testing.T) {
	e, manager, d := taskEnv(t)
	ctx := context.Background()
	agent := d.Nodes[0].AgentID
	if _, err := e.api.Config.AddCLI(ctx, agent, "gh"); err != nil {
		t.Fatal(err)
	}
	e.sharedCredential(t, "cli", "gh", "任务共享CLI", "fixture-cli-before")
	modelAccount := e.sharedCredential(t, "model", "anthropic", "任务共享模型", "fixture-model-before")
	frozen := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "冻结配置", "request": map[string]any{}, "workflow": d.Workflow}))
	if err := e.api.Config.RemoveCLI(ctx, agent, "gh"); err != nil {
		t.Fatal(err)
	}
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/credential-accounts/"+modelAccount.ID.String()+"/credential", map[string]any{"expectedRevision": modelAccount.Revision, "credential": map[string]any{"type": "api_key", "key": "fixture-model-after"}})
	src := &tasks.Source{Normal: e.api.Config, Store: e.st, ModelAuth: e.api.Config.ModelAuth.Coordinator, WorkRoot: t.TempDir(), MaxSessionBytes: 4096, PauseNormal: func(context.Context, uuid.UUID) (func(), error) { return func() {}, nil }, FreezeNormal: func(context.Context, uuid.UUID) error { return nil }}
	instances, err := e.st.TaskInstances(ctx, frozen.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = src.Prepare(ctx, instances[0]); err != nil {
		t.Fatal(err)
	}
	snap, err := src.ExecutionSnapshot(ctx, instances[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.CLIs) != 1 || snap.CLIs[0] != "gh" || snap.CLIPayloads["gh"].Env["GH_TOKEN"] != "fixture-cli-before" || !credentialHasKey(t, snap.ModelCredential, "fixture-model-before") {
		t.Fatal("task credentials followed new config")
	}
	normal, err := src.ExecutionSnapshot(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(normal.CLIs) != 0 || !credentialHasKey(t, normal.ModelCredential, "fixture-model-after") {
		t.Fatal("normal did not follow new config")
	}
}

func TestTaskInboxIsSeparateFromNormalAndOtherTask(t *testing.T) {
	e, manager, d := taskEnv(t)
	node := d.Nodes[0]
	instance := e.agentCaller(node.RuntimeID)
	second := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "另一个任务", "request": map[string]any{}, "workflow": d.Workflow}))
	other := e.agentCaller(second.Nodes[0].RuntimeID)
	instance.expectError(404, "not_found", "GET", "/tasks/"+d.Task.ID.String()+"/nodes/"+second.Nodes[0].ID.String()+"/messages", nil)
	msg := sendInbox(e.console(), "默认项目/Manager#"+d.Task.ID.String(), "任务专属消息")
	if msg.To.Address == nil || *msg.To.Address != "默认组织/默认项目/Manager#"+d.Task.ID.String() {
		t.Fatal("task recipient lost address")
	}
	manager.expectError(404, "not_found", "POST", "/inbox/messages/"+msg.ID.String()+"/read", nil)
	other.expectError(404, "not_found", "POST", "/inbox/messages/"+msg.ID.String()+"/read", nil)
	instance.expect(200, "POST", "/inbox/messages/"+msg.ID.String()+"/read", nil)
	var turns int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE task_id=$1`, d.Task.ID).Scan(&turns); err != nil || turns != 0 {
		t.Fatal("preparing task dispatched premature Inbox turn", err, turns)
	}
}

// 重连接积压与首轮poll前的新Redis中继并存时，不能让新ID越过PG历史。
func TestTaskSSEResumeBacklogBeforeNewRelay(t *testing.T) {
	cases := []struct {
		name                string
		backlog             int
		project, clearRedis bool
	}{{"task", 9, false, false}, {"beyond_one_batch", 115, false, false}, {"redis_cleared", 115, false, true}, {"project_cross_task", 115, true, false}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			e, manager, first := taskEnv(t)
			e.api.O.TaskEventPoll = 500 * time.Millisecond
			ctx := context.Background()
			checkpoint, err := e.st.TaskEventPosition(ctx, &first.Task.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			ids := []uuid.UUID{first.Task.ID}
			if test.project {
				second := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "另一个任务", "request": map[string]any{}, "workflow": first.Workflow}))
				ids = append(ids, second.Task.ID)
			}
			mutate := func(id uuid.UUID, n int) {
				_, err := e.st.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
					m.Record.View.Task.Title = fmt.Sprintf("事件%d", n)
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < test.backlog; i++ {
				mutate(ids[i%len(ids)], i)
			}
			var taskID, projectID *uuid.UUID
			key, path := coord.TaskStream(first.Task.ID.String()), "/tasks/"+first.Task.ID.String()+"/events"
			if test.project {
				projectID = &first.Task.ProjectID
				key = coord.ProjectTaskStream(projectID.String())
				path = "/projects/" + projectID.String() + "/tasks/events"
			} else {
				taskID = &first.Task.ID
			}
			backlog, err := e.st.TaskEvents(ctx, taskID, projectID, checkpoint, 1000)
			if err != nil || len(backlog) < test.backlog {
				t.Fatal("fixture events missing", err)
			}
			relay := func(event store.TaskEvent) {
				payload, _ := json.Marshal(tasks.StreamEvent{ID: event.ID, Data: event.Data})
				if _, err := e.co.AppendEvent(ctx, key, event.Type, payload); err != nil {
					t.Fatal(err)
				}
			}
			relay(backlog[len(backlog)-1])
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(requestCtx, "GET", e.srv.URL+"/api/v1"+path, nil)
			request.Header.Set("Last-Event-ID", fmt.Sprint(checkpoint))
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("SSE connection failed", response.StatusCode)
			}
			// Headers已flush，但首次500ms poll尚未发生。此时插入新事件并中继。
			mutate(ids[len(ids)-1], 1000)
			expected, err := e.st.TaskEvents(ctx, taskID, projectID, checkpoint, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if test.clearRedis {
				if err = e.rdb.Del(ctx, e.co.Keys().Events(key)).Err(); err != nil {
					t.Fatal(err)
				}
			}
			relay(expected[len(expected)-1])
			reader := bufio.NewReader(response.Body)
			received := []int64{}
			for len(received) < len(expected) {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal("SSE lost backlog", err, len(received), len(expected))
				}
				if strings.HasPrefix(line, "id: ") {
					id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					received = append(received, id)
				}
			}
			for i, event := range expected {
				if received[i] != event.ID {
					t.Fatal("SSE skipped/reordered persisted event", i, received[i], event.ID)
				}
			}
		})
	}
}

func TestTaskFailedMessageProjection(t *testing.T) {
	for _, code := range []string{"session_missing", "pi_error", "pi_exited"} {
		t.Run(code, func(t *testing.T) {
			e, manager, d := taskEnv(t)
			ctx := context.Background()
			node := d.Nodes[0]
			_, err := e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
				m.Record.View.Task.Status = "running"
				_, err := e.st.EnqueueTaskTurn(ctx, m, "work", "task_node", 1, nil)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			v, ok, err := e.st.ClaimTaskTurn(ctx, d.Task.ID, node.RuntimeID)
			if err != nil || !ok {
				t.Fatal(err)
			}
			raw := "provider failed with FAKE_MODEL_KEY=do-not-project and /private/credential-file"
			if _, err = e.st.UpdateTaskTurn(ctx, d.Task.ID, v.ID, "failed", "partial", &code, &raw); err != nil {
				t.Fatal(err)
			}
			page := decode[taskmodel.MessagePage](t, manager.expect(200, "GET", "/tasks/"+d.Task.ID.String()+"/nodes/"+node.ID.String()+"/messages", nil))
			if len(page.Messages) != 2 {
				t.Fatal("missing corresponding reply")
			}
			for _, m := range page.Messages {
				if m.Role == "assistant" {
					if m.RunID != v.ID || m.NodeRunID != node.ID || m.ErrorCode == nil || *m.ErrorCode != code || m.ErrorMessage == nil || strings.Contains(*m.ErrorMessage, "do-not-project") || strings.Contains(*m.ErrorMessage, "/private/") {
						t.Fatalf("unsafe or unattributed failure %+v", m)
					}
				} else if m.ErrorCode != nil || m.ErrorMessage != nil {
					t.Fatal("user message acquired reply failure")
				}
			}
			events, err := e.st.TaskEvents(ctx, &d.Task.ID, nil, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Type == "message.created" && strings.Contains(string(event.Data), "do-not-project") {
					t.Fatal("secret in persistent SSE event")
				}
			}
		})
	}
}

func TestTaskRequestIDSurvivesAsyncDispatch(t *testing.T) {
	e, _, d := taskEnv(t)
	ctx := context.Background()
	record, err := e.st.GetTask(ctx, d.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "running"
		_, err := e.st.EnqueueTaskTurn(ctx, m, "async first node", "task_node", 1, nil)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := e.st.ClaimTaskTurn(ctx, d.Task.ID, d.Nodes[0].RuntimeID)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	for _, kind := range []string{store.EventRunQueued, store.EventRunDispatched} {
		var id string
		if err = e.st.Pool.QueryRow(ctx, `SELECT data->>'requestId' FROM lifecycle_events WHERE task_turn_id=$1 AND type=$2`, v.ID, kind).Scan(&id); err != nil || id != record.CreationRequestID.String() {
			t.Fatal("async reply lost request correlation", kind, id, err)
		}
	}
}

func credentialHasKey(t *testing.T, c modelcredential.Credential, key string) bool {
	t.Helper()
	b, e := c.AuthFile("anthropic")
	defer clear(b)
	return e == nil && bytes.Contains(b, []byte(key))
}
