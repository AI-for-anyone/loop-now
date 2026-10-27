package e2e_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// This matrix starts only a dedicated TEST App/schema/Redis prefix and TLS ports.
// It never sends a model turn or starts a real OAuth helper. Run after the server
// owner declares project credential handlers and migrations stable.
func TestProjectCredentialCLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	t.Logf("isolated resources: schema=%s redisPrefix=%s consoleAddr=%s tlsAddr=%s", w.schema, w.cfg.RedisKeyPrefix, w.addr, w.extAddr)
	_, _, token := s.issueToken("", "project-credential-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	project := s.defaultProject()
	provider, model := cliKeyProvider(t, ext)
	agent := ext.run("agent", "create", "--project", project, "--name", "共享凭证 CLI 验证", "--provider", provider, "--model", model).ok(t)["id"].(string)
	selection := func() map[string]any { return ext.run("agent", "credential-selection", "get", agent).ok(t) }
	version := func() string { return fmt.Sprintf("%.0f", selection()["configVersion"]) }
	modelSelection := func() map[string]any {
		for _, v := range selection()["selections"].([]any) {
			item := v.(map[string]any)
			if item["purpose"] == "model" && item["provider"] == provider {
				return item
			}
		}
		t.Fatal("model selection missing")
		return nil
	}
	create := func(key string) map[string]any {
		b, _ := json.Marshal(map[string]any{"type": "api_key", "key": key})
		ext.stdin = string(b)
		r := ext.run("project", "model-account", "create", project, "--provider", provider, "--name", "同名显示标签", "--from-file", "-")
		data := r.ok(t)
		if r.Meta.RequestID == "" || strings.Contains(r.Stdout, key) {
			t.Fatal("missing real request ID or exposed key")
		}
		return data
	}
	if v := modelSelection(); v["mode"] != "automatic" || v["status"] != "not_configured" {
		t.Fatal("new Agent must be automatic without account")
	}
	first := create("synthetic-project-key-A")
	account := first["id"].(string)
	oldVersion := first["currentVersionId"].(string)
	if v := modelSelection(); v["resolvedAccountId"] != account || v["mode"] != "automatic" {
		t.Fatal("unique account not resolved")
	}
	second := create("synthetic-project-key-B")
	other := second["id"].(string)
	if other == account {
		t.Fatal("duplicate labels merged account identities")
	}
	if v := modelSelection(); v["status"] != "selection_required" || len(v["candidates"].([]any)) != 2 {
		t.Fatal("second account did not require selection")
	}
	explicitVersion := version()
	ext.run("agent", "credential-selection", "set", agent, "--provider", provider, "--account-id", account, "--expected-config-version", explicitVersion).ok(t)
	conflict := ext.run("agent", "credential-selection", "set", agent, "--provider", provider, "--account-id", other, "--expected-config-version", explicitVersion)
	conflict.fails(t, "precondition_failed", 8)
	if v := modelSelection(); v["selectedAccountId"] != account || v["mode"] != "explicit" {
		t.Fatal("CAS failure changed selection")
	}
	ext.stdin = `{"type":"api_key","key":"synthetic-project-key-C"}`
	replaced := ext.run("project", "model-account", "replace", project, account, "--expected-revision", "1", "--from-file", "-", "--yes").ok(t)
	if replaced["currentVersionId"] == oldVersion || replaced["revision"] == first["revision"] {
		t.Fatal("replacement did not make a new version")
	}
	ext.run("project", "model-account", "rename", project, account, "--name", "stale name", "--expected-revision", "1").fails(t, "precondition_failed", 8)
	if _, err := s.a.Store.GetModelAuthVersion(context.Background(), uuid.MustParse(oldVersion)); err != nil {
		t.Fatal("replacement removed historical version")
	}
	ext.run("project", "model-account", "rename", project, account, "--name", "新标签", "--expected-revision", fmt.Sprintf("%.0f", replaced["revision"])).ok(t)
	renamed := ext.run("project", "model-account", "get", project, account).ok(t)
	ext.run("project", "model-account", "delete", project, account, "--expected-revision", fmt.Sprintf("%.0f", renamed["revision"]), "--yes").ok(t)
	if v := modelSelection(); v["status"] != "account_deleted" || v["selectedAccountId"] != account || v["resolvedAccountId"] != nil {
		t.Fatal("deleted explicit selection fell back")
	}
	for _, preserved := range []string{oldVersion, replaced["currentVersionId"].(string)} {
		if _, err := s.a.Store.GetModelAuthVersion(context.Background(), uuid.MustParse(preserved)); err != nil {
			t.Fatal("account deletion removed historical model version")
		}
	}
	tombstone := ext.run("project", "model-account", "get", project, account).ok(t)
	if tombstone["status"] != "deleted" {
		t.Fatal("missing safe tombstone")
	}
	ext.run("agent", "credential-selection", "clear", agent, "--provider", provider, "--expected-config-version", version(), "--yes").ok(t)
	if v := modelSelection(); v["mode"] != "automatic" || v["resolvedAccountId"] != other {
		t.Fatal("non-migration clear did not restore unique automatic")
	}
	// Legacy writes are refused, without silently creating or replacing shared accounts.
	ext.stdin = `{"provider":"` + provider + `","key":"synthetic-old-agent-input"}`
	ext.run("agent", "credential", "set-model", agent, "--from-file", "-").fails(t, "project_credential_required", 8)
	ext.run("agent", "credential", "clear-model", agent, "--yes").fails(t, "project_credential_required", 8)
	if v := modelSelection(); v["resolvedAccountId"] != other {
		t.Fatal("old rejected write modified project selection")
	}
	// Existing external console-only OAuth paths stay forbidden; never start login.
	client := credentialTLSClient(t, w)
	credentialAppRequest(t, client, s.ext+"/api/v1/agents/"+agent+"/model-auth/operations", token, "POST", `{"provider":"openai-codex","expectedConfigVersion":1}`, 403)
	// CLI account lifecycle is independent of permission assignment.
	ext.stdin = `{"type":"cli","fields":{"token":"synthetic-project-gh"}}`
	cliAccount := ext.run("project", "cli-account", "create", project, "--cli", "gh", "--name", "gh fixture", "--from-file", "-").ok(t)["id"].(string)
	ext.run("agent", "credential-selection", "set", agent, "--cli", "gh", "--account-id", cliAccount, "--expected-config-version", version()).fails(t, "validation_failed", 9)
	ext.run("agent", "cli", "add", agent, "gh").ok(t)
	ext.run("agent", "credential-selection", "set", agent, "--cli", "gh", "--account-id", cliAccount, "--expected-config-version", version()).ok(t)
	ext.run("agent", "cli", "remove", agent, "gh", "--yes").ok(t)
	if acct := ext.run("project", "cli-account", "get", project, cliAccount).ok(t); acct["status"] == "deleted" {
		t.Fatal("permission removal deleted shared account")
	}
	impact := ext.run("project", "model-account", "impact", project, "--provider", provider, "--account-id", other).ok(t)
	if users := impact["agents"].([]any); len(users) != 1 || users[0].(map[string]any)["agentId"] != agent || users[0].(map[string]any)["mode"] != "automatic" {
		t.Fatal("model impact did not reflect current automatic user")
	}
	cliImpact := ext.run("project", "cli-account", "impact", project, "--cli", "gh", "--account-id", cliAccount).ok(t)
	if len(cliImpact["agents"].([]any)) != 0 {
		t.Fatal("CLI impact included agent without permission")
	}
	cliBefore := ext.run("project", "cli-account", "get", project, cliAccount).ok(t)
	ext.stdin = `{"type":"cli","fields":{"token":"synthetic-project-gh-replacement"}}`
	cliReplaced := ext.run("project", "cli-account", "replace", project, cliAccount, "--expected-revision", fmt.Sprintf("%.0f", cliBefore["revision"]), "--from-file", "-", "--yes").ok(t)
	if cliReplaced["currentVersionId"] == cliBefore["currentVersionId"] {
		t.Fatal("CLI replacement reused version")
	}
	ext.run("project", "cli-account", "rename", project, cliAccount, "--name", "gh renamed", "--expected-revision", fmt.Sprintf("%.0f", cliBefore["revision"])).fails(t, "precondition_failed", 8)
	cliRenamed := ext.run("project", "cli-account", "rename", project, cliAccount, "--name", "gh renamed", "--expected-revision", fmt.Sprintf("%.0f", cliReplaced["revision"])).ok(t)
	if cliRenamed["name"] != "gh renamed" || cliRenamed["currentVersionId"] != cliReplaced["currentVersionId"] {
		t.Fatal("CLI rename changed credential version")
	}
	deleted := ext.run("project", "cli-account", "delete", project, cliAccount, "--expected-revision", fmt.Sprintf("%.0f", cliRenamed["revision"]), "--yes").ok(t)
	for _, preserved := range []string{cliBefore["currentVersionId"].(string), cliReplaced["currentVersionId"].(string)} {
		var exists bool
		if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM project_cli_versions WHERE id=$1)`, uuid.MustParse(preserved)).Scan(&exists); err != nil || !exists {
			t.Fatal("account deletion removed historical CLI version")
		}
	}
	if deleted["status"] != "deleted" || deleted["deletedAt"] == nil {
		t.Fatal("CLI delete missing tombstone")
	}
	ext.run("project", "cli-account", "delete", project, cliAccount, "--expected-revision", fmt.Sprintf("%.0f", cliRenamed["revision"]), "--yes").fails(t, "precondition_failed", 8)
	if got := ext.run("project", "cli-account", "get", project, cliAccount).ok(t); got["status"] != "deleted" {
		t.Fatal("CLI tombstone unreadable")
	}
	models := ext.run("project", "model-account", "list", project, "--provider", provider).ok(t)["accounts"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != other {
		t.Fatal("model list lost purpose/provider/deleted filtering")
	}
	if len(ext.run("project", "cli-account", "list", project, "--cli", "gh").ok(t)["accounts"].([]any)) != 0 {
		t.Fatal("CLI list included deleted account")
	}
	// A manually issued TEST Agent token proves identity/routing, not autonomous Pi execution.
	internal := testTaskClient(t, s, agent)
	denied := internal.run("project", "model-account", "list", project)
	denied.fails(t, "command_not_allowed", 2)
	if denied.Meta.RequestID != "" {
		t.Fatal("Agent configuration command reached network")
	}
	credentialAppRequest(t, client, s.ext+"/api/v1/projects/"+project+"/credential-accounts", internal.vars["LOOPCTL_AGENT_TOKEN"], "GET", "", 401)
	t.Log("actual dedicated TLS App: stable IDs/repeated labels, 0/1/2 candidates, CAS, replacement/history, deleted explicit no fallback, null automatic, old writes denied, CLI complete CRUD/CAS and permission removal preserves shared account, exact impact/filtering; no model/OAuth calls")
}

// The proxy forwards exactly one actual App write, consumes its success, then
// drops only the CLI response. No fixture invents the committed account.
func TestProjectCredentialUnknownWriteWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	_, _, token := s.issueToken("", "project-credential-unknown", nil)
	project := s.defaultProject()
	direct := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	provider, _ := cliKeyProvider(t, direct)
	backend := credentialTLSClient(t, w)
	var forwards atomic.Int32
	accountID := make(chan string, 1)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, in *http.Request) {
		forwards.Add(1)
		body, err := io.ReadAll(in.Body)
		if err != nil {
			t.Error("proxy request read failed")
			return
		}
		request, err := http.NewRequestWithContext(in.Context(), in.Method, s.ext+in.URL.RequestURI(), strings.NewReader(string(body)))
		if err != nil {
			t.Error("proxy request failed")
			return
		}
		request.Header = in.Header.Clone()
		response, err := backend.Do(request)
		if err != nil {
			t.Error("actual App request failed")
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 201 {
			t.Error("actual App did not commit account")
			return
		}
		var account map[string]any
		if json.Unmarshal(data, &account) != nil {
			t.Error("actual App response invalid")
			return
		}
		id, ok := account["id"].(string)
		if !ok {
			t.Error("actual App did not return stable ID")
			return
		}
		accountID <- id
		connection, _, err := out.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("proxy drop failed")
			return
		}
		_ = connection.Close()
	}))
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	external := newLoopctl(t, map[string]string{"LOOPCTL_URL": proxy.URL, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": ca, "LOOPCTL_RETRY_MAX": "3"})
	external.stdin = `{"type":"api_key","key":"synthetic-response-loss-key"}`
	result := external.run("project", "model-account", "create", project, "--provider", provider, "--name", "nonunique label", "--from-file", "-")
	result.fails(t, "connection_failed", 4)
	if !strings.Contains(result.Stdout, "写入结果未知") || forwards.Load() != 1 {
		t.Fatal("response loss was retried or claimed known")
	}
	if strings.Contains(result.Stdout, "synthetic-response-loss-key") {
		t.Fatal("unknown outcome leaked secret")
	}
	select {
	case id := <-accountID:
		if got := direct.run("project", "model-account", "get", project, id).ok(t); got["id"] != id {
			t.Fatal("committed account not readable")
		}
	default:
		t.Fatal("no actual committed account ID")
	}
	if len(direct.run("project", "model-account", "list", project, "--provider", provider).ok(t)["accounts"].([]any)) != 1 {
		t.Fatal("unknown response created duplicate account")
	}
	t.Logf("actual TLS App response loss: committed once, CLI unknown/exit4, no replay; schema=%s redisPrefix=%s", w.schema, w.cfg.RedisKeyPrefix)
}
