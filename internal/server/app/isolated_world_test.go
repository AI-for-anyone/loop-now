package app_test

import (
	"context"
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
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// isolatedWorld creates/removes only its own schema and Redis prefix, with no
// stale namespace sweeps, real auth, model, native CLI install or business task.
func isolatedWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	node := filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node")
	real, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: node, WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent"}})
	if e != nil {
		t.Fatal("offline Pi/catalog unavailable", e)
	}
	conn, e := pgx.Connect(ctx, pgtest.URL(t))
	if e != nil {
		t.Fatal("TEST PG unavailable")
	}
	defer conn.Close(ctx)
	schema := "isolated_world_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal("TEST schema creation failed")
	}
	t.Cleanup(func() {
		c, e := pgx.Connect(context.Background(), pgtest.URL(t))
		if e != nil {
			t.Error("cleanup TEST connection failed")
			return
		}
		defer c.Close(context.Background())
		if _, e = c.Exec(context.Background(), `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); e != nil {
			t.Error("own schema cleanup failed")
		}
	})
	opts, e := redis.ParseURL(redistest.URL(t))
	if e != nil {
		t.Fatal("TEST Redis URL invalid")
	}
	rc := redis.NewClient(opts)
	prefix := "loopnow:test:isolated-world:" + uuid.NewString() + ":"
	if e = rc.Ping(ctx).Err(); e != nil {
		t.Fatal("TEST Redis unavailable")
	}
	t.Cleanup(func() {
		if e := redistest.DeletePrefix(context.Background(), rc, prefix); e != nil {
			t.Error("own prefix cleanup failed")
		}
		_ = rc.Close()
	})
	fake := fakepibin.Path(t)
	wrapper := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.4.0; exit 0; fi\nexec " + fake + " \"$@\"\n"
	if e = os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	return &world{t: t, schema: schema, piAIDir: real.PiAIDir, cfg: config.Config{
		DatabaseURL: pgtest.URL(t), RedisURL: redistest.URL(t), RedisKeyPrefix: prefix, ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://127.0.0.1:4342"}, DefaultOrgName: "org", DefaultProjectName: "project", WorkRoot: filepath.Join(t.TempDir(), "work"), PiBin: wrapper, PiVersion: "1.4.0", PiAIDir: real.PiAIDir, NodeBin: node, PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second, PiStopGrace: time.Second, AbortTimeout: time.Second, GitCloneTimeout: time.Minute, CLIInstallTimeout: time.Minute, LockTTL: 5 * time.Second, LockRenewInterval: time.Second, ReconcileInterval: 100 * time.Millisecond, SSEKeepalive: time.Second, EventStreamMaxLen: 1000, RunTextTTL: time.Hour, ShutdownTimeout: 10 * time.Second, MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100, TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, LoopctlDistDir: loopctlfake.Dir(t), TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskRecordTimeout: time.Second}}
}
