package e2e_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Pick a provider advertising API Key support; env diagnostic names do not select credentials.
func cliKeyProvider(t *testing.T, ext *loopctl) (string, string) {
	t.Helper()
	providers := ext.run("model", "providers").ok(t)["providers"].([]any)
	for _, item := range providers {
		p := item.(map[string]any)
		for _, method := range p["authMethods"].([]any) {
			if method == "api_key" {
				id := p["id"].(string)
				models := ext.run("model", "list", id).ok(t)["models"].([]any)
				if len(models) > 0 {
					return id, models[0].(map[string]any)["id"].(string)
				}
			}
		}
	}
	t.Fatal("App model catalog has no API Key provider with models")
	return "", ""
}

// This sends only synthetic API Keys. No messages, login or provider requests are made.
func TestModelCredentialCLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	_, _, token := s.issueToken("", "credential-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	provider, model := cliKeyProvider(t, ext)
	created := ext.run("agent", "create", "--platform", "--name", "凭证验证", "--provider", provider, "--model", model).ok(t)
	id := created["id"].(string)
	agentID := uuid.MustParse(id)
	version := created["configVersion"].(float64)
	const keyA = "synthetic-cli-account-A"
	const keyB = "synthetic-cli-account-B"
	input, _ := json.Marshal(map[string]any{"provider": provider, "key": keyA, "expectedConfigVersion": version})
	file := filepath.Join(t.TempDir(), "model-key.json")
	if err := os.WriteFile(file, input, 0600); err != nil {
		t.Fatal(err)
	}
	r := ext.run("agent", "credential", "set-model", id, "--from-file", file)
	meta := r.ok(t)
	if r.Meta.RequestID == "" || meta["provider"] != provider || meta["authMethod"] != "api_key" || meta["status"] != "set" {
		t.Fatal("App API Key metadata/request ID missing")
	}
	oldID, err := uuid.Parse(meta["versionId"].(string))
	if err != nil {
		t.Fatal("invalid account version ID")
	}
	if strings.Contains(r.Stdout, keyA) || len(meta["envNames"].([]any)) != 0 || len(meta["fieldNames"].([]any)) != 0 || meta["filePaths"].([]any)[0] != "auth.json" {
		t.Fatal("metadata exposed model input or described env injection")
	}
	// accountLabel is optional: API Key accounts may omit it.
	detail := ext.run("agent", "get", id).ok(t)
	got := detail["modelCredential"].(map[string]any)
	if got["versionId"] != oldID.String() || got["provider"] != provider || got["authMethod"] != "api_key" || got["accountLabel"] != meta["accountLabel"] {
		t.Fatal("Agent detail lost model metadata")
	}
	encoded, _ := json.Marshal(detail)
	if bytes.Contains(encoded, []byte(keyA)) {
		t.Fatal("Agent detail exposed key")
	}
	// CLI rejects old env syntax locally, and the actual App rejects it over HTTP too.
	for _, old := range []string{`{"ANTHROPIC_API_KEY":"synthetic-old"}`, `{"env":{"ANTHROPIC_API_KEY":"synthetic-old"}}`} {
		ext.stdin = old
		invalid := ext.run("agent", "credential", "set-model", id, "--from-file", "-")
		invalid.fails(t, "invalid_argument", 2)
		if invalid.Meta.RequestID != "" || strings.Contains(invalid.Stdout, "synthetic-old") {
			t.Fatal("invalid input was sent or exposed")
		}
	}
	client := credentialTLSClient(t, w)
	response := credentialAppRequest(t, client, s.ext+"/api/v1/agents/"+id+"/model-credential", token, "PUT", `{"env":{"ANTHROPIC_API_KEY":"synthetic-old"}}`, 422)
	if response["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatal("actual App accepted old env schema")
	}
	current, exists, err := s.a.Store.CurrentModelAuthVersion(context.Background(), agentID)
	if err != nil || !exists || current == nil || current.ID != oldID {
		t.Fatal("rejected env input changed current account")
	}
	// Replace through the CLI; existing version A is retained for task snapshots.
	ext.stdin = `{"provider":"` + provider + `","key":"` + keyB + `"}`
	replacement := ext.run("agent", "credential", "set-model", id, "--from-file", "-").ok(t)
	newID := uuid.MustParse(replacement["versionId"].(string))
	if newID == oldID {
		t.Fatal("key replacement reused account version")
	}
	if _, err := s.a.Store.GetModelAuthVersion(context.Background(), oldID); err != nil {
		t.Fatal("replacement deleted historical account")
	}
	// New writes with old config are refused by the real App, preserving account B.
	ext.stdin = string(input)
	ext.run("agent", "credential", "set-model", id, "--from-file", "-").fails(t, "precondition_failed", 8)
	// A valid external token cannot query, start or cancel console-only OAuth operations.
	for _, op := range []struct{ method, path, body string }{
		{"GET", "/model-auth", ""},
		{"POST", "/model-auth/operations", `{"provider":"openai-codex","expectedConfigVersion":1}`},
		{"GET", "/model-auth/operations/" + uuid.NewString(), ""},
		{"POST", "/model-auth/operations/" + uuid.NewString() + "/cancel", `{}`},
	} {
		denied := credentialAppRequest(t, client, s.ext+"/api/v1/agents/"+id+op.path, token, op.method, op.body, 403)
		if denied["error"].(map[string]any)["code"] != "forbidden" {
			t.Fatal("external OAuth denial missing")
		}
	}
	var operations int
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_operations WHERE agent_id=$1`, agentID).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("external OAuth created an operation")
	}
	ext.stdin = ""
	ext.run("agent", "credential", "clear-model", id, "--yes").ok(t)
	current, exists, err = s.a.Store.CurrentModelAuthVersion(context.Background(), agentID)
	if err != nil || !exists || current != nil {
		t.Fatal("CLI clear did not persist explicit NULL marker")
	}
	if detail := ext.run("agent", "get", id).ok(t); detail["modelCredential"] != nil {
		t.Fatal("clear left credential metadata")
	}
	if _, err := s.a.Store.GetModelAuthVersion(context.Background(), newID); err != nil {
		t.Fatal("clear deleted task-bound account version")
	}
	t.Log("real App/TLS external CLI platform private credential: provider/key, version precondition, metadata, old env rejection, preserved historical versions, NULL clear marker and four console-only OAuth denials")
}

func credentialTLSClient(t *testing.T, w *world) *http.Client {
	t.Helper()
	pem, err := os.ReadFile(w.caFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid test CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}
func credentialAppRequest(t *testing.T, client *http.Client, url, token, method, body string, status int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("actual App %s expected %d got %d", method, status, resp.StatusCode)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
