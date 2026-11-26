package e2e_test

import (
	"testing"
)

// Real App + TLS external CLI: an Agent created with a working folder and its own configuration reads back the same,
// update changes and clears them, and the App's own refusals come back as they are. No model turn is dispatched.
func TestAgentSettingsCLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	_, _, token := s.issueToken("", "agent-settings-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	models := ext.run("model", "list", "claude-code").ok(t)["models"].([]any)
	if len(models) == 0 {
		t.Fatal("the App's catalog has no claude-code models")
	}
	model := models[0].(map[string]any)["id"].(string)
	dir := t.TempDir()

	created := ext.run("agent", "create", "--project", "Default project", "--name", "Local dev", "--provider", "claude-code", "--model", model,
		"--working-dir", dir, "--use-local-config", "true", "--permission-mode", "supervised").ok(t)
	id := created["id"].(string)
	got := ext.run("agent", "get", id).ok(t)
	if got["workingDirectory"] != dir || got["useLocalConfig"] != true || got["permissionMode"] != "supervised" {
		t.Fatalf("read back: workingDirectory %v, useLocalConfig %v, permissionMode %v", got["workingDirectory"], got["useLocalConfig"], got["permissionMode"])
	}

	// Update sends only what is given: the folder and mode stay when only the configuration changes.
	upd := ext.run("agent", "update", id, "--use-local-config", "false").ok(t)
	if upd["useLocalConfig"] != false || upd["workingDirectory"] != dir || upd["permissionMode"] != "supervised" {
		t.Fatalf("after --use-local-config false: %v", upd)
	}
	upd = ext.run("agent", "update", id, "--clear-working-dir", "--permission-mode", "full").ok(t)
	if upd["workingDirectory"] != nil || upd["permissionMode"] != "full" {
		t.Fatalf("after --clear-working-dir: %v", upd)
	}

	// The App's checks are passed through: a relative folder, and a service tier on a model that is not Codex.
	ext.run("agent", "update", id, "--working-dir", "relative/dir").fails(t, "validation_failed", 9)
	ext.run("agent", "update", id, "--service-tier", "priority").fails(t, "validation_failed", 9)
}
