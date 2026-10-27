package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
)

// Static management only: freshly generated synthetic key, TEST schema and TLS
// App. No gcloud invocation, OAuth helper, model turn or remote token request.
func TestGoogleSACLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	t.Logf("SA CLI resources: schema=%s redisPrefix=%s consoleAddr=%s tlsAddr=%s", w.schema, w.cfg.RedisKeyPrefix, w.addr, w.extAddr)
	_, _, token := s.issueToken("", "google-sa-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	project := s.defaultProject()
	provider, model := cliKeyProvider(t, ext)
	agent := ext.run("agent", "create", "--project", project, "--name", "SA CLI", "--provider", provider, "--model", model).ok(t)["id"].(string)
	version := func() string { return fmt.Sprintf("%.0f", ext.run("agent", "get", agent).ok(t)["configVersion"]) }
	selection := func() map[string]any {
		for _, item := range ext.run("agent", "credential-selection", "get", agent).ok(t)["selections"].([]any) {
			v := item.(map[string]any)
			if v["purpose"] == "cli" && v["cli"] == "gcloud" {
				return v
			}
		}
		t.Fatal("gcloud selection missing")
		return nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	credential := func(target, endpoint string) string {
		sa, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-sa-owner", "private_key_id": "synthetic-key-id", "private_key": keyPEM, "client_email": "synthetic@fixture.iam.gserviceaccount.com", "client_id": "1234567890", "token_uri": endpoint})
		fields := map[string]string{"serviceAccountJson": string(sa)}
		if target != "" {
			fields["defaultProject"] = target
		}
		b, _ := json.Marshal(map[string]any{"type": "cli", "fields": fields})
		return string(b)
	}
	create := func(target string) map[string]any {
		ext.stdin = credential(target, "https://oauth2.googleapis.com/token")
		r := ext.run("project", "cli-account", "create", project, "--cli", "gcloud", "--name", "同名SA", "--from-file", "-")
		a := r.ok(t)
		if a["authMethod"] != "service_account" || a["purpose"] != "cli" || a["cli"] != "gcloud" || r.Meta.RequestID == "" || strings.Contains(r.Stdout, "PRIVATE KEY") {
			t.Fatal("unsafe or wrong actual SA metadata")
		}
		return a
	}
	a, b := create(""), create("synthetic-target")
	idA, idB := a["id"].(string), b["id"].(string)
	if idA == idB {
		t.Fatal("same label merged account identities")
	}
	accounts := ext.run("project", "cli-account", "list", project, "--cli", "gcloud").ok(t)["accounts"].([]any)
	if len(accounts) != 2 {
		t.Fatal("gcloud account filter lost accounts")
	}
	if len(ext.run("project", "model-account", "list", project, "--provider", provider).ok(t)["accounts"].([]any)) != 0 {
		t.Fatal("CLI accounts leaked into model purpose")
	}
	ext.run("agent", "credential-selection", "set", agent, "--cli", "gcloud", "--account-id", idA, "--expected-config-version", version()).fails(t, "validation_failed", 9)
	ext.run("agent", "cli", "add", agent, "gcloud").ok(t)
	if selection()["status"] != "selection_required" {
		t.Fatal("two SA candidates silently picked first")
	}
	ext.run("agent", "credential-selection", "set", agent, "--cli", "gcloud", "--account-id", idA, "--expected-config-version", version()).ok(t)
	if v := selection(); v["resolvedAccountId"] != idA || v["mode"] != "explicit" {
		t.Fatal("SA single selection not explicit")
	}
	impact := ext.run("project", "cli-account", "impact", project, "--cli", "gcloud", "--account-id", idA).ok(t)["agents"].([]any)
	if len(impact) != 1 || impact[0].(map[string]any)["mode"] != "explicit" {
		t.Fatal("SA impact should remain single explicit")
	}
	// A disallowed endpoint must fail local server validation, without network.
	ext.stdin = credential("", "https://fixture.invalid/token")
	ext.run("project", "cli-account", "replace", project, idA, "--expected-revision", "1", "--from-file", "-", "--yes").fails(t, "validation_failed", 9)
	if got := ext.run("project", "cli-account", "get", project, idA).ok(t); got["currentVersionId"] != a["currentVersionId"] || got["revision"] != a["revision"] {
		t.Fatal("invalid SA replacement mutated prior account")
	}
	ext.stdin = credential("synthetic-new-target", "https://oauth2.googleapis.com/token")
	replaced := ext.run("project", "cli-account", "replace", project, idA, "--expected-revision", "1", "--from-file", "-", "--yes").ok(t)
	if replaced["currentVersionId"] == a["currentVersionId"] || replaced["authMethod"] != "service_account" {
		t.Fatal("SA replace did not create distinct static version")
	}
	ext.run("project", "cli-account", "rename", project, idA, "--name", "stale", "--expected-revision", "1").fails(t, "precondition_failed", 8)
	renamed := ext.run("project", "cli-account", "rename", project, idA, "--name", "新SA标签", "--expected-revision", fmt.Sprintf("%.0f", replaced["revision"])).ok(t)
	if renamed["currentVersionId"] != replaced["currentVersionId"] {
		t.Fatal("rename changed SA credential version")
	}
	deleted := ext.run("project", "cli-account", "delete", project, idA, "--expected-revision", fmt.Sprintf("%.0f", renamed["revision"]), "--yes").ok(t)
	if deleted["status"] != "deleted" || selection()["status"] != "account_deleted" {
		t.Fatal("SA deleted explicit account fell back")
	}
	for _, v := range []any{a["currentVersionId"], replaced["currentVersionId"]} {
		var exists bool
		if err := s.a.Store.Pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM project_cli_versions WHERE id=$1)", v).Scan(&exists); err != nil || !exists {
			t.Fatal("SA delete lost historical version")
		}
	}
	ext.run("agent", "credential-selection", "clear", agent, "--cli", "gcloud", "--expected-config-version", version(), "--yes").ok(t)
	if v := selection(); v["mode"] != "automatic" || v["resolvedAccountId"] != idB {
		t.Fatal("SA single null did not restore unique automatic")
	}
	if len(ext.run("project", "cli-account", "list", project, "--cli", "gcloud").ok(t)["accounts"].([]any)) != 1 {
		t.Fatal("SA list did not exclude tombstone")
	}
	internal := testTaskClient(t, s, agent)
	r := internal.run("project", "cli-account", "login-list", project)
	r.fails(t, "command_not_allowed", 2)
	if r.Meta.RequestID != "" {
		t.Fatal("Agent management refusal dialed App")
	}
	t.Log("actual SA static CRUD/filter/CAS/metadata/history/impact/single choice passed; no tool/helper/model/native auth or remote request")
}
