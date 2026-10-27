package store_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrateConcurrentAndIdempotent(t *testing.T) {
	schema := pgtest.NewSchema(t)
	url := pgtest.URL(t)
	ctx := context.Background()

	open := func() *store.Store {
		s, err := store.Open(ctx, url, store.OpenOptions{SearchPath: schema})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a, b := open(), open()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, s := range []*store.Store{a, b} {
		wg.Go(func() { errs[i] = s.Migrate(ctx, quietLogger()) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("并发迁移失败：%v", err)
		}
	}

	files, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("找不到迁移文件：%v", err)
	}
	// 每个SQL迁移加真实012 Go迁移恰好执行一次；Go迁移不以空SQL占号。
	expected := len(files) + 1
	check := func(when string) {
		var applied, versions int
		if err := a.Pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT version_id) FROM goose_db_version WHERE version_id > 0 AND is_applied`).
			Scan(&applied, &versions); err != nil {
			t.Fatal(err)
		}
		if applied != expected || versions != expected {
			t.Fatalf("%s：应有 %d 个迁移各执行一次，实际记录 %d 次、%d 个版本", when, expected, applied, versions)
		}
	}
	check("并发迁移后")

	// 已是最新结构时再执行，不做任何改动。
	if err := a.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	check("重复迁移后")
}

func TestOneActiveRunPerAgent(t *testing.T) {
	s := pgtest.NewStore(t)
	ctx := context.Background()
	org, proj, agent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	mustExec(t, s, `INSERT INTO organizations (id, name, is_default) VALUES ($1, 'o', true)`, org)
	mustExec(t, s, `INSERT INTO projects (id, organization_id, name, is_default) VALUES ($1, $2, 'p', true)`, proj, org)
	mustExec(t, s, `INSERT INTO agents (id, project_id, name, model_provider, model_id) VALUES ($1, $2, 'a', 'anthropic', 'm')`, agent, proj)

	insertRun := func(status string) error {
		_, err := s.Pool.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status)
			VALUES ($1, $2, $3, 1, 1, $4)`, uuid.Must(uuid.NewV7()), agent, uuid.Must(uuid.NewV7()), status)
		return err
	}
	if err := insertRun("running"); err != nil {
		t.Fatal(err)
	}
	if err := insertRun("starting"); err == nil {
		t.Fatal("同一 Agent 不应同时有两次进行中的执行")
	}
	if err := insertRun("queued"); err != nil {
		t.Fatalf("排队执行不受限制：%v", err)
	}
	if err := insertRun("completed"); err != nil {
		t.Fatalf("已结束的执行不受限制：%v", err)
	}
}

func mustExec(t *testing.T, s *store.Store, sql string, args ...any) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
