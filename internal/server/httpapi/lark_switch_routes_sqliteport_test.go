//go:build sqliteport

// SQLITE-PORT(G11): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 lark_switch_routes_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskLarkSwitchRoutesFailClosedWithoutNativeManager

package httpapi_test

import (
	"context"
	"loop-now/internal/server/tasks"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestTaskLarkSwitchRoutesFailClosedWithoutNativeManager(t *testing.T) {
	safetyResources(t)
	e := newEnv(t)
	e.api.Tasks = &tasks.Service{Store: e.st, Options: tasks.Options{MaxRequest: 1 << 20}}
	base := "/tasks/" + uuid.NewString() + "/lark-account-switches"
	input := map[string]any{"actionId": uuid.NewString(), "expectedTaskVersion": 1, "expectedNodeRunId": uuid.NewString(), "expectedAgentId": uuid.NewString(), "expectedRuntimeId": uuid.NewString(), "expectedLarkBindingRevision": 0, "targetAccountId": uuid.NewString(), "targetVersionId": uuid.NewString(), "expectedAccountRevision": 1}
	input["expectedMembers"] = []any{map[string]any{"agentId": input["expectedAgentId"], "runtimeId": input["expectedRuntimeId"], "expectedBindingRevision": 0}, map[string]any{"agentId": uuid.NewString(), "runtimeId": uuid.NewString(), "expectedBindingRevision": 0}}
	e.console().expectError(503, "internal_error", "POST", base, input)
	e.console().expectError(404, "not_found", "GET", base+"/"+uuid.NewString(), nil)
	for _, key := range []string{"actionId", "expectedTaskVersion", "expectedNodeRunId", "expectedLarkBindingRevision", "expectedMembers", "expectedAccountRevision"} {
		old := input[key]
		input[key] = nil
		e.console().expectError(422, "validation_failed", "POST", base, input, "X-Contract-Invalid-Request", "1")
		input[key] = old
	}
	_, token := e.identity("switch-external")
	external := e.external(token)
	agent := e.agentCaller(e.platformAgent("switch-agent").ID)
	agent.client = &http.Client{}
	for _, r := range []struct {
		method, path string
		body         any
	}{{"POST", base, input}, {"GET", base + "/" + uuid.NewString(), nil}} {
		external.expectError(403, "forbidden", r.method, r.path, r.body)
		agent.expectError(404, "not_found", r.method, r.path, r.body)
		e.console().expectError(403, "forbidden", r.method, r.path, r.body, "Authorization", "Bearer "+agent.token)
	}
	var actions int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_lark_switch_actions`).Scan(&actions); err != nil || actions != 0 {
		t.Fatal("missing native manager or invalid input reserved a switch")
	}
	if len(e.v.Failures()) != 0 {
		t.Fatal("switch route response violated formal contract")
	}
}
