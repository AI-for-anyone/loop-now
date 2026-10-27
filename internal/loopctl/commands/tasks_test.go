package commands_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

const taskUUID = "0192f1a2-0000-7000-8000-000000000051"
const nodeUUID = "0192f1a2-0000-7000-8000-000000000052"
const actionUUID = "0192f1a2-0000-7000-8000-000000000053"
const artifactUUID = "0192f1a2-0000-7000-8000-000000000054"

func taskWorkflow() map[string]any {
	return map[string]any{"startStepId": "work", "steps": []any{map[string]any{"id": "work", "title": "处理", "description": "完成要求", "agentId": agentUUID, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}}, "edges": []any{}}
}
func taskCreateInput() map[string]any {
	return map[string]any{"actionId": actionUUID, "title": "通用任务", "request": map[string]any{"text": "要求", "precise": json.Number("9007199254740993")}, "workflow": taskWorkflow()}
}
func taskArtifact() map[string]any {
	sum := sha256.Sum256([]byte("abc"))
	return map[string]any{"id": artifactUUID, "taskId": taskUUID, "nodeRunId": nodeUUID, "name": "结果.txt", "contentType": "text/plain", "size": 3, "sha256": hex.EncodeToString(sum[:]), "createdAt": now, "downloadPath": "/tasks/" + taskUUID + "/artifacts/" + artifactUUID}
}
func taskDetail() map[string]any {
	d := map[string]any{
		"task":    map[string]any{"id": taskUUID, "projectId": projectUUID, "title": "通用任务", "status": "running", "currentNodeRunId": nodeUUID, "currentStepId": "work", "currentAgentId": agentUUID, "reason": nil, "createdAt": now, "updatedAt": now, "endedAt": nil, "link": "/p/默认项目/tasks/" + taskUUID, "version": 1},
		"request": map[string]any{}, "workflow": taskWorkflow(), "nodes": []any{map[string]any{"id": nodeUUID, "taskId": taskUUID, "stepId": "work", "visit": 1, "attempt": 1, "agentId": agentUUID, "agentName": "dev", "runtimeId": agentUUID, "status": "submitted", "inputs": map[string]any{}, "inputVersions": []any{}, "output": map[string]any{}, "edgeId": nil, "reason": nil, "question": nil, "runIds": []any{}, "startedAt": now, "endedAt": nil}}, "artifacts": []any{taskArtifact()}, "cleanup": "none",
	}
	d["sleepingInstances"] = []any{}
	for _, n := range d["nodes"].([]any) {
		node := n.(map[string]any)
		node["sleep"], node["sleepRevision"] = nil, 0
	}
	return d
}
func taskSamples() map[string]clitest.Sample {
	ok := clitest.Sample{Status: 200, Body: taskDetail()}
	return map[string]clitest.Sample{
		"GET /tasks/{taskId}/nodes/{nodeRunId}/messages": {Status: 200, Body: map[string]any{"messages": []any{}, "nextCursor": nil}},
		"GET /tasks":        {Status: 200, Body: map[string]any{"tasks": []any{taskDetail()["task"]}, "nextCursor": nil}},
		"GET /tasks/agents": {Status: 200, Body: map[string]any{"agents": []any{map[string]any{"id": agentUUID, "name": "dev", "address": agentAddress, "prompt": "处理要求", "clis": []any{"gh"}, "configVersion": 1}}}},
		"POST /tasks":       {Status: 201, Body: taskDetail()}, "GET /tasks/current": ok, "GET /tasks/{taskId}": ok,
		"POST /tasks/{taskId}/nodes/{nodeRunId}/submit": {Status: 200, Body: taskDetail()["nodes"].([]any)[0]}, "POST /tasks/{taskId}/nodes/{nodeRunId}/fail": {Status: 200, Body: taskDetail()["nodes"].([]any)[0]},
		"POST /tasks/{taskId}/retry": ok, "POST /tasks/{taskId}/cancel": ok,
		"POST /tasks/{taskId}/artifacts":             {Status: 201, Body: taskArtifact()},
		"GET /tasks/{taskId}/artifacts/{artifactId}": {Status: 200, Raw: []byte("abc"), Header: http.Header{"Content-Type": {"application/octet-stream"}, "X-Content-Sha256": {taskArtifact()["sha256"].(string)}}},
	}
}
func jsonFile(t *testing.T, value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, "task.json", string(b))
}
func withTaskSamples(t *testing.T) (*clitest.Stub, *env) {
	s, e := agentStub(t)
	for op, sample := range taskSamples() {
		s.On(op, sample)
	}
	return s, e
}

