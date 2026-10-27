package app_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// Web owns only this file; production and shared test helpers are read-only.
func TestWebDBConnectionsIntegration(t *testing.T) {
	webDBConnectionsIntegration(t, "db")
}

func TestWebGoogleSAIntegration(t *testing.T) {
	webDBConnectionsIntegration(t, "sa")
}

func TestWebGoogleMetadataScreenshots(t *testing.T) { webDBConnectionsIntegration(t, "google-view") }

func TestWebGoogleLoginIntegration(t *testing.T) { webDBConnectionsIntegration(t, "google") }

func webDBConnectionsIntegration(t *testing.T, slice string) {
	if os.Getenv("LOOP_NOW_WEB_DB_INTEGRATION") != "1" {
		t.Skip("explicit TEST-only React DB management integration")
	}
	db, err := url.Parse(pgtest.URL(t))
	if err != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("TEST database required")
	}
	ro, err := redis.ParseURL(redistest.URL(t))
	if err != nil || ro.DB != 10 {
		t.Fatal("TEST Redis DB10 required")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/add-cloud-database-cli-templates/app-"+slice)
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	var w *world
	var address string
	audit := map[string]any{"boundary": "actual React -> route.fetch temporary App -> isolated TEST PG/Redis; management only; no model/OAuth/database client execution"}
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		conn, e := pgx.Connect(ctx, pgtest.URL(t))
		if e != nil {
			t.Error("cleanup PG read failed")
			return
		}
		defer conn.Close(ctx)
		var exists bool
		e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("schema cleanup not verified")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		iter := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := iter.Next(ctx)
		audit["redisPrefixEmpty"] = !found && iter.Err() == nil
		if found || iter.Err() != nil {
			t.Error("Redis cleanup not verified")
		}
		_, e = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("work root cleanup not verified")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4318"} {
			if addr == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("temporary listener remains")
			}
		}
		audit["listenersClosed"] = closed
		raw, _ := json.MarshalIndent(audit, "", "  ")
		if os.WriteFile(filepath.Join(evidence, "driver-results.json"), raw, 0600) != nil {
			t.Error("audit write failed")
		}
	})
	w = newWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4318"}
	var helper *webDBGoogleHelper
	var s *server
	if slice == "google" {
		helper = &webDBGoogleHelper{control: t.TempDir()}
		s = webDBStartGoogle(t, w, helper)
	} else {
		s = w.start(nil)
	}
	address = s.a.Addr()
	t.Cleanup(s.stop)
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	path, agent := s.createAgent("WebDBConnectionWorker")
	for _, cli := range []string{"psql", "mysql"} {
		s.must(200, "PUT", path+"/clis/"+cli, nil)
	}
	if slice == "sa" || slice == "google" {
		s.must(200, "PUT", path+"/clis/gcloud", nil)
	}
	private := decode[apitypes.Agent](t, s.must(201, "POST", "/platform/agents", map[string]any{"name": "WebDBPrivate", "model": agent.Model}))
	s.must(200, "PUT", "/agents/"+private.ID.String()+"/clis/psql", nil)
	if slice == "sa" || slice == "google" {
		s.must(200, "PUT", "/agents/"+private.ID.String()+"/clis/gcloud", nil)
	}
	manifest := map[string]any{"slice": slice, "apiBase": s.url, "webOrigin": "http://127.0.0.1:4318", "projectId": dc.Project.ID, "projectName": dc.Project.Name, "agentId": agent.ID, "platformAgentId": private.ID}
	if slice == "sa" || slice == "google" {
		manifest["sa1"] = webDBConnectionsSAJSON(t, "one")
		manifest["sa2"] = webDBConnectionsSAJSON(t, "two")
	}
	if helper != nil {
		manifest["controlDir"] = helper.control
	}
	if slice == "google-view" {
		credential := clicredential.Google{Type: "authorized_user", Account: "synthetic-view@example.test", ClientID: "fixture-client", ClientSecret: "synthetic-client", RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"fixture-scope"}}
		plain, err := credential.Encode()
		if err != nil {
			t.Fatal("synthetic view encode failed")
		}
		defer clear(plain)
		label := credential.Account
		record, err := s.a.Store.CreateProjectCredential(context.Background(), dc.Project.ID, "cli", "gcloud", "合成Google已保存账号", store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
		if err != nil {
			t.Fatal("synthetic metadata seed failed")
		}
		manifest["accountId"] = record.View.ID
		audit["boundary"] = "actual React readonly GET -> isolated App/TEST PG; directly seeded synthetic Google metadata, not login lifecycle; zero auth POST/model/provider"
	}
	manifestPath := filepath.Join(t.TempDir(), "web-db-manifest.json")
	raw, _ := json.Marshal(manifest)
	if os.WriteFile(manifestPath, raw, 0600) != nil {
		t.Fatal("manifest write failed")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	script := "db-connections-app.mjs"
	if slice == "sa" {
		script = "google-sa-app.mjs"
	}
	if slice == "google" {
		script = "google-login-app.mjs"
	}
	if slice == "google-view" {
		script = "google-metadata-app.mjs"
	}
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/"+script))
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_DB_MANIFEST=" + manifestPath}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	ctx := context.Background()
	counts := map[string]int{}
	for name, query := range map[string]string{
		"sets":            `SELECT count(*) FROM agent_db_connection_sets`,
		"members":         `SELECT count(*) FROM agent_db_connection_members`,
		"cliVersions":     `SELECT count(*) FROM project_cli_versions`,
		"runs":            `SELECT count(*) FROM runs`,
		"taskTurns":       `SELECT count(*) FROM task_turns`,
		"oauthOperations": `SELECT count(*) FROM model_auth_operations`,
		"deletedAccounts": `SELECT count(*) FROM project_credential_accounts WHERE deleted_at IS NOT NULL`,
	} {
		var n int
		if err := s.a.Store.Pool.QueryRow(ctx, query).Scan(&n); err != nil {
			t.Fatalf("PG read-back failed: %s", name)
		}
		counts[name] = n
	}
	if helper != nil {
		audit["boundary"] = "actual React -> isolated App/TEST PG/Redis; explicit offline CLILoginHelper; context-wide local issuer fulfillment; no SDK/callback/provider/model"
		audit["helperStarts"] = helper.starts.Load()
		audit["helperCancels"] = helper.cancels.Load()
		audit["helperRefreshes"] = helper.refreshes.Load()
		for name, query := range map[string]string{"googleGrants": "SELECT count(*) FROM google_cli_grants", "googleOperations": "SELECT count(*) FROM project_cli_auth_operations", "googleSucceeded": "SELECT count(*) FROM project_cli_auth_operations WHERE status='succeeded'", "googleFailed": "SELECT count(*) FROM project_cli_auth_operations WHERE status='failed'", "googleCancelled": "SELECT count(*) FROM project_cli_auth_operations WHERE status='cancelled'", "syntheticGoogleVersions": "SELECT count(*) FROM project_cli_versions WHERE auth_method='oauth' AND account_label='synthetic-web@example.test'", "retainedTerminalURLs": "SELECT count(*) FROM project_cli_auth_operations WHERE status NOT IN ('pending','awaiting_authorization') AND authorization_url IS NOT NULL"} {
			var n int
			if err := s.a.Store.Pool.QueryRow(ctx, query).Scan(&n); err != nil {
				t.Fatalf("PG safe read failed: %s", name)
			}
			counts[name] = n
		}
	}
	audit["pgCounts"] = counts
	earlyProof, _ := json.MarshalIndent(counts, "", "  ")
	if os.WriteFile(filepath.Join(evidence, "pg-readback.json"), earlyProof, 0600) != nil {
		t.Fatal("PG proof write failed")
	}
	if browserErr != nil {
		t.Fatal("browser failed; sanitized failure and PG read-back retained")
	}
	sets, members, versions := 2, 3, 4
	if slice == "sa" {
		sets, members, versions = 0, 0, 3
	}
	deleted := 1
	if slice == "google" {
		sets, members, versions, deleted = 0, 0, 2, 0
	}
	if slice == "google-view" {
		sets, members, versions, deleted = 0, 0, 1, 0
	}
	if counts["sets"] != sets || counts["members"] != members || counts["cliVersions"] != versions || counts["deletedAccounts"] != deleted || counts["runs"] != 0 || counts["taskTurns"] != 0 || counts["oauthOperations"] != 0 {
		t.Fatalf("PG proof mismatch: %+v", counts)
	}
	if slice == "sa" {
		for name, query := range map[string]string{"googleGrants": `SELECT count(*) FROM google_cli_grants`, "googleOperations": `SELECT count(*) FROM project_cli_auth_operations`} {
			var n int
			if err := s.a.Store.Pool.QueryRow(ctx, query).Scan(&n); err != nil || n != 0 {
				t.Fatalf("unexpected Google activity: %s", name)
			}
			counts[name] = n
		}
	}
	if slice == "google" {
		if counts["syntheticGoogleVersions"] != 1 || counts["retainedTerminalURLs"] != 0 {
			t.Fatal("synthetic subject or terminal URL PG mismatch")
		}
		expected := map[string]int{"googleGrants": 1, "googleOperations": 3, "googleSucceeded": 1, "googleFailed": 1, "googleCancelled": 1}
		queries := map[string]string{"googleGrants": "SELECT count(*) FROM google_cli_grants", "googleOperations": "SELECT count(*) FROM project_cli_auth_operations", "googleSucceeded": "SELECT count(*) FROM project_cli_auth_operations WHERE status='succeeded'", "googleFailed": "SELECT count(*) FROM project_cli_auth_operations WHERE status='failed'", "googleCancelled": "SELECT count(*) FROM project_cli_auth_operations WHERE status='cancelled'"}
		for name, query := range queries {
			var n int
			if err := s.a.Store.Pool.QueryRow(ctx, query).Scan(&n); err != nil || n != expected[name] {
				t.Fatalf("Google PG proof mismatch: %s", name)
			}
			counts[name] = n
		}
		if helper.starts.Load() != 3 || helper.cancels.Load() != 1 || helper.refreshes.Load() != 0 {
			t.Fatal("offline helper lifecycle mismatch")
		}
		audit["helperStarts"] = helper.starts.Load()
		audit["helperCancels"] = helper.cancels.Load()
		audit["helperRefreshes"] = helper.refreshes.Load()
	}
	audit["browserCompleted"] = true
	audit["pgCounts"] = counts
	proof, _ := json.MarshalIndent(counts, "", "  ")
	if os.WriteFile(filepath.Join(evidence, "pg-readback.json"), proof, 0600) != nil {
		t.Fatal("PG proof write failed")
	}
}

