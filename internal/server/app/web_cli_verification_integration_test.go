package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	verifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// Web owns this file only. Native provider protocol acceptance belongs to the
// native harness; this test exercises real App/PG workers with explicit children.
func TestWebCLIVerificationIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_CLI_VERIFICATION_INTEGRATION") != "1" {
		t.Skip("explicit isolated TEST App browser slice")
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
	slice := os.Getenv("LOOP_NOW_WEB_CLI_VERIFICATION_SLICE")
	if slice != "" && slice != "install-state" {
		t.Fatal("unknown isolated verification slice")
	}
	folder := "app"
	if slice == "install-state" {
		folder = "app-install-state"
	}
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	evidence := filepath.Join(root, "web/test-results/add-cli-credential-verification", folder, runID)
	if e = os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	webCLIVerificationJSON(t, filepath.Join(root, "web/test-results/add-cli-credential-verification", "latest-"+folder+".json"), map[string]any{"runId": runID, "evidenceDir": evidence, "slice": slice})
	control := t.TempDir()
	var w *world
	var address string
	audit := map[string]any{"boundary": "actual React -> forwarded temporary App -> isolated TEST PG/Redis; actual installer/native versions; probe child fixtures, not native network or real provider acceptance"}
	if executable, err := os.Executable(); err == nil {
		if file, err := os.Open(executable); err == nil {
			hash := sha256.New()
			_, err = io.Copy(hash, file)
			file.Close()
			if err == nil {
				audit["compiledTestBinarySHA256"] = hex.EncodeToString(hash.Sum(nil))
			}
		}
	}
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, e := pgx.Connect(ctx, pgtest.URL(t))
		if e != nil {
			t.Error("cleanup PG unavailable")
			return
		}
		defer conn.Close(ctx)
		var exists bool
		e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("TEST schema cleanup not verified")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("TEST prefix cleanup not verified")
		}
		_, e = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("TEST work root cleanup not verified")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4320"} {
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
		webCLIVerificationJSON(t, filepath.Join(evidence, "driver-results.json"), audit)
	})
	w = newWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4320"}
	w.cfg.CLIInstallTimeout = 5 * time.Minute // Match the real default; old world helper uses one minute.
	w.cfg.CLIProbeTimeout = 4 * time.Second
	w.cfg.AuthRecordTimeout = time.Second
	archives := map[string]string{
		"https://github.com/cli/cli/releases/download/v2.100.0/gh_2.100.0_macOS_arm64.zip": filepath.Join(root, ".local-build/cli-verification-native-fixture/gh-official.zip"),
		"https://cdn.mysql.com/archives/mysql-8.4/mysql-8.4.9-macos15-arm64.tar.gz":        filepath.Join(root, ".local-build/cli-installation-proof/downloads/mysql-8.4.9-macos15-arm64.tar.gz"),
		"https://get.enterprisedb.com/postgresql/postgresql-18.3-1-osx-binaries.zip":       filepath.Join(root, ".local-build/cli-installation-proof/downloads/postgresql-18.3-1-osx-binaries.zip"),
	}
	files := map[string]string{}
	destinations := map[string]string{}
	i := 0
	for remote, file := range archives {
		if _, e = os.Stat(file); e != nil {
			t.Fatal("fixed local public tool archive prerequisite missing")
		}
		i++
		key := string(rune('a' + i))
		files["/"+key] = file
		destinations[remote] = key
	}
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, file)
	}))
	t.Cleanup(vendor.Close)
	client := &http.Client{Transport: webCLIVerificationTransport{base: http.DefaultTransport, url: vendor.URL, archives: destinations}}
	var mu sync.Mutex
	counts := map[string]int{}
	factory := func(_ context.Context, spec verifyservice.CommandSpec) (*exec.Cmd, error) {
		// Never log/serialize private spec.Args/Environment or credential contents.
		realRoot, rootError := filepath.EvalSymlinks(w.cfg.WorkRoot)
		realHome, homeError := filepath.EvalSymlinks(spec.Home)
		relative, relativeError := filepath.Rel(realRoot, realHome)
		parts := strings.Split(relative, string(os.PathSeparator))
		if rootError != nil || homeError != nil || relativeError != nil || len(parts) != 3 || parts[0] != ".cli-verification" {
			return nil, errors.New("fixture home boundary")
		}
		instanceID, instanceError := uuid.Parse(parts[1])
		operationID, operationError := uuid.Parse(parts[2])
		if instanceError != nil || operationError != nil || instanceID == uuid.Nil || operationID == uuid.Nil {
			return nil, errors.New("fixture operation scope boundary")
		}

		if spec.Phase == "version" {
			if spec.CLI == "mysql" || spec.CLI == "psql" {
				// Version-only private diagnostic; no credential has been rendered yet.
				return exec.Command("/bin/sh", "-c", `"$1" --version > "$2" 2> "$3"; code=$?; cat "$2"; cat "$3" >&2; exit "$code"`, "fixture", spec.Executable, filepath.Join(control, spec.CLI+".version-output"), filepath.Join(control, spec.CLI+".version-error")), nil
			}
			return exec.Command(spec.Executable, spec.Args...), nil
		}
		if spec.Phase != "probe" {
			return nil, errors.New("unexpected fixture phase")
		}
		mu.Lock()
		counts[spec.CLI]++
		mu.Unlock()
		if spec.CLI == "gh" {
			if strings.Join(spec.Args, " ") != "api --hostname github.com --method GET --include user" {
				return nil, errors.New("nonfixed GitHub plan")
			}
			found := false
			for _, v := range spec.Environment {
				if v == "GH_TOKEN=synthetic-web-gh-A" || v == "GH_TOKEN=synthetic-web-gh-B" {
					found = true
				}
			}
			if !found {
				return nil, errors.New("captured synthetic credential missing")
			}
		}
		if spec.CLI == "mysql" || spec.CLI == "psql" {
			suffix := []string{"--batch", "--skip-column-names", "--skip-auto-rehash", "--connect-timeout=10", "--execute", "SELECT 1"}
			if spec.CLI == "psql" {
				suffix = []string{"-X", "-w", "-v", "ON_ERROR_STOP=1", "-A", "-t", "-c", "SELECT 1"}
			}
			if len(spec.Args) != len(suffix)+2 || spec.Args[0] != "--loop-connection-id" || strings.Join(spec.Args[2:], " ") != strings.Join(suffix, " ") {
				return nil, errors.New("nonfixed database plan")
			}
		}
		mode, _ := os.ReadFile(filepath.Join(control, spec.CLI+".mode"))
		if string(mode) == "hold" {
			return exec.Command("/bin/sh", "-c", "sleep 20"), nil
		}
		if string(mode) == "fail" {
			return exec.Command("/bin/sh", "-c", "printf 'fixed fixture rejection' >&2; exit 1"), nil
		}
		if spec.CLI == "gh" {
			return exec.Command("/bin/sh", "-c", `sleep 0.3; printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":123,"login":"synthetic-web"}\n'`), nil
		}
		if spec.CLI == "mysql" || spec.CLI == "psql" {
			return exec.Command("/bin/sh", "-c", `sleep 0.3; printf '1\n'`), nil
		}
		return nil, errors.New("unregistered fixture CLI")
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, w.cfg.LogFormat, w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIHTTPClient: client, CLIVerificationCommand: factory})
	if e != nil {
		ln.Close()
		t.Fatal("isolated actual App build failed", e)
	}
	go func() { _ = a.Serve() }()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	address = a.Addr()
	t.Cleanup(func() {
		s.stop()
		if err := os.RemoveAll(w.cfg.WorkRoot); err != nil {
			t.Error("self-owned work root cleanup failed")
		}
	})
	// App is now compiled and started; server may release its short shared build window.
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	accountIDs := map[string]string{}
	for _, cli := range []string{"gh", "mysql", "psql"} {
		fields := map[string]string{"token": "synthetic-web-gh-A"}
		if cli != "gh" {
			mode := "REQUIRED"
			if cli == "psql" {
				mode = "require"
			}
			fields = map[string]string{"host": "127.0.0.1", "port": "1", "user": "synthetic", "password": "synthetic-web-password", "database": "fixture", "sslMode": mode}
		}
		var view struct {
			ID string `json:"id"`
		}
		raw := s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": cli, "name": "Web " + cli, "credential": map[string]any{"type": "cli", "fields": fields}})
		if json.Unmarshal(raw, &view) != nil || view.ID == "" {
			t.Fatal("synthetic account seed failed")
		}
		accountIDs[cli] = view.ID
	}
	manifest := map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4320", "projectId": dc.Project.ID, "projectName": dc.Project.Name, "accounts": accountIDs, "controlDir": control, "evidenceDir": evidence, "slice": slice, "installTimeoutMs": w.cfg.CLIInstallTimeout.Milliseconds()}
	manifestPath := filepath.Join(t.TempDir(), "web-verification-manifest.json")
	webCLIVerificationJSON(t, manifestPath, manifest)
	audit["schema"] = w.schema
	audit["redisPrefix"] = w.cfg.RedisKeyPrefix
	audit["appStarted"] = true
	webCLIVerificationJSON(t, filepath.Join(evidence, "app-ready.json"), map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4320", "boundary": "self-owned TEST App; synthetic accounts; no current service"})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/cli-verification-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_CLI_VERIFICATION_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	versionDiagnostics := map[string]any{}
	for _, cli := range []string{"mysql", "psql"} {
		stdout, _ := os.ReadFile(filepath.Join(control, cli+".version-output"))
		stderr, _ := os.ReadFile(filepath.Join(control, cli+".version-error"))
		versionDiagnostics[cli] = map[string]any{"stdoutBytes": len(stdout), "stderrBytes": len(stderr), "containsFixedVersion": strings.Contains(string(stdout), map[string]string{"mysql": "8.4.9", "psql": "18.3"}[cli]), "wrapperInvocationRejected": strings.Contains(string(stderr), "invalid invocation"), "nativeUnavailable": strings.Contains(string(stderr), "native client unavailable")}
		clear(stdout)
		clear(stderr)
	}
	audit["versionDiagnostics"] = versionDiagnostics
	values := map[string]int{}
	for name, query := range map[string]string{"operations": `SELECT count(*) FROM cli_credential_verifications`, "passed": `SELECT count(*) FROM cli_credential_verifications WHERE status='succeeded'`, "cancelled": `SELECT count(*) FROM cli_credential_verifications WHERE status='cancelled'`, "failed": `SELECT count(*) FROM cli_credential_verifications WHERE status='failed'`, "running": `SELECT count(*) FROM cli_credential_verifications WHERE status IN('pending','running')`, "runs": `SELECT count(*) FROM runs`, "taskTurns": `SELECT count(*) FROM task_turns`, "versions": `SELECT count(*) FROM project_cli_versions`} {
		var n int
		if e = s.a.Store.Pool.QueryRow(context.Background(), query).Scan(&n); e != nil {
			t.Error("isolated PG count failed")
		} else {
			values[name] = n
		}
	}
	mu.Lock()
	audit["probeChildCalls"] = counts
	mu.Unlock()
	audit["pgCounts"] = values
	webCLIVerificationJSON(t, filepath.Join(evidence, "pg-readback.json"), values)
	expectedCounts := map[string]int{"operations": 6, "passed": 4, "failed": 1, "cancelled": 1, "versions": 4}
	if slice == "install-state" {
		expectedCounts = map[string]int{"operations": 2, "passed": 2, "failed": 0, "cancelled": 0, "versions": 3}
	}
	for key, expected := range expectedCounts {
		if values[key] != expected {
			t.Errorf("PG %s count: expected %d actual %d", key, expected, values[key])
		}
	}
	if values["running"] != 0 || values["runs"] != 0 || values["taskTurns"] != 0 {
		t.Error("unexpected active verification/model/task writes")
	}
	if browserErr != nil {
		t.Error("actual React App driver failed", browserErr)
	}
}

type webCLIVerificationTransport struct {
	base     http.RoundTripper
	url      string
	archives map[string]string
}

func (t webCLIVerificationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	key, ok := t.archives[r.URL.String()]
	if !ok {
		return nil, errors.New("external download denied")
	}
	copy := r.Clone(r.Context())
	u, _ := url.Parse(t.url + "/" + key)
	copy.URL = u
	copy.Host = u.Host
	return t.base.RoundTrip(copy)
}
func webCLIVerificationJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		t.Fatal("safe test evidence encode failed")
	}
	if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
		t.Fatal("safe test evidence write failed")
	}
}