func TestTaskQueriesAndCreate(t *testing.T) {
	s, e := withTaskSamples(t)
	e.run("task", "list", "--limit", "7", "--cursor", "opaque-cursor").ok(t)
	if lastReq(t, s).Op != "GET /tasks" {
		t.Fatal(lastReq(t, s))
	}
	q, _ := url.ParseQuery(lastReq(t, s).RawQuery)
	if q.Get("cursor") != "opaque-cursor" || q.Get("limit") != "7" {
		t.Fatal(q)
	}
	e.run("task", "agents").ok(t)
	if lastReq(t, s).Op != "GET /tasks/agents" {
		t.Fatal(lastReq(t, s))
	}
	for _, args := range [][]string{{"task", "get", taskUUID}, {"task", "current"}} {
		d := e.run(args...).ok(t)
		if d["task"].(map[string]any)["id"] != taskUUID {
			t.Fatal(d)
		}
	}
	d := e.run("task", "artifact", "list", taskUUID).ok(t)
	if len(d["artifacts"].([]any)) != 1 || len(d) != 1 {
		t.Fatal(d)
	}
	file := jsonFile(t, taskCreateInput())
	e.run("task", "create", "--from-file", file).ok(t)
	if !bytes.Contains(lastReq(t, s).Body, []byte("9007199254740993")) {
		t.Fatal("JSON number lost precision")
	}
	b, _ := os.ReadFile(file)
	e.stdin = string(b)
	e.run("task", "create", "--from-file", "-").ok(t)
	if !bytes.Equal(lastReq(t, s).Body, b) {
		t.Fatal("JSON was rewritten")
	}
}

func TestTaskActionsPreserveContextAndReceipts(t *testing.T) {
	s, e := withTaskSamples(t)
	input := map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil}
	d := e.run("task", "submit", taskUUID, nodeUUID, "--from-file", jsonFile(t, input)).ok(t)
	if d["status"] != "submitted" || d["id"] != nodeUUID {
		t.Fatal(d)
	}
	if lastReq(t, s).Path != "/agent/v1/tasks/"+taskUUID+"/nodes/"+nodeUUID+"/submit" {
		t.Fatal(lastReq(t, s))
	}
	if !reflect.DeepEqual(lastBody(t, s), input) {
		t.Fatal(lastBody(t, s))
	}
	e.stdin = "失败原因\n"
	e.run("task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-").ok(t)
	if lastBody(t, s)["reason"] != "失败原因\n" {
		t.Fatal(lastBody(t, s))
	}
	e.run("task", "retry", taskUUID, "--action-id", actionUUID, "--expected-node-run", nodeUUID, "--yes").ok(t)
	if _, ok := lastBody(t, s)["note"]; ok {
		t.Fatal("optional note sent")
	}
	e.stdin = "沿用原会话继续"
	e.run("task", "retry", taskUUID, "--action-id", actionUUID, "--expected-node-run", nodeUUID, "--note-file", "-", "--yes").ok(t)
	if !reflect.DeepEqual(lastBody(t, s), map[string]any{"actionId": actionUUID, "expectedNodeRunId": nodeUUID, "note": e.stdin}) {
		t.Fatal(lastBody(t, s))
	}
	e.run("task", "cancel", taskUUID, "--action-id", actionUUID, "--yes").ok(t)
	if !reflect.DeepEqual(lastBody(t, s), map[string]any{"actionId": actionUUID}) {
		t.Fatal(lastBody(t, s))
	}
}

func TestTaskLocalErrorsAndExternalDenied(t *testing.T) {
	e := newEnv(t).agent("http://127.0.0.1:1")
	e.block = true
	for _, data := range []string{"null", "[]", "{} {}", "{", "\xff"} {
		e.stdin = data
		e.run("task", "create", "--from-file", "-").fails(t, output.CodeInvalidArgument, 2)
	}
	e.stdin = " \n"
	e.run("task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-").fails(t, output.CodeInvalidArgument, 2)
	e.run("task", "fail", taskUUID, nodeUUID, "--action-id", "invalid", "--reason-file", "-").fails(t, output.CodeInvalidArgument, 2)
	e.run("task", "retry", taskUUID, "--action-id", actionUUID, "--expected-node-run", "bad", "--yes").fails(t, output.CodeInvalidArgument, 2)
	e.run("task", "create", "--from-file", filepath.Join(t.TempDir(), "missing")).fails(t, output.CodeInputUnreadable, 2)
	if e.dialCount() != 0 {
		t.Fatal("local errors dialed")
	}
	x := newEnv(t)
	x.vars[conn.EnvToken] = extToken
	x.vars[conn.EnvURL] = "https://localhost:1"
	x.block = true
	for _, c := range commands.Registry().Commands() {
		if c.Path[0] != "task" {
			continue
		}
		for _, args := range sampleArgv(t, c) {
			x.run(args...).fails(t, output.CodeCommandNotAllowed, 2)
		}
	}
	if x.dialCount() != 0 {
		t.Fatal("external dialed")
	}
}

