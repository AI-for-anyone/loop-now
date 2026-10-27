package api_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"testing"
)

func TestLarkLoginFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	check := func(name string, v any, want bool) {
		t.Helper()
		err := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (err == nil) != want {
			t.Fatalf("%s valid=%t: %v", name, want, err)
		}
	}
	id, source := uuid.NewString(), uuid.NewString()
	start := map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source, "expectedSourceRevision": float64(1)}
	check("CLIAuthStart", start, true)
	for _, key := range []string{"sourceAccountId", "expectedSourceRevision", "operationId"} {
		old := start[key]
		start[key] = nil
		check("CLIAuthStart", start, false)
		delete(start, key)
		check("CLIAuthStart", start, false)
		start[key] = old
	}
	for _, key := range []string{"name", "appSecret", "scope", "accountId"} {
		start[key] = "forbidden"
		check("CLIAuthStart", start, false)
		delete(start, key)
	}
	check("CLIAuthStart", map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "accountId": source, "expectedRevision": float64(1)}, true)
	now := "2026-10-20T00:00:00Z"
	op := map[string]any{"id": id, "projectId": uuid.NewString(), "cli": "lark-cli", "authType": "lark_user", "targetAccountId": nil, "targetRevision": nil, "name": nil, "status": "pending", "accountId": nil, "startedAt": now, "expiresAt": "2026-10-20T00:05:00Z", "completedAt": nil, "authorizationUrl": nil, "userCode": nil, "errorCode": nil, "errorMessage": nil, "sourceAccountId": source, "sourceVersionId": uuid.NewString(), "sourceRevision": float64(1)}
	check("CLIAuthOperation", op, true)
	op["status"] = "awaiting_authorization"
	op["authorizationUrl"] = "https://accounts.feishu.cn/oauth/v1/authorize?user_code=public"
	check("CLIAuthOperation", op, true) // official userCode is optional, not the GitHub pair.
	op["userCode"] = "official-code"
	check("CLIAuthOperation", op, true)
	op["status"] = "cancelled"
	op["completedAt"] = now
	op["errorCode"] = "auth_cancelled"
	op["errorMessage"] = "已取消"
	check("CLIAuthOperation", op, false)
	op["userCode"] = nil
	op["authorizationUrl"] = nil
	check("CLIAuthOperation", op, true)
	op["device_code"] = "must-not-be-public"
	check("CLIAuthOperation", op, false)
	identity := map[string]any{"brand": "feishu", "appId": "app-fixture", "openId": "ou-fixture", "displayName": "用户", "grantedScopes": nil, "verifiedAt": now}
	check("LarkIdentity", identity, true)
	identity["grantedScopes"] = []any{}
	check("LarkIdentity", identity, true)
	identity["accessToken"] = "forbidden"
	check("LarkIdentity", identity, false)
}
