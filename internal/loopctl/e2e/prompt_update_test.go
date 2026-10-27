package e2e_test

import (
	"encoding/json"
	"strconv"
	"testing"
)

// Real App + TLS external CLI; Prompt updates never dispatch a model turn.
func TestAgentPromptCLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	_, _, token := s.issueToken("", "prompt-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	provider, model := cliKeyProvider(t, ext)
	ext.stdin = "initial"
	created := ext.run("agent", "create", "--project", "默认项目", "--name", "Prompt验证", "--provider", provider, "--model", model, "--prompt-file", "-").ok(t)
	id := created["id"].(string)
	if created["prompt"] != "initial" || created["promptVersion"] != float64(1) {
		t.Fatal("creation did not initialize Prompt without a precondition")
	}
	// Local presence check applies even to an empty Prompt and happens before any API request.
	ext.stdin = ""
	invalid := ext.run("agent", "update", id, "--prompt-file", "-")
	invalid.fails(t, "invalid_argument", 2)
	if invalid.Meta.RequestID != "" {
		t.Fatal("missing Prompt version reached App")
	}
	// The real API enforces the same condition for callers bypassing CLI validation.
	client := credentialTLSClient(t, w)
	for _, body := range []string{`{"prompt":""}`, `{"prompt":"","expectedPromptVersion":null}`, `{"prompt":"","expectedPromptVersion":0}`} {
		result := credentialAppRequest(t, client, s.ext+"/api/v1/agents/"+id, token, "PATCH", body, 422)
		if result["error"].(map[string]any)["code"] != "validation_failed" {
			t.Fatal("App did not enforce Prompt precondition presence")
		}
	}
	ext.stdin = "saved"
	saved := ext.run("agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", "1").ok(t)
	if saved["prompt"] != "saved" || saved["promptVersion"] != float64(2) {
		t.Fatal("matching Prompt version not saved")
	}
	// Unrelated fields retain their old CLI input contract and do not move the Prompt baseline.
	named := ext.run("agent", "update", id, "--name", "Prompt已改名").ok(t)
	if named["promptVersion"] != float64(2) || named["prompt"] != "saved" {
		t.Fatal("name change altered Prompt baseline")
	}
	modeled := ext.run("agent", "update", id, "--provider", provider, "--model", model).ok(t)
	if modeled["promptVersion"] != float64(2) || modeled["prompt"] != "saved" {
		t.Fatal("model-only update required/changed Prompt baseline")
	}
	config := modeled["configVersion"]
	ext.stdin = "saved"
	same := ext.run("agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", "2").ok(t)
	if same["promptVersion"] != float64(2) || same["configVersion"] != config {
		t.Fatal("same-text valid save manufactured versions")
	}
	// Same text and mixed field requests still fail stale CAS; no automatic refresh/write retry.
	for _, args := range [][]string{
		{"agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", "1"},
		{"agent", "update", id, "--name", "must-not-save", "--prompt-file", "-", "--expected-prompt-version", "1"},
	} {
		conflict := ext.run(args...)
		conflict.fails(t, "precondition_failed", 8)
		if conflict.Meta.Attempts != 1 {
			t.Fatal("CLI retried stale Prompt write")
		}
	}
	current := ext.run("agent", "get", id).ok(t)
	if current["name"] != "Prompt已改名" || current["prompt"] != "saved" || current["promptVersion"] != float64(2) {
		t.Fatal("conflicting mixed PATCH partially saved")
	}
	// Clearing is an explicit Prompt write, then another same-empty save remains idempotent.
	ext.stdin = ""
	cleared := ext.run("agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", "2").ok(t)
	if cleared["prompt"] != "" || cleared["promptVersion"] != float64(3) {
		t.Fatal("explicit Prompt clear failed")
	}
	version := strconv.FormatInt(int64(cleared["promptVersion"].(float64)), 10)
	again := ext.run("agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", version).ok(t)
	if again["promptVersion"] != float64(3) {
		t.Fatal("same-empty save moved version")
	}
	// Agent tokens keep their previous lack of configuration authority.
	agent := testTaskClient(t, s, id)
	agent.stdin = "attempt"
	agent.run("agent", "update", id, "--prompt-file", "-", "--expected-prompt-version", "3").fails(t, "command_not_allowed", 2)
	// No new project Prompt write capability is granted to this external token.
	encoded, _ := json.Marshal(map[string]any{"prompt": "unauthorized", "expectedPromptVersion": 1})
	denied := credentialAppRequest(t, client, s.ext+"/api/v1/projects/"+s.defaultProject()+"/prompt", token, "PUT", string(encoded), 403)
	if denied["error"].(map[string]any)["code"] != "forbidden" {
		t.Fatal("external token gained project Prompt permission")
	}
	t.Log("real App Prompt CAS: initial creation, explicit version, empty/same text, unrelated fields, 412 whole-PATCH rejection and unchanged identity permissions")
}