func TestTaskFormalErrorsAndNoWriteRetry(t *testing.T) {
	s, e := withTaskSamples(t)
	for _, tc := range []struct {
		status, exit int
		code         string
	}{{403, 6, "forbidden"}, {409, 8, "task_conflict"}, {422, 9, "validation_failed"}} {
		s.On("POST /tasks/{taskId}/nodes/{nodeRunId}/submit", clitest.Sample{Status: tc.status, Body: apiError(tc.code, "节点或派发无效")})
		e.run("task", "submit", taskUUID, nodeUUID, "--from-file", jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})).fails(t, tc.code, tc.exit)
	}
	var calls int
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set(conn.HeaderVersion, testVersion)
		w.WriteHeader(503)
		w.Write([]byte(`{"error":{"code":"unavailable","message":"临时失败"}}`))
	}))
	defer h.Close()
	newEnv(t).agent(h.URL).run("task", "cancel", taskUUID, "--action-id", actionUUID, "--yes").fails(t, "unavailable", 4)
	if calls != 1 {
		t.Fatalf("write retried %d times", calls)
	}
}

func TestTaskArtifactTransfer(t *testing.T) {
	s, e := withTaskSamples(t)
	file := writeFile(t, "原文件.txt", "abc")
	d := e.run("task", "artifact", "put", taskUUID, nodeUUID, "--action-id", actionUUID, "--file", file, "--name", "成果 #.txt", "--content-type", "text/plain").ok(t)
	if d["id"] != artifactUUID {
		t.Fatal(d)
	}
	r := lastReq(t, s)
	q, _ := url.ParseQuery(r.RawQuery)
	if q.Get("name") != "成果 #.txt" || q.Get("nodeRunId") != nodeUUID || q.Get("actionId") != actionUUID || q.Get("contentType") != "text/plain" || string(r.Body) != "abc" || r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("%+v", r)
	}
	out := filepath.Join(t.TempDir(), "成果.txt")
	e.run("task", "artifact", "get", taskUUID, artifactUUID, "--out", out).ok(t)
	b, _ := os.ReadFile(out)
	if string(b) != "abc" {
		t.Fatal(string(b))
	}
	s.On("GET /tasks/{taskId}/artifacts/{artifactId}", clitest.Sample{Status: 200, Raw: []byte("bad"), Header: http.Header{"Content-Type": {"application/octet-stream"}, "X-Content-Sha256": {taskArtifact()["sha256"].(string)}}})
	e.run("task", "artifact", "get", taskUUID, artifactUUID, "--out", out).fails(t, output.CodeUnexpectedResponse, 1)
	b, _ = os.ReadFile(out)
	if string(b) != "abc" {
		t.Fatal("damaged original")
	}
	files, _ := os.ReadDir(filepath.Dir(out))
	if len(files) != 1 {
		t.Fatal("temporary file left")
	}
	e.run("task", "artifact", "get", taskUUID, artifactUUID, "--out", filepath.Join(out, "child")).fails(t, output.CodeOutputUnwritable, 2)
}

func TestTaskUploadDefaultMetadata(t *testing.T) {
	s, e := withTaskSamples(t)
	file := writeFile(t, "结果.txt", "abc")
	e.run("task", "artifact", "put", taskUUID, nodeUUID, "--action-id", actionUUID, "--file", file).ok(t)
	q, _ := url.ParseQuery(lastReq(t, s).RawQuery)
	if q.Get("name") != "结果.txt" || q.Get("contentType") != "application/octet-stream" {
		t.Fatal(q)
	}
}

