package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/larksidecar"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// Web owns only this file. Production code/shared helpers are read-only.
func TestWebLarkUserLoginIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_LARK_INTEGRATION") != "1" {
		t.Skip("explicit TEST-only desktop browser slice")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/add-lark-user-login/app", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e := os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	audit := map[string]any{"boundary": "actual React -> forwarded temporary App -> TEST PG; real Lark client protocol rewritten to loopback provider; no native/provider/model/QA"}
	save := func(name string, v any) {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil || os.WriteFile(filepath.Join(evidence, name), b, 0600) != nil {
			t.Error("safe audit write failed")
		}
	}
	var w *world
	address := ""
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
		e = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", w.schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("schema remains")
		}
		ro, e := redis.ParseURL(redistest.URL(t))
		if e != nil {
			t.Error("TEST Redis unavailable")
			return
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("prefix remains")
		}
		_, e = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("work root remains")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4334"} {
			if addr == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("listener remains")
			}
		}
		audit["listenersClosed"] = closed
		save("driver-results.json", audit)
	})
	w = githubAppWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4334"}
	w.cfg.AuthLoginTimeout = 2 * time.Minute
	p := larktest.New(t)
	attempts := []*larktest.Attempt{p.Add("ou-web-synthetic", "桌面核实用户", nil, ""), p.Add("ou-web-late", "取消迟到用户", nil, ""), p.Add("ou-web-recovered", "原操作恢复用户", nil, "")}
	control := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for i, a := range attempts {
					if _, e := os.Stat(filepath.Join(control, fmt.Sprintf("%d.json", i+1))); e == nil {
						a.Release()
					}
				}
			}
		}
	}()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(ctx, &w.cfg, config.NewLogger(io.Discard, w.cfg.LogFormat, w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, LarkLoginHelper: p.Client})
	if e != nil {
		ln.Close()
		t.Fatal(e)
	}
	go func() { _ = a.Serve() }()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	address = a.Addr()
	t.Cleanup(s.stop)
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project
	source := decode[cm.Account](t, s.must(201, "POST", "/projects/"+project.ID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "桌面原应用", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-web-synthetic", "appSecret": "SYNTHETIC_APP_SECRET_ONLY", "brand": "feishu"}}}))
	manifest := map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4334", "projectId": project.ID, "projectName": project.Name, "sourceAccountId": source.ID, "controlDir": control, "evidenceDir": evidence}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	b, _ := json.Marshal(manifest)
	if os.WriteFile(manifestPath, b, 0600) != nil {
		t.Fatal("manifest write failed")
	}
	runCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
	defer stop()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/lark-login-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_LARK_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	counts := map[string]int{}
	for name, q := range map[string]string{"accounts": "SELECT count(*) FROM project_credential_accounts WHERE deleted_at IS NULL", "grants": "SELECT count(*) FROM lark_cli_grants", "operations": "SELECT count(*) FROM project_cli_auth_operations WHERE cli_name='lark-cli'", "succeeded": "SELECT count(*) FROM project_cli_auth_operations WHERE cli_name='lark-cli' AND status='succeeded'", "cancelled": "SELECT count(*) FROM project_cli_auth_operations WHERE cli_name='lark-cli' AND status='cancelled'", "terminalURLs": "SELECT count(*) FROM project_cli_auth_operations WHERE cli_name='lark-cli' AND status NOT IN ('pending','awaiting_authorization') AND authorization_url IS NOT NULL", "runs": "SELECT count(*) FROM runs", "turns": "SELECT count(*) FROM task_turns"} {
		var n int
		if e := a.Store.Pool.QueryRow(ctx, q).Scan(&n); e != nil {
			t.Fatalf("PG read failed: %s", name)
		}
		counts[name] = n
	}
	save("pg-readback.json", counts)
	audit["deviceCalls"] = p.DeviceCalls.Load()
	audit["deniedProviderDials"] = p.DeniedDials.Load()
	audit["passed"] = browserErr == nil
	if browserErr != nil {
		t.Fatal("desktop App browser failed; safe evidence retained")
	}
	for key, want := range map[string]int{"accounts": 3, "grants": 2, "operations": 3, "succeeded": 2, "cancelled": 1, "terminalURLs": 0, "runs": 0, "turns": 0} {
		if counts[key] != want {
			t.Errorf("PG %s=%d want %d", key, counts[key], want)
		}
	}
	if p.DeviceCalls.Load() != 3 || p.DeniedDials.Load() != 0 {
		t.Error("unexpected protocol/provider boundary")
	}
}

// Actual gate delegation with no preexisting Pi/native scopes. This is Web/App
// publication proof only, not a native process or QA execution acceptance.
func TestWebLarkSwitchPublicationIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_LARK_SWITCH_INTEGRATION") != "1" {
		t.Skip("explicit TEST-only switch publication browser slice")
	}
	slice := os.Getenv("LOOP_NOW_WEB_LARK_SWITCH_SLICE")
	if slice != "commit" && slice != "unknown" && slice != "target-unavailable" {
		t.Fatal("explicit commit, unknown or target-unavailable slice required")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/allow-blocked-task-lark-account-switch/app-"+slice, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if os.MkdirAll(evidence, 0700) != nil {
		t.Fatal("evidence directory failed")
	}
	audit := map[string]any{"boundary": "actual desktop React -> temporary App/TEST PG/Redis; real empty-scope local/sidecar gates; synthetic original blocked history; stopped task driver so queued intent only; no native/Pi/model/QA execution"}
	save := func(name string, value any) {
		b, e := json.MarshalIndent(value, "", "  ")
		if e != nil || os.WriteFile(filepath.Join(evidence, name), b, 0600) != nil {
			t.Error("safe audit write failed")
		}
	}
	var schema, prefix, workRoot, address string
	t.Run("isolated", func(t *testing.T) {
		s, in, before, provider := taskLarkSwitchAppReady(t)
		ctx := context.Background()
		address = s.a.Addr()
		prefix = s.a.Coord.Keys().Prefix
		workRoot = s.a.Cfg.WorkRoot
		if s.a.Store.Pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema) != nil {
			t.Fatal("schema read failed")
		}
		// Only this self-owned, idle test server changes its allowed browser origin.
		s.a.API.O.ConsoleOrigins = []string{"http://127.0.0.1:4336"}
		// Delegate to the actual Manager/RuntimeGate, never a successful no-op.
		s.a.Tasks.LarkSwitchNative = &taskLarkAppNative{larksidecar.NewManager()}
		var publisher store.TaskLarkFenceMember
		for _, m := range in.Members {
			if m.Role == "publisher" {
				publisher = m
			}
		}
		// Complete the explicitly synthetic blocked display fixture; production
		// data/permissions/snapshots are not changed or inferred from a role name.
		_, e := s.a.Store.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			step := "qa"
			m.Record.View.Task.CurrentStepID = &step
			m.Record.View.Task.CurrentAgentID = &publisher.AgentID
			for i := range m.Record.View.Nodes {
				n := &m.Record.View.Nodes[i]
				if n.Inputs == nil {
					n.Inputs = map[string]any{}
				}
				if n.InputVersions == nil {
					n.InputVersions = []workflow.InputVersion{}
				}
			}
			return nil, nil
		})
		if e != nil {
			t.Fatal("synthetic blocked display setup failed", e)
		}
		view := decode[apitypes.ConsoleTaskDetail](t, s.must(200, "GET", "/tasks/"+in.TaskID.String(), nil))
		if view.LarkSwitchParticipants == nil || len(*view.LarkSwitchParticipants) != 2 {
			t.Fatal("console participant assembly missing")
		}
		project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project
		members := []taskmodel.TaskLarkSwitchMemberInput{}
		for _, m := range in.Capture.Members {
			members = append(members, taskmodel.TaskLarkSwitchMemberInput{AgentID: m.AgentID, RuntimeID: m.RuntimeID, ExpectedBindingRevision: m.PreviousRevision})
		}
		control := t.TempDir()
		if slice == "target-unavailable" {
			controlCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-controlCtx.Done():
						return
					case <-ticker.C:
						if _, err := os.Stat(filepath.Join(control, "invalidate-target")); err != nil {
							continue
						}
						// TEST-only authoritative unavailable state after UI selection,
						// before the original HTTP POST reaches its transaction.
						tag, err := s.a.Store.Pool.Exec(controlCtx, `UPDATE lark_cli_grants SET refresh_state='unavailable',error_code='auth_timeout' WHERE version_id=$1 AND refresh_state='ready'`, in.Capture.Target.VersionID)
						if err != nil || tag.RowsAffected() != 1 {
							t.Error("TEST target invalidation failed")
							return
						}
						if os.WriteFile(filepath.Join(control, "target-invalidated"), nil, 0600) != nil {
							t.Error("TEST target acknowledgement failed")
						}
						return
					}
				}
			}()
		}
		manifest := map[string]any{"slice": slice, "controlDir": control, "apiBase": s.url, "webOrigin": "http://127.0.0.1:4336", "projectName": project.Name, "taskId": in.TaskID, "expectedTaskVersion": view.Task.Version, "expectedNodeRunId": in.NodeRunID, "expectedAgentId": publisher.AgentID, "expectedRuntimeId": publisher.RuntimeID, "expectedMembers": members, "targetAccountId": in.Capture.Target.AccountID, "targetVersionId": in.Capture.Target.VersionID, "evidenceDir": evidence}
		manifestPath := filepath.Join(t.TempDir(), "manifest.json")
		b, _ := json.Marshal(manifest)
		if os.WriteFile(manifestPath, b, 0600) != nil {
			t.Fatal("manifest write failed")
		}
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/lark-switch-app.mjs"))
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_LARK_SWITCH_MANIFEST=" + manifestPath}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
		})
		browserErr := cmd.Run()
		counts := map[string]int{}
		for key, q := range map[string]string{"actions": "SELECT count(*) FROM task_lark_switch_actions", "bindings": "SELECT count(*) FROM task_lark_credential_bindings", "members": "SELECT count(*) FROM task_lark_binding_members", "fences": "SELECT count(*) FROM task_lark_switch_fences", "turns": "SELECT count(*) FROM task_turns", "runs": "SELECT count(*) FROM runs"} {
			var n int
			if s.a.Store.Pool.QueryRow(ctx, q).Scan(&n) != nil {
				t.Fatalf("PG read failed: %s", key)
			}
			counts[key] = n
		}
		unchanged := true
		followupUnprepared := false
		for _, m := range in.Members {
			inst, e := s.a.Store.TaskInstance(ctx, m.RuntimeID)
			if e != nil || sha256.Sum256(inst.Snapshot) != before[m.RuntimeID] {
				unchanged = false
			}
			if m.Role == "followup" {
				followupUnprepared = e == nil && !inst.Prepared && inst.SessionFile == nil
			}
		}
		audit["snapshotHashesUnchanged"] = unchanged
		audit["followupUnprepared"] = followupUnprepared
		audit["deniedProviderDials"] = provider.DeniedDials.Load()
		audit["browserPassed"] = browserErr == nil
		save("pg-readback.json", counts)
		if browserErr != nil {
			t.Fatal("desktop switch publication browser failed; safe evidence retained")
		}
		wantCounts := map[string]int{"actions": 1, "bindings": 1, "members": 2, "fences": 0, "turns": 1, "runs": 0}
		if slice == "target-unavailable" {
			wantCounts = map[string]int{"actions": 0, "bindings": 0, "members": 0, "fences": 0, "turns": 0, "runs": 0}
		}
		for key, want := range wantCounts {
			if counts[key] != want {
				t.Errorf("PG %s=%d want%d", key, counts[key], want)
			}
		}
		if !unchanged || !followupUnprepared || provider.DeniedDials.Load() != 0 {
			t.Error("original scope/provider boundary changed")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if schema != "" {
		conn, e := pgx.Connect(ctx, pgtest.URL(t))
		if e != nil {
			t.Fatal("cleanup PG unavailable")
		}
		defer conn.Close(ctx)
		var exists bool
		e = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("schema remains")
		}
	}
	if prefix != "" {
		ro, e := redis.ParseURL(redistest.URL(t))
		if e != nil {
			t.Fatal("cleanup Redis unavailable")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, prefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("prefix remains")
		}
	}
	if workRoot != "" {
		_, e := os.Stat(workRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("work root remains")
		}
	}
	closed := true
	for _, addr := range []string{address, "127.0.0.1:4336"} {
		if addr == "" {
			continue
		}
		c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if e == nil {
			closed = false
			c.Close()
			t.Error("listener remains")
		}
	}
	audit["listenersClosed"] = closed
	save("driver-results.json", audit)
}

