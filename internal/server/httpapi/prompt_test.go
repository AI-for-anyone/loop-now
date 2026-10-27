package httpapi_test

import (
	"context"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"testing"
)

func TestPromptContractCASAndAccess(t *testing.T) {
	e := newEnv(t)
	a := e.platformAgent("Prompt CAS")
	path := "/agents/" + a.ID.String()
	project := "/projects/" + e.dc.Project.ID.String() + "/prompt"
	current := decode[store.ProjectPrompt](t, e.expect(200, "GET", project, nil))
	if current.Prompt != "" || current.PromptVersion != 1 {
		t.Fatal("migration defaults wrong")
	}
	for _, v := range []any{nil, 0, -1, 1.5, "1"} {
		body := map[string]any{"prompt": "x", "expectedPromptVersion": v}
		e.expect(422, "PUT", project, body, "X-Contract-Invalid-Request", "1")
		e.expect(422, "PATCH", path, body, "X-Contract-Invalid-Request", "1")
	}
	e.expect(422, "PATCH", path, map[string]any{"prompt": "x"}, "X-Contract-Invalid-Request", "1")
	e.expect(422, "PUT", project, map[string]any{"prompt": "x"}, "X-Contract-Invalid-Request", "1")
	saved := decode[store.ProjectPrompt](t, e.expect(200, "PUT", project, map[string]any{"prompt": "公共规则", "expectedPromptVersion": 1}))
	if saved.PromptVersion != 2 {
		t.Fatal("project version did not advance")
	}
	e.expect(412, "PUT", project, map[string]any{"prompt": "公共规则", "expectedPromptVersion": 1})
	same := decode[store.ProjectPrompt](t, e.expect(200, "PUT", project, map[string]any{"prompt": "公共规则", "expectedPromptVersion": 2}))
	if same.PromptVersion != 2 {
		t.Fatal("same text generated version")
	}
	patched := decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"prompt": "职责", "expectedPromptVersion": 1}))
	if patched.PromptVersion != 2 {
		t.Fatal("Agent prompt version absent")
	}
	e.expect(412, "PATCH", path, map[string]any{"name": "不应提交", "prompt": "职责", "expectedPromptVersion": 1})
	actual := decode[apitypes.Agent](t, e.expect(200, "GET", path, nil))
	if actual.Name != a.Name || actual.Prompt != "职责" || actual.ConfigVersion != patched.ConfigVersion {
		t.Fatal("mixed stale PATCH partially committed")
	}
	actual = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"name": "只有改名"}))
	if actual.PromptVersion != 2 {
		t.Fatal("rename changed Prompt version")
	}
	actual = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"prompt": "", "expectedPromptVersion": 2}))
	if actual.PromptVersion != 3 || actual.Prompt != "" {
		t.Fatal("clear did not execute CAS")
	}
	_, secret := e.identity("prompt-external")
	external := e.external(secret)
	agent := e.agentCaller(a.ID)
	for _, method := range []string{"GET", "PUT"} {
		var body any
		if method == "PUT" {
			body = map[string]any{"prompt": "forbidden", "expectedPromptVersion": 2}
		}
		external.expectError(403, "forbidden", method, project, body)
		e.console().expectError(403, "forbidden", method, project, body, "Authorization", "Bearer "+agent.token)
	}
	// External Agent config ability stays available and uses the same CAS.
	external.expect(200, "PATCH", path, map[string]any{"prompt": "外部职责", "expectedPromptVersion": 3})
	e.expect(204, "DELETE", path, nil)
	e.expect(404, "PATCH", path, map[string]any{"prompt": "x", "expectedPromptVersion": 4})
	p, err := e.st.CreateProject(context.Background(), e.dc.Organization.ID, "删除Prompt项目")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	e.expect(404, "GET", "/projects/"+p.ID.String()+"/prompt", nil)
	e.expect(404, "PUT", "/projects/"+p.ID.String()+"/prompt", map[string]any{"prompt": "x", "expectedPromptVersion": 1})
}