// 流式成果下载不受JSON上限影响，读取中断后重新写临时文件，不拼接两次内容。
func TestTaskArtifactReadRecovery(t *testing.T) {
	data := bytes.Repeat([]byte("成果"), 2048)
	sum := sha256.Sum256(data)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set(conn.HeaderVersion, testVersion)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("X-Content-SHA256", hex.EncodeToString(sum[:]))
		if calls == 1 {
			w.Write(data[:7])
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	e := newEnv(t).agent(srv.URL)
	e.vars[conn.EnvMaxResponse] = "10"
	out := filepath.Join(t.TempDir(), "out")
	r := e.run("task", "artifact", "get", taskUUID, artifactUUID, "--out", out)
	r.ok(t)
	b, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(b, data) || calls != 2 || r.Env.Meta.Attempts != 2 {
		t.Fatalf("download: calls=%d attempts=%d bytes=%d err=%v", calls, r.Env.Meta.Attempts, len(b), err)
	}
}

func TestTaskJSONHelpAndSecrets(t *testing.T) {
	s, e := withTaskSamples(t)
	for _, c := range commands.Registry().Commands() {
		if c.Path[0] != "task" {
			continue
		}
		for _, args := range sampleArgv(t, c) {
			r := e.run(args...)
			r.ok(t)
			if strings.Contains(r.Stdout, tokenSecret) {
				t.Fatal("secret leaked")
			}
		}
	}
	for _, r := range s.Requests() {
		if strings.Contains(r.RawQuery, tokenSecret) || bytes.Contains(r.Body, []byte(tokenSecret)) {
			t.Fatal("secret in business request")
		}
	}
}

func TestTaskHistoryErrorFieldsArePassedThrough(t *testing.T) {
	s, e := withTaskSamples(t)
	message := map[string]any{"id": actionUUID, "runId": nodeUUID, "nodeRunId": nodeUUID, "role": "assistant", "text": "", "origin": "task_node", "status": "failed", "createdAt": now}
	for _, fields := range []map[string]any{nil, {"errorCode": nil, "errorMessage": nil}, {"errorCode": "session_missing", "errorMessage": "会话文件丢失，未创建替代会话"}} {
		body := map[string]any{}
		for k, v := range message {
			body[k] = v
		}
		for k, v := range fields {
			body[k] = v
		}
		s.On("GET /tasks/{taskId}/nodes/{nodeRunId}/messages", clitest.Sample{Status: 200, Body: map[string]any{"messages": []any{body}, "nextCursor": nil}})
		d := e.run("task", "history", taskUUID, nodeUUID, "--limit", "5", "--cursor", "opaque").ok(t)
		if !reflect.DeepEqual(d["messages"].([]any)[0], body) {
			t.Fatal("TaskMessage fields were changed")
		}
		q, _ := url.ParseQuery(lastReq(t, s).RawQuery)
		if q.Get("limit") != "5" || q.Get("cursor") != "opaque" {
			t.Fatal(q)
		}
	}
}

// Existing formal TaskQuestionRef values are read-only references, not a CLI question state machine.
func TestTaskQuestionReferencesPassThrough(t *testing.T) {
	s, e := withTaskSamples(t)
	for _, status := range []string{"", "open", "answered", "expired", "closed"} {
		detail := taskDetail()
		var expected any
		if status != "" {
			expected = map[string]any{"id": actionUUID, "threadId": artifactUUID, "messageId": nodeUUID, "text": "原题文本", "status": status, "canAnswer": status == "open"}
		}
		detail["nodes"].([]any)[0].(map[string]any)["question"] = expected
		for _, query := range []struct {
			op   string
			args []string
		}{
			{"GET /tasks/{taskId}", []string{"task", "get", taskUUID}},
			{"GET /tasks/current", []string{"task", "current"}},
		} {
			s.On(query.op, clitest.Sample{Status: 200, Body: detail})
			before := len(s.Requests())
			got := e.run(query.args...).ok(t)["nodes"].([]any)[0].(map[string]any)["question"]
			if !reflect.DeepEqual(got, expected) || len(s.Requests()) != before+1 || lastReq(t, s).Method != "GET" {
				t.Fatal("question reference was changed or caused extra actions")
			}
		}
	}
}

func TestTaskQuestionSharedContract(t *testing.T) {
	console := clitest.Load(t, "openapi.yaml").Components.Schemas["TaskQuestionRef"]
	agent := clitest.Load(t, "agent-openapi.yaml").Components.Schemas["TaskQuestionRef"]
	if console == nil || agent == nil {
		t.Fatal("missing formal task question reference")
	}
	a, err := json.Marshal(console.Value)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(agent.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("formal task question references differ across APIs")
	}
}
