// Package sqlitetest gives component tests an isolated SQLite environment: each call creates a separate data
// directory under t.TempDir(), which is closed at the end of the test and removed with the temporary directory. No
// external service is needed, tests do not affect each other, and t.Parallel() is safe.
//
// The fixture turns on db.DetectNestedWrites: writing through *db.DB by mistake inside a write transaction callback
// fails immediately instead of waiting for a timeout.
package sqlitetest

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/server/store/schema"
)

func init() { db.DetectNestedWrites.Store(true) }

// NewDB returns a database with all migrations applied (about 20ms as a baseline, so no template copy is needed).
func NewDB(t testing.TB) *db.DB {
	t.Helper()
	d := open(t, t.TempDir())
	if err := schema.Migrate(context.Background(), d, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}
	return d
}

// NewStore returns a migrated Store with its own data directory.
func NewStore(t testing.TB) *store.Store {
	t.Helper()
	return store.New(NewDB(t))
}

// NewStoreIn opens and migrates a Store in the given data directory, for tests that need to reopen the database in
// the same directory; the caller is responsible for closing it.
func NewStoreIn(t testing.TB, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), db.Options{Dir: dir})
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	if err := s.Migrate(context.Background(), slog.New(slog.DiscardHandler)); err != nil {
		s.Close()
		t.Fatalf("migrate the test database: %v", err)
	}
	return s
}

// NewEmptyDB returns a database without any tables, for tests that only test the wrapper layer or create their own
// tables.
func NewEmptyDB(t testing.TB) *db.DB {
	t.Helper()
	return open(t, t.TempDir())
}

func open(t testing.TB, dir string) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), db.Options{Dir: dir})
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close the test database: %v", err)
		}
	})
	return d
}

// HoldWriter occupies the single write connection inside a write transaction; it is already held when HoldWriter
// returns. Calling the returned function releases it (it may be called more than once).
// Tests use it to make concurrent writes stop at "waiting for the write connection", replacing the approach of
// holding a row lock from the PostgreSQL days. It is released automatically at the end of the test.
func HoldWriter(t testing.TB, d *db.DB) (release func()) {
	t.Helper()
	held, done := make(chan struct{}), make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		ended <- d.InTx(context.Background(), func(*db.Tx) error {
			close(held)
			<-done
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-ended:
		t.Fatalf("hold the write connection: %v", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			close(done)
			if err := <-ended; err != nil {
				t.Errorf("release the write connection: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
}

// WaitWriteWaiters waits until at least n calls are waiting for the write connection (it fails when ctx ends or
// after 5 seconds), to confirm that concurrent writes really have reached the waiting point rather than not having
// started yet.
func WaitWriteWaiters(t testing.TB, ctx context.Context, d *db.DB, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for d.WriteWaiters() < n {
		if ctx.Err() != nil || time.Now().After(deadline) {
			t.Fatalf("fewer than %d writes are waiting for the write connection (now %d)", n, d.WriteWaiters())
		}
		time.Sleep(time.Millisecond)
	}
}
