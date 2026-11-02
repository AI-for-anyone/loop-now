package maintenance

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// 每日维护清理保留期之外的日志、写出能打开的完整备份，并只保留最近几份；不碰备份目录里的其他东西。
func TestDailyPrunesBacksUpAndKeepsNewest(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	old, recent := now.Add(-91*24*time.Hour), now.Add(-89*24*time.Hour)
	for _, at := range []time.Time{old, old, recent} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO lifecycle_events(id,at,agent_id,type,data) VALUES($1,$2,$3,'pi.started','{}')`, uuid.New(), at, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO audit_log(id,at,actor_kind,method,operation,status) VALUES($1,$2,'console','GET','x',200)`, uuid.New(), old); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var existing []string
	for day := range 8 {
		name := now.Add(-time.Duration(8-day) * 24 * time.Hour).Format(backupLayout)
		existing = append(existing, name)
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{st: st, o: Options{BackupDir: dir, BackupKeep: 7, PruneBatch: 1, Now: func() time.Time { return now }}, log: slog.New(slog.DiscardHandler)}
	s.o.defaults()
	if err := s.Daily(ctx); err != nil {
		t.Fatal(err)
	}
	var events, audits int
	if err := st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM lifecycle_events WHERE type='pi.started'),(SELECT count(*) FROM audit_log)`).Scan(&events, &audits); err != nil {
		t.Fatal(err)
	}
	if events != 1 || audits != 0 {
		t.Fatalf("保留期之外的日志应清理、之内的保留：%d %d", events, audits)
	}
	names, err := backups(dir)
	if err != nil {
		t.Fatal(err)
	}
	newest := now.Format(backupLayout)
	if want := append(slices.Clone(existing[2:]), newest); !slices.Equal(names, want) {
		t.Fatalf("应只保留最近 7 份：%v，期望 %v", names, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Fatal("清理备份不应碰其他文件", err)
	}
	// 备份是能打开的完整库。
	restored, err := db.Open(ctx, db.Options{Dir: filepath.Join(dir, newest)})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var agents, kept int
	if err := restored.QueryRow(ctx, `SELECT (SELECT count(*) FROM agents),(SELECT count(*) FROM lifecycle_events WHERE type='pi.started')`).Scan(&agents, &kept); err != nil || agents != 1 || kept != 1 {
		t.Fatalf("备份内容不对：%d %d %v", agents, kept, err)
	}
}

// 每日维护按上一次备份排期：没有备份时启动后不久就做，刚备份过就等满一个间隔。
func TestDailyScheduleFollowsLastBackup(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := &Service{o: Options{BackupDir: dir, Now: func() time.Time { return now }}}
	s.o.defaults()
	if got := s.untilDaily(); got != time.Minute {
		t.Fatalf("没有备份时：%v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, now.Add(-2*time.Hour).Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, partialPrefix+now.Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := s.untilDaily(); got != 22*time.Hour {
		t.Fatalf("两小时前备份过（未完成的不算）：%v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, now.Add(-30*time.Hour).Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := s.untilDaily(); got != 22*time.Hour {
		t.Fatalf("按最新一份排期：%v", got)
	}
}

// 备份失败时不留下看起来完整的备份。
func TestBackupFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dir := t.TempDir()
	now := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Backup(cancelled, st.Pool, dir, now); err == nil {
		t.Fatal("取消的备份应失败")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("失败的备份留下了 %d 项：%v", len(entries), err)
	}
	path, err := Backup(ctx, st.Pool, dir, now)
	if err != nil || filepath.Base(path) != now.Format(backupLayout) {
		t.Fatal(path, err)
	}
	if _, err := Backup(ctx, st.Pool, dir, now); err == nil {
		t.Fatal("同一时刻的备份已存在时应拒绝")
	}
}
