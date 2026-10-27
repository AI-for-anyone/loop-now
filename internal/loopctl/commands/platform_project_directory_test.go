package commands_test

import (
	"net/http"
	"testing"

	"loop-now/internal/loopctl/clitest"
)

func platformDirectorySample() map[string]any {
	return map[string]any{"id": projectUUID, "name": "同名项目", "organization": map[string]any{"id": agentUUID, "name": "组织B"}, "manager": map[string]any{"id": agentUUID, "name": "负责人", "address": "组织B/同名项目/负责人"}}
}

func TestPlatformProjectDirectoryCLI(t *testing.T) {
	s := clitest.NewAgent(t)
	item := platformDirectorySample()
	s.On("GET /projects", clitest.Sample{Status: http.StatusOK, Body: map[string]any{"projects": []any{item}}})
	e := newEnv(t).agent(s.Server.URL)
	if projects := e.run("project", "list").ok(t)["projects"].([]any); len(projects) != 1 || projects[0].(map[string]any)["manager"].(map[string]any)["address"] != "组织B/同名项目/负责人" {
		t.Fatal("safe directory was changed")
	}
	if reqs := s.Requests(); len(reqs) != 1 || reqs[0].Path != "/agent/v1/projects" {
		t.Fatal("Agent list used wrong root")
	}
	s.On("GET /projects", clitest.Sample{Status: http.StatusForbidden, Body: map[string]any{"error": map[string]any{"code": "forbidden", "message": "仅平台 Agent"}}})
	e.run("project", "list").fails(t, "forbidden", 6)
	if len(s.Requests()) != 2 {
		t.Fatal("forbidden request retried or fell back")
	}
	e.block = true
	before := e.dialCount()
	e.run("project", "get", projectUUID).fails(t, "command_not_allowed", 2)
	e.run("project", "model-account", "list", projectUUID).fails(t, "command_not_allowed", 2)
	if before != e.dialCount() {
		t.Fatal("discovery widened management permissions")
	}
	ext := clitest.NewExternal(t)
	ext.On("GET /projects", clitest.Sample{Status: 200, Body: map[string]any{"projects": []any{project()}}})
	if got := newEnv(t).external(ext).run("project", "list").ok(t)["projects"].([]any)[0].(map[string]any); got["agentCount"] != float64(1) || got["isDefault"] != true {
		t.Fatal("external Project DTO changed")
	}
}