func webDBConnectionsSAJSON(t *testing.T, suffix string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("synthetic key generation failed")
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal("synthetic key encoding failed")
	}
	defer clear(der)
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	defer clear(private)
	raw, err := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "synthetic-key-" + suffix, "private_key": string(private), "client_email": "synthetic-" + suffix + "@fixture.iam.gserviceaccount.com", "client_id": "100000000000000000001", "token_uri": "https://oauth2.googleapis.com/token"})
	if err != nil {
		t.Fatal("synthetic JSON encoding failed")
	}
	defer clear(raw)
	return string(raw)
}

// Explicit App dependency injection only. It never invokes SDK, OAuth, a callback
// listener or the network. Browser controls carry operation IDs/actions only.
type webDBGoogleHelper struct {
	control                    string
	starts, cancels, refreshes atomic.Int32
}

func (h *webDBGoogleHelper) Login(ctx context.Context, id uuid.UUID, onURL func(string) error) (clicredential.Google, error) {
	h.starts.Add(1)
	select {
	case <-ctx.Done():
		h.cancels.Add(1)
		return clicredential.Google{}, &cliauth.SafeError{Code: "auth_cancelled"}
	case <-time.After(2200 * time.Millisecond):
	}
	if err := onURL("https://accounts.google.com/o/oauth2/auth?client_id=fixture-client&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=fixture-scope&state=fixture-state&code_challenge=fixture-challenge&code_challenge_method=S256&access_type=offline"); err != nil {
		return clicredential.Google{}, err
	}
	timer := time.NewTicker(40 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			h.cancels.Add(1)
			return clicredential.Google{}, &cliauth.SafeError{Code: "auth_cancelled"}
		case <-timer.C:
			raw, err := os.ReadFile(filepath.Join(h.control, id.String()+".json"))
			if err != nil {
				continue
			}
			var control struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(raw, &control) != nil {
				return clicredential.Google{}, &cliauth.SafeError{Code: "invalid_response"}
			}
			if control.Action == "fail" {
				return clicredential.Google{}, &cliauth.SafeError{Code: "oauth_failed"}
			}
			if control.Action == "succeed" {
				return clicredential.Google{Type: "authorized_user", Account: "synthetic-web@example.test", ClientID: "fixture-client", ClientSecret: "synthetic-client", RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"fixture-scope"}}, nil
			}
		}
	}
}
func (h *webDBGoogleHelper) Refresh(context.Context, clicredential.Google) (clicredential.Google, error) {
	h.refreshes.Add(1)
	return clicredential.Google{}, &cliauth.SafeError{Code: "requires_login"}
}
func webDBStartGoogle(t *testing.T, w *world, helper *webDBGoogleHelper) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, w.cfg.LogFormat, w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLILoginHelper: helper, CLIRefresher: helper})
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	go func() { _ = a.Serve() }()
	return &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}
