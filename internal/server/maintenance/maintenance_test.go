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

// Daily maintenance deletes logs outside the retention period, writes a complete backup that can be opened, and keeps
// only the most recent few; it does not touch anything else in the backup directory.
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
		t.Fatalf("logs older than the retention period should be removed and newer ones kept: %d %d", events, audits)
	}
	names, err := backups(dir)
	if err != nil {
		t.Fatal(err)
	}
	newest := now.Format(backupLayout)
	if want := append(slices.Clone(existing[2:]), newest); !slices.Equal(names, want) {
		t.Fatalf("only the latest 7 should be kept: %v, want %v", names, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Fatal("cleaning up backups should not touch other files", err)
	}
	// The backup is a complete database that can be opened.
	restored, err := db.Open(ctx, db.Options{Dir: filepath.Join(dir, newest)})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var agents, kept int
	if err := restored.QueryRow(ctx, `SELECT (SELECT count(*) FROM agents),(SELECT count(*) FROM lifecycle_events WHERE type='pi.started')`).Scan(&agents, &kept); err != nil || agents != 1 || kept != 1 {
		t.Fatalf("unexpected backup contents: %d %d %v", agents, kept, err)
	}
}

// Daily maintenance is scheduled from the last backup: with no backup it runs shortly after start, and right after a
// backup it waits a full interval.
func TestDailyScheduleFollowsLastBackup(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := &Service{o: Options{BackupDir: dir, Now: func() time.Time { return now }}}
	s.o.defaults()
	if got := s.untilDaily(); got != time.Minute {
		t.Fatalf("with no backup: %v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, now.Add(-2*time.Hour).Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, partialPrefix+now.Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := s.untilDaily(); got != 22*time.Hour {
		t.Fatalf("backed up two hours ago (unfinished ones do not count): %v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, now.Add(-30*time.Hour).Format(backupLayout)), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := s.untilDaily(); got != 22*time.Hour {
		t.Fatalf("scheduled from the newest backup: %v", got)
	}
}

// A failed backup leaves nothing that looks like a complete backup.
func TestBackupFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dir := t.TempDir()
	now := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Backup(cancelled, st.Pool, dir, now); err == nil {
		t.Fatal("a cancelled backup should fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a failed backup left %d entries: %v", len(entries), err)
	}
	path, err := Backup(ctx, st.Pool, dir, now)
	if err != nil || filepath.Base(path) != now.Format(backupLayout) {
		t.Fatal(path, err)
	}
	if _, err := Backup(ctx, st.Pool, dir, now); err == nil {
		t.Fatal("should refuse when a backup for the same moment exists")
	}
}
