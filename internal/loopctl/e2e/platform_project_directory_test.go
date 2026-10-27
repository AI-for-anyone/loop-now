package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// Self-owned resources only: no pgtest/redistest stale-resource sweeps.
func platformDirectorySchema(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal("TEST connection unavailable")
	}
	name := "platform_directory_app_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = c.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		var exists bool
		if err := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, name).Scan(&exists); err != nil || exists {
			t.Error("schema cleanup failed")
		} else {
			t.Log("schema cleanup verified")
		}
	})
	return name
}

func platformDirectoryRedis(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	opts, err := redis.ParseURL(redistest.URL(t))
	if err != nil {
		t.Fatal("invalid TEST Redis URL")
	}
	r := redis.NewClient(opts)
	if err = r.Ping(ctx).Err(); err != nil {
		t.Fatal("TEST Redis unavailable")
	}
	prefix := "loopnow:test:platform-directory:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		defer r.Close()
		if err := redistest.DeletePrefix(ctx, r, prefix); err != nil {
			t.Error(err)
		}
		keys, err := r.Keys(ctx, prefix+"*").Result()
		if err != nil || len(keys) != 0 {
			t.Error("Redis prefix cleanup failed")
		} else {
			t.Log("Redis prefix cleanup verified")
		}
	})
	return prefix
}

