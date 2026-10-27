package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/config"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// This Web-owned harness uses only a synthetic source root and a fresh TEST
// schema/prefix. The real source adapter/HTTP/App/transaction are not mocked.
func TestWebCLIImportIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_CLI_IMPORT_INTEGRATION") != "1" {
		t.Skip("explicit isolated TEST browser slice")
	}
	db, e := url.Parse(pgtest.URL(t))
	if e != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("TEST database required")
	}
	ro, e := redis.ParseURL(redistest.URL(t))
	if e != nil || ro.DB != 10 {
		t.Fatal("TEST Redis DB10 required")
	}
	root := testenv.RepoRoot()
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	evidence := filepath.Join(root, "web/test-results/add-local-cli-credential-import/app", runID)
	if e = os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	webCLIImportJSON(t, filepath.Join(root, "web/test-results/add-local-cli-credential-import/latest-app.json"), map[string]any{"runId": runID, "evidenceDir": evidence})
	audit := map[string]any{"boundary": "actual React -> route.fetch temporary schema17 App/TEST PG/Redis; real source adapter on synthetic root; no host/provider"}
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			h := sha256.Sum256(b)
			audit["compiledTestBinarySHA256"] = hex.EncodeToString(h[:])
		}
	}
	var w *world
	address := ""
	syntheticRoot := ""
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, pgtest.URL(t))
		if err != nil {
			t.Error("cleanup PG unavailable")
			return
		}
		defer c.Close(ctx)
		var exists bool
		err = c.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", w.schema).Scan(&exists)
		audit["schemaRemoved"] = err == nil && !exists
		if err != nil || exists {
			t.Error("TEST schema remains")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("TEST prefix remains")
		}
		for key, path := range map[string]string{"workRootRemoved": w.cfg.WorkRoot, "syntheticRootRemoved": syntheticRoot} {
			_, err = os.Stat(path)
			audit[key] = os.IsNotExist(err)
			if !os.IsNotExist(err) {
				t.Error("temporary root remains", key)
			}
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4322"} {
			if addr == "" {
				continue
			}
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				closed = false
				t.Error("temporary listener remains")
			}
		}
		audit["listenersClosed"] = closed
		webCLIImportJSON(t, filepath.Join(evidence, "driver-results.json"), audit)
	})
	w = newWorld(t)
	syntheticRoot = t.TempDir()
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4322"}
	files := map[string]string{".config/gh/hosts.yml": "github.com:\n  user: synthetic-web\n  oauth_token: SYNTHETIC_WEB_IMPORT_PRIVATE_GH\n", ".lark-cli/config.json": `{"strictMode":"off","currentApp":"fixture","apps":[{"name":"fixture","appId":"cli_webfixture","appSecret":"SYNTHETIC_WEB_IMPORT_PRIVATE_LARK","brand":"feishu","defaultAs":"bot","users":[]}]}`}
	before := map[string][32]byte{}
	for name, value := range files {
		p := filepath.Join(syntheticRoot, name)
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		before[name] = sha256.Sum256([]byte(value))
	}
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	var opened atomic.Int32
	factory := func(context.Context) (cliimport.Reader, error) {
		opened.Add(1)
		return source.New(syntheticRoot, catalog)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIImportReaderFactory: factory})
	if e != nil {
		ln.Close()
		t.Fatal("isolated App build failed", e)
	}
	go func() { _ = a.Serve() }()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	address = a.Addr()
	t.Cleanup(func() {
		s.stop()
		if err := os.RemoveAll(w.cfg.WorkRoot); err != nil {
			t.Error("work cleanup failed")
		}
		if err := os.RemoveAll(syntheticRoot); err != nil {
			t.Error("synthetic root cleanup failed")
		}
	})
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	if opened.Load() != 0 {
		t.Fatal("startup/GET read source")
	}
	manifestPath := filepath.Join(t.TempDir(), "import-manifest.json")
	webCLIImportJSON(t, manifestPath, map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4322", "projectId": dc.Project.ID, "projectName": dc.Project.Name, "evidenceDir": evidence})
	webCLIImportJSON(t, filepath.Join(evidence, "app-ready.json"), map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4322", "migrationTarget": 17, "sourceFactoryCallsBeforeBrowser": opened.Load()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	node := filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node")
	cmd := exec.CommandContext(ctx, node, filepath.Join(root, "web/scripts/cli-import-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_CLI_IMPORT_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	counts := map[string]int{}
	for name, q := range map[string]string{"accounts": "SELECT count(*) FROM project_credential_accounts", "versions": "SELECT count(*) FROM project_cli_versions", "operations": "SELECT count(*) FROM cli_credential_import_operations", "succeeded": "SELECT count(*) FROM cli_credential_import_operations WHERE status='succeeded'", "itemsImported": "SELECT count(*) FROM cli_credential_import_items WHERE status='imported'", "itemsAlreadyImported": "SELECT count(*) FROM cli_credential_import_items WHERE status='already_imported'", "receipts": "SELECT count(*) FROM cli_credential_import_receipts", "verifications": "SELECT count(*) FROM cli_credential_verifications", "runs": "SELECT count(*) FROM runs", "taskTurns": "SELECT count(*) FROM task_turns"} {
		var n int
		if err := a.Store.Pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Error("PG readback failed", name)
		}
		counts[name] = n
	}
	webCLIImportJSON(t, filepath.Join(evidence, "pg-readback.json"), counts)
	for key, want := range map[string]int{"accounts": 2, "versions": 2, "operations": 2, "succeeded": 2, "itemsImported": 2, "itemsAlreadyImported": 2, "receipts": 2, "verifications": 0, "runs": 0, "taskTurns": 0} {
		if counts[key] != want {
			t.Errorf("PG %s expected %d got %d", key, want, counts[key])
		}
	}
	unchanged := true
	for name, want := range before {
		b, err := os.ReadFile(filepath.Join(syntheticRoot, name))
		if err != nil || sha256.Sum256(b) != want {
			unchanged = false
		}
		clear(b)
	}
	audit["sourceUnchanged"] = unchanged
	audit["sourceFactoryCalls"] = opened.Load()
	audit["pgCounts"] = counts
	audit["schema"] = w.schema
	audit["redisPrefix"] = w.cfg.RedisKeyPrefix
	if !unchanged || opened.Load() != 2 {
		t.Error("unexpected source mutation/read count")
	}
	if browserErr != nil {
		t.Error("actual React driver failed", browserErr)
	}
}
func webCLIImportJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(b, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