// Only the missing 4.2 cells. The previously accepted browser matrix is not rerun.
func TestWebLarkUserLoginFailureDeleteIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_LARK_MISSING_CELLS") != "1" {
		t.Skip("explicit TEST-only failure/delete browser slice")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/add-lark-user-login/failure-delete", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e := os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	audit := map[string]any{"boundary": "desktop React -> temporary App/TEST PG; new-login failure and account deletion only; not public re-login/native/task/OAuth"}
	save := func(name string, value any) {
		b, e := json.MarshalIndent(value, "", "  ")
		if e != nil || os.WriteFile(filepath.Join(evidence, name), b, 0600) != nil {
			t.Error("safe audit write failed")
		}
	}
	var w *world
	address := ""
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
		e = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", w.schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("schema remains")
		}
		ro, e := redis.ParseURL(redistest.URL(t))
		if e != nil {
			t.Error("cleanup Redis unavailable")
			return
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("prefix remains")
		}
		_, e = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("work root remains")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4334"} {
			if addr == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("listener remains")
			}
		}
		audit["listenersClosed"] = closed
		save("driver-results.json", audit)
	})
	w = githubAppWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4334"}
	w.cfg.AuthLoginTimeout = time.Minute
	p := larktest.New(t)
	s := w.start(nil)
	address = s.a.Addr()
	t.Cleanup(s.stop)
	s.a.CLIAuth.Lark = p.Client
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project
	accounts := "/projects/" + project.ID.String() + "/credential-accounts"
	base := "/projects/" + project.ID.String() + "/cli-auth/operations"
	source := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "缺格原应用", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-web-missing-cells", "appSecret": "SYNTHETIC_ONLY_SECRET", "brand": "feishu"}}}))
	seed := p.Add("ou-web-keep-old", "保留的核实用户", nil, "")
	seed.Release()
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	user := waitLarkApp(t, s, base, id, "succeeded").Account
	if user == nil {
		t.Fatal("synthetic old user missing")
	}
	hashVersions := func() [32]byte {
		rows, e := s.a.Store.Pool.Query(ctx, "SELECT secret FROM project_cli_versions ORDER BY id")
		if e != nil {
			t.Fatal("credential read failed")
		}
		defer rows.Close()
		h := sha256.New()
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) != nil {
				t.Fatal("credential scan failed")
			}
			h.Write(b)
		}
		if rows.Err() != nil {
			t.Fatal("credential rows failed")
		}
		var out [32]byte
		copy(out[:], h.Sum(nil))
		return out
	}
	before := hashVersions()
	failed := p.Add("ou-must-not-save", "不会保存的用户", nil, "temporarily_unavailable")
	failed.Release()
	manifest := map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4334", "projectId": project.ID, "projectName": project.Name, "sourceAccountId": source.ID, "userAccountId": user.ID, "evidenceDir": evidence}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	b, _ := json.Marshal(manifest)
	if os.WriteFile(manifestPath, b, 0600) != nil {
		t.Fatal("manifest write failed")
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/lark-login-missing-cells-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_LARK_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	browserErr := cmd.Run()
	counts := map[string]int{}
	for key, q := range map[string]string{"activeAccounts": "SELECT count(*) FROM project_credential_accounts WHERE deleted_at IS NULL", "deletedAccounts": "SELECT count(*) FROM project_credential_accounts WHERE deleted_at IS NOT NULL", "versions": "SELECT count(*) FROM project_cli_versions", "grants": "SELECT count(*) FROM lark_cli_grants", "operations": "SELECT count(*) FROM project_cli_auth_operations WHERE cli_name='lark-cli'", "failed": "SELECT count(*) FROM project_cli_auth_operations WHERE status='failed'", "succeeded": "SELECT count(*) FROM project_cli_auth_operations WHERE status='succeeded'", "terminalURLs": "SELECT count(*) FROM project_cli_auth_operations WHERE status NOT IN('pending','awaiting_authorization') AND authorization_url IS NOT NULL", "runs": "SELECT count(*) FROM runs", "turns": "SELECT count(*) FROM task_turns"} {
		var n int
		if s.a.Store.Pool.QueryRow(ctx, q).Scan(&n) != nil {
			t.Fatalf("PG read failed: %s", key)
		}
		counts[key] = n
	}
	audit["passed"] = browserErr == nil
	audit["credentialVersionsUnchanged"] = before == hashVersions()
	audit["deviceCalls"] = p.DeviceCalls.Load()
	audit["deniedProviderDials"] = p.DeniedDials.Load()
	save("pg-readback.json", counts)
	if browserErr != nil {
		t.Fatal("failure/delete desktop browser failed; safe evidence retained")
	}
	for key, want := range map[string]int{"activeAccounts": 0, "deletedAccounts": 2, "versions": 2, "grants": 1, "operations": 2, "failed": 1, "succeeded": 1, "terminalURLs": 0, "runs": 0, "turns": 0} {
		if counts[key] != want {
			t.Errorf("PG %s=%d want%d", key, counts[key], want)
		}
	}
	if before != hashVersions() || p.DeviceCalls.Load() != 2 || p.DeniedDials.Load() != 0 {
		t.Error("retained version/provider boundary changed")
	}
}