func TestPlatformProjectDirectoryWithApp(t *testing.T) {
	w := newPlatformDirectoryWorld(t, true)
	s := w.start(nil)
	defer func() {
		s.stop()
		for _, addr := range []string{w.addr, w.extAddr} {
			c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				c.Close()
				t.Error("temporary listener remained open")
			}
		}
		if err := os.RemoveAll(w.cfg.WorkRoot); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(w.cfg.WorkRoot); !os.IsNotExist(err) {
			t.Error("workroot cleanup failed")
		}
		t.Log("temporary listeners/workroot cleanup verified")
	}()
	ctx := context.Background()
	_, _, externalToken := s.issueToken("", "directory-fixture", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": externalToken, "LOOPCTL_CA_FILE": w.caFile})
	provider, model := cliKeyProvider(t, ext)
	platform := ext.run("agent", "create", "--platform", "--name", "目录协作者", "--provider", provider, "--model", model).ok(t)
	platformID := platform["id"].(string)
	ext.stdin = `{"provider":"` + provider + `","key":"synthetic-platform-directory","expectedConfigVersion":1}`
	ext.run("agent", "credential", "set-model", platformID, "--from-file", "-").ok(t)
	ext.stdin = ""
	orgID := uuid.New()
	if _, err := s.a.Store.Pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'跨组织')`, orgID); err != nil {
		t.Fatal(err)
	}
	project, err := s.a.Store.CreateProject(ctx, orgID, "协作项目")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := s.a.Store.CreateAgent(ctx, store.ProjectScope(project.ID), "项目经理", provider, model, "private-project-prompt-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Store.SetProjectManager(ctx, project.ID, manager.ID); err != nil {
		t.Fatal(err)
	}
	ext.stdin = `{"type":"api_key","key":"synthetic-project-directory"}`
	ext.run("project", "model-account", "create", project.ID.String(), "--provider", provider, "--name", "隔离合成账号", "--from-file", "-").ok(t)
	ext.stdin = ""
	// A real fakePi process obtains its token through the production executor.
	s.console(201, "POST", "/inbox/messages", map[string]any{"to": platformID, "text": "开始目录验证"})
	s.waitIdle(platformID)
	platformEnv := w.agentEnv()
	plat := newLoopctl(t, platformEnv)
	if plat.run("whoami").ok(t)["address"] != "目录协作者" {
		t.Fatal("wrong process token")
	}
	directory := plat.run("project", "list")
	var data struct {
		Projects []store.PlatformProjectDirectoryItem `json:"projects"`
	}
	if err = json.Unmarshal(directory.Data, &data); err != nil {
		t.Fatal(err)
	}
	var target *store.PlatformDirectoryManager
	foundNull := false
	for _, p := range data.Projects {
		if p.Manager == nil {
			foundNull = true
		}
		if p.ID == project.ID {
			target = p.Manager
		}
	}
	if !foundNull || target == nil || target.ID != manager.ID || target.Address != "跨组织/协作项目/项目经理" {
		t.Fatal("wrong cross-organization directory")
	}
	for _, bad := range []string{"private-project-prompt-sentinel", "synthetic-project-directory", "prompt", "credential"} {
		if strings.Contains(directory.Stdout, bad) {
			t.Fatal("directory exposed private content")
		}
	}
	sent := plat.run("inbox", "send", target.Address, "--text", "合成跨组织协作").ok(t)
	s.waitIdle(manager.ID.String())
	managerEnv := w.agentEnv()
	mgr := newLoopctl(t, managerEnv)
	if mgr.run("whoami").ok(t)["address"] != target.Address {
		t.Fatal("Manager process token not bound to recipient")
	}
	mgr.run("project", "list").fails(t, "forbidden", 6)
	read := mgr.run("inbox", "read", sent["id"].(string)).ok(t)
	if read["text"] != "合成跨组织协作" {
		t.Fatal("wrong Manager message")
	}
	reply := mgr.run("inbox", "reply", sent["id"].(string), "--text", "合成确认").ok(t)
	s.waitIdle(platformID)
	if plat.run("inbox", "read", reply["id"].(string)).ok(t)["text"] != "合成确认" {
		t.Fatal("bidirectional Inbox failed")
	}
	if _, err = s.a.Store.RenameProject(ctx, project.ID, "新协作项目"); err != nil {
		t.Fatal(err)
	}
	plat.run("inbox", "send", target.Address, "--text", "旧地址不得改投").fails(t, "not_found", 7)
	var renamed struct {
		Projects []store.PlatformProjectDirectoryItem `json:"projects"`
	}
	r := plat.run("project", "list")
	r.ok(t)
	if err = json.Unmarshal(r.Data, &renamed); err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, p := range renamed.Projects {
		if p.ID == project.ID {
			matched = p.Manager != nil && p.Manager.ID == manager.ID && p.Manager.Address == "跨组织/新协作项目/项目经理"
		}
	}
	if !matched {
		t.Fatal("rename did not update current locator")
	}
	plat.run("project", "get", project.ID.String()).fails(t, "command_not_allowed", 2)
	// Production process shutdown invalidates its token, no forged Principal.
	if err = s.a.Executor.Release(ctx, uuid.MustParse(platformID)); err != nil {
		t.Fatal(err)
	}
	plat.run("project", "list").fails(t, "unauthenticated", 5)
	var conversations int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&conversations); err != nil || conversations != 3 {
		t.Fatal("unexpected Inbox mutation count")
	}
	t.Log("real process tokens; cross-org directory/Inbox/read/reply, old-address rejection, revocation passed; synthetic TEST only")
}

func newPlatformDirectoryWorld(t *testing.T, external bool) *world {
	t.Helper()
	real, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.1.0：%v", err)
	}
	schema := platformDirectorySchema(t)
	prefix := platformDirectoryRedis(t)
	dump := filepath.Join(t.TempDir(), "dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w := &world{t: t, schema: schema, dump: dump, external: external, cfg: config.Config{
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second,
		DatabaseURL: pgtest.URL(t), RedisURL: redistest.URL(t),
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
		WorkRoot: filepath.Join(t.TempDir(), "work"), RedisKeyPrefix: prefix, DefaultOrgName: "默认组织", DefaultProjectName: "默认项目",
		PiBin: fakepibin.Path(t), PiVersion: "1.1.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second,
		PiStopGrace: 2 * time.Second, AbortTimeout: 3 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, LockTTL: 5 * time.Second, LockRenewInterval: time.Second, ReconcileInterval: 500 * time.Millisecond,
		SSEKeepalive: time.Second, EventStreamMaxLen: 1000, RunTextTTL: time.Hour, ShutdownTimeout: 10 * time.Second,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16,
		TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100,
		LoopctlDistDir: loopctlfake.Dir(t), ExecEnvPassthrough: []string{"FAKEPI_DUMP"},
	}}
	if external {
		cert, key, ca := selfSigned(t)
		w.cfg.ExternalListenAddr, w.cfg.ExternalTLSCert, w.cfg.ExternalTLSKey = "127.0.0.1:0", cert, key
		w.caFile = ca
	}
	return w
}
