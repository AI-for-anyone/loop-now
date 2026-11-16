package httpapi_test

import (
	"context"
	"github.com/google/uuid"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"net/http"
	"testing"
	"time"
)

type authControllerStub struct {
	operation modelauth.Operation
	calls     int
	answers   []string
}

func (f *authControllerStub) State(context.Context, uuid.UUID) (modelauth.State, error) {
	f.calls++
	return modelauth.State{Operation: &f.operation}, nil
}
func (f *authControllerStub) Start(_ context.Context, agent uuid.UUID, in modelauth.LoginInput) (modelauth.Operation, error) {
	f.calls++
	f.operation.AgentID = agent
	f.operation.Provider = in.Provider
	f.operation.ConfigVersion = in.ExpectedConfigVersion
	return f.operation, nil
}
func (f *authControllerStub) Get(context.Context, uuid.UUID, uuid.UUID) (modelauth.Operation, error) {
	f.calls++
	return f.operation, nil
}
func (f *authControllerStub) Cancel(context.Context, uuid.UUID, uuid.UUID) (modelauth.Operation, error) {
	f.calls++
	f.operation.Status = "cancelled"
	f.operation.AuthorizationURL = nil
	now := time.Now().UTC()
	f.operation.CompletedAt = &now
	return f.operation, nil
}

func (f *authControllerStub) Input(_ context.Context, _ uuid.UUID, _ uuid.UUID, in cm.LoginInput) (modelauth.State, error) {
	f.calls++
	f.answers = append(f.answers, in.PromptID)
	f.operation.Prompt = nil
	return modelauth.State{Operation: &f.operation}, nil
}

func TestModelAuthConsoleContractAndIdentityBoundary(t *testing.T) {
	e := newEnv(t)
	a := e.platformAgent("认证边界")
	now := time.Now().UTC()
	url := "https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"
	stub := &authControllerStub{operation: modelauth.Operation{ID: uuid.New(), AgentID: a.ID, Provider: "openai-codex", ConfigVersion: 1, Status: "awaiting_authorization", StartedAt: now, ExpiresAt: now.Add(time.Minute), AuthorizationURL: &url}}
	e.api.ModelAuth = stub
	_, secret := e.identity("auth-external")
	external := e.external(secret)
	agent := e.agentCaller(a.ID)
	// OAuth paths deliberately have no Agent schema/route; inspect their 404 directly.
	agent.client = &http.Client{}
	base := "/agents/" + a.ID.String() + "/model-auth"
	routes := []struct {
		method, path string
		body         any
	}{{"GET", base, nil}, {"POST", base + "/operations", map[string]any{"provider": "openai-codex", "expectedConfigVersion": 1}}, {"GET", base + "/operations/" + stub.operation.ID.String(), nil}, {"POST", base + "/operations/" + stub.operation.ID.String() + "/cancel", map[string]any{}},
		{"POST", base + "/operations/" + stub.operation.ID.String() + "/input", map[string]any{"promptId": "1", "value": "device"}}}
	for _, route := range routes {
		external.expectError(403, "forbidden", route.method, route.path, route.body)
		agent.expectError(404, "not_found", route.method, route.path, route.body)
		e.console().expectError(403, "forbidden", route.method, route.path, route.body, "Authorization", "Bearer "+agent.token)
	}
	if stub.calls != 0 {
		t.Fatal("unauthorized identity reached authentication controller")
	}
	state := decode[modelauth.State](t, e.expect(200, "GET", base, nil))
	if state.Operation == nil || state.Operation.AuthorizationURL == nil {
		t.Fatal("active login cannot restore authorization")
	}
	e.expect(200, "POST", routes[1].path, routes[1].body)
	e.expect(200, "GET", routes[2].path, nil)
	// 插件提供方登录的回答：经同一控制台边界，返回 ModelAuthState。
	stub.operation.Provider = "fake-oauth"
	stub.operation.Prompt = &cm.LoginPrompt{ID: "1", Kind: "select", Message: "Pick", Options: []cm.LoginPromptOption{{ID: "device", Label: "Device"}}}
	answered := decode[modelauth.State](t, e.expect(200, "POST", routes[4].path, routes[4].body))
	if answered.Operation == nil || answered.Operation.Prompt != nil || len(stub.answers) != 1 || stub.answers[0] != "1" {
		t.Fatalf("回答：%+v %v", answered.Operation, stub.answers)
	}
	op := decode[modelauth.Operation](t, e.expect(200, "POST", routes[3].path, routes[3].body))
	if op.Status != "cancelled" || op.AuthorizationURL != nil {
		t.Fatal("terminal operation retained authorization URL")
	}
}
func TestModelAuthUnassembledIsExplicit(t *testing.T) {
	e := newEnv(t)
	e.api.ModelAuth = nil
	base := "/agents/" + uuid.NewString() + "/model-auth"
	e.expect(503, "GET", base, nil)
	e.expect(503, "POST", base+"/operations", map[string]any{"provider": "openai-codex", "expectedConfigVersion": 1})
}
