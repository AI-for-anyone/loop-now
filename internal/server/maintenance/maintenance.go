// Package maintenance maintains the local databases on a schedule:
//   - a passive checkpoint every 5 minutes, logging a warning when a WAL exceeds the threshold;
//   - once a day a truncating checkpoint, PRAGMA optimize, deleting logs older than 90 days from events.db, and a
//     backup with VACUUM INTO, keeping only the most recent few.
//
// Daily maintenance is scheduled from the time of the last backup: it is not skipped when the service restarts often,
// and it is not repeated right after a backup.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// backupLayout is the name of a backup subdirectory (a UTC time), so lexicographic order is time order.
const backupLayout = "2006-01-02T150405Z"

// partialPrefix is the prefix of a backup directory still being written; it is renamed to its final name only once
// complete, and deleted on failure.
const partialPrefix = ".partial-"

// Options configures maintenance; zero fields take their defaults (tests can make them smaller).
type Options struct {
	BackupDir  string
	BackupKeep int

	CheckpointInterval time.Duration // interval of passive checkpoints, default 5 minutes
	DailyInterval      time.Duration // interval of daily maintenance, default 24 hours
	StartDelay         time.Duration // minimum wait after start before the first daily maintenance, default 1 minute
	LogRetention       time.Duration // how long events.db logs are kept, default 90 days
	WALWarnBytes       int64         // warn when a single WAL exceeds this many bytes, default db.WALWarnBytes
	PruneBatch         int           // maximum rows deleted per delete transaction, default 10000
	Now                func() time.Time
}

func (o *Options) defaults() {
	if o.CheckpointInterval <= 0 {
		o.CheckpointInterval = 5 * time.Minute
	}
	if o.DailyInterval <= 0 {
		o.DailyInterval = 24 * time.Hour
	}
	if o.StartDelay <= 0 {
		o.StartDelay = time.Minute
	}
	if o.LogRetention <= 0 {
		o.LogRetention = 90 * 24 * time.Hour
	}
	if o.WALWarnBytes <= 0 {
		o.WALWarnBytes = db.WALWarnBytes
	}
	if o.PruneBatch <= 0 {
		o.PruneBatch = 10000
	}
	if o.BackupKeep <= 0 {
		o.BackupKeep = 7
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Service runs maintenance in the background.
type Service struct {
	st     *store.Store
	o      Options
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex // daily maintenance and manual triggers are mutually exclusive
}

// Start starts background maintenance.
func Start(st *store.Store, o Options, log *slog.Logger) *Service {
	o.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{st: st, o: o, log: log, cancel: cancel, done: make(chan struct{})}
	go s.loop(ctx)
	return s
}

// Stop stops background maintenance and waits for the step in progress to finish or for ctx to expire.
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	checkpoint := time.NewTicker(s.o.CheckpointInterval)
	defer checkpoint.Stop()
	daily := time.NewTimer(s.untilDaily())
	defer daily.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-checkpoint.C:
			s.Passive(ctx)
		case <-daily.C:
			if err := s.Daily(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("daily database maintenance failed", "err", err)
			}
			daily.Reset(s.untilDaily())
		}
	}
}

// untilDaily returns the time until the next daily maintenance: one full interval after the last backup, and at least
// StartDelay.
func (s *Service) untilDaily() time.Duration {
	wait := s.o.StartDelay
	if last, ok := lastBackup(s.o.BackupDir); ok {
		if due := last.Add(s.o.DailyInterval).Sub(s.o.Now()); due > wait {
			wait = due
		}
	}
	return wait
}

// Passive runs one passive checkpoint and logs a warning when a WAL is too large.
func (s *Service) Passive(ctx context.Context) {
	if _, err := s.st.Pool.Checkpoint(ctx, db.CheckpointPassive); err != nil && ctx.Err() == nil {
		s.log.Warn("passive checkpoint failed", "err", err)
	}
	s.warnWAL()
}

func (s *Service) warnWAL() {
	data, events := s.st.Pool.WALSize()
	if data > s.o.WALWarnBytes || events > s.o.WALWarnBytes {
		s.log.Warn("WAL is too large: a long-running read transaction may be blocking checkpoints", "dataWALBytes", data, "eventsWALBytes", events, "thresholdBytes", s.o.WALWarnBytes)
	}
}

// Daily runs one daily maintenance: truncating checkpoint, optimize, deleting old logs, and a backup keeping only the
// most recent few. A failed step does not affect the later steps; the errors of all steps are joined and returned at
// the end.
func (s *Service) Daily(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	results, err := s.st.Pool.Checkpoint(ctx, db.CheckpointTruncate)
	if err != nil {
		errs = append(errs, fmt.Errorf("truncate checkpoint: %w", err))
	}
	for _, r := range results {
		if r.Busy {
			s.log.Warn("truncate checkpoint did not complete: a read transaction is holding an old snapshot", "schema", r.Schema, "logFrames", r.LogFrames, "checkpointed", r.Checkpointed)
		}
	}
	if err := s.st.Pool.Optimize(ctx); err != nil {
		errs = append(errs, fmt.Errorf("optimize: %w", err))
	}
	before := s.o.Now().Add(-s.o.LogRetention)
	if n, err := s.st.PruneEventLogs(ctx, before, s.o.PruneBatch); err != nil {
		errs = append(errs, fmt.Errorf("prune old logs: %w", err))
	} else if n > 0 {
		s.log.Info("pruned old logs", "rows", n, "before", before)
	}
	if path, err := Backup(ctx, s.st.Pool, s.o.BackupDir, s.o.Now()); err != nil {
		errs = append(errs, fmt.Errorf("back up the databases: %w", err))
	} else {
		s.log.Info("backed up the database", "dir", path)
		if err := prune(s.o.BackupDir, s.o.BackupKeep); err != nil {
			errs = append(errs, fmt.Errorf("prune old backups: %w", err))
		}
	}
	s.warnWAL()
	return errors.Join(errs...)
}

// Backup backs up both databases into a new subdirectory of dir named by time (data.db, events.db) and returns that
// subdirectory. It writes to a temporary directory first and renames it to the final name only once fully written; on
// failure the temporary directory is deleted. It can be called while the service is running.
func Backup(ctx context.Context, d *db.DB, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := now.UTC().Format(backupLayout)
	final := filepath.Join(dir, name)
	if _, err := os.Stat(final); err == nil {
		return "", fmt.Errorf("backup %s already exists", final)
	}
	partial := filepath.Join(dir, partialPrefix+name)
	if err := os.Mkdir(partial, 0o700); err != nil {
		return "", err
	}
	if err := d.BackupTo(ctx, partial); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	return final, nil
}

// backups returns the names of the final backup subdirectories under dir, oldest first.
func backups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), partialPrefix) {
			continue
		}
		if _, err := time.Parse(backupLayout, e.Name()); err == nil {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func lastBackup(dir string) (time.Time, bool) {
	names, err := backups(dir)
	if err != nil || len(names) == 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(backupLayout, names[len(names)-1])
	return t, err == nil
}

// prune keeps only the newest keep backups; it does not touch directories and files whose names are not backup times.
func prune(dir string, keep int) error {
	names, err := backups(dir)
	if err != nil {
		return err
	}
	var errs []error
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(dir, names[0])); err != nil {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}
