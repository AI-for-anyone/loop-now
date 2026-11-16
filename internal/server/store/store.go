// Package store is the server's access layer to SQLite (through internal/server/db): migrations at startup, and reads
// and writes of the business tables.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store/schema"
)

// Store holds the connection pool.
type Store struct {
	ImageMaxCount int
	// sleepScan tracks a bounded round of due candidates; it is not sleep authority.
	sleepScan sleepScanProgress
	// TaskCommitted wakes the event stream relay after a task transaction commits; configured at startup and not
	// changed while running.
	TaskCommitted func()
	// VerificationInvalidated runs only after a credential/project mutation commits.
	VerificationInvalidated func(uuid.UUID, *uuid.UUID)
	// Import hooks are configured only by an App that assembled schema017 import.
	// They are not allow-missing-table fallbacks and never open a nested tx.
	CLIImportDeletingTx func(context.Context, *db.Tx, uuid.UUID) error
	CLIImportDeleted    func(uuid.UUID)
	Pool                *db.DB
	// InstanceID is written into run record events to show which server instance recorded them (reserved for multiple
	// instances); nothing is recorded when it is nil.
	InstanceID *uuid.UUID
	// pluginWake is called after a transaction that wrote plugin events commits; see SetPluginEventsCommitted.
	pluginWake atomic.Pointer[func()]
}

// Open opens the databases in the data directory. The caller runs migrations separately with Migrate.
func Open(ctx context.Context, o db.Options) (*Store, error) {
	d, err := db.Open(ctx, o)
	if err != nil {
		return nil, err
	}
	return New(d), nil
}

// New builds a Store on already opened databases, and installs the hook that runs before each write transaction
// commits.
func New(d *db.DB) *Store {
	s := &Store{Pool: d}
	d.SetWriteHook(s.beforeCommit)
	return s
}

// Close closes the connection pool.
func (s *Store) Close() { _ = s.Pool.Close() }

// Ping is used for health checks.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// Migrate runs the migrations that have not run yet, in order. Each migration runs in one write transaction, and on
// failure the database stays in its state before that migration.
func (s *Store) Migrate(ctx context.Context, logger *slog.Logger) error {
	return schema.Migrate(ctx, s.Pool, logger)
}

// MigrateTo runs only the migrations up to and including version.
func (s *Store) MigrateTo(ctx context.Context, logger *slog.Logger, version int64) error {
	return schema.MigrateTo(ctx, s.Pool, logger, version)
}

// uniqueTargets maps the PostgreSQL constraint names still used in the code to the table and columns SQLite reports
// for a unique violation: for plain unique constraints and partial unique indexes SQLite reports only the columns, not
// the constraint name. Register new name-based checks here.
var uniqueTargets = map[string]struct {
	table string
	cols  []string
}{
	"agents_name_per_project":               {"agents", []string{"project_id", "name"}},
	"agents_name_platform":                  {"agents", []string{"name"}},
	"cli_credential_import_operations_pkey": {"cli_credential_import_operations", []string{"id"}},
	"external_identities_name_live":         {"external_identities", []string{"name"}},
	"model_auth_operations_one_active":      {"model_auth_operations", []string{"agent_id"}},
	"plugins_pkey":                          {"plugins", []string{"id"}},
	"projects_name_live":                    {"projects", []string{"organization_id", "name"}},
	"runs_one_active_per_agent":             {"runs", []string{"agent_id"}},
}

// isUniqueViolation reports whether the error is a unique violation of the given constraint; an empty constraint
// matches any unique violation.
func isUniqueViolation(err error, constraint string) bool {
	if constraint == "" {
		return db.IsAnyUniqueViolation(err)
	}
	target, ok := uniqueTargets[constraint]
	if !ok {
		panic(fmt.Sprintf("store: 唯一约束 %s 没有登记在 uniqueTargets", constraint))
	}
	return db.IsUniqueViolation(err, target.table, target.cols...)
}

// inTx runs fn in a write transaction.
func (s *Store) inTx(ctx context.Context, fn func(*db.Tx) error) error {
	return s.Pool.InTx(ctx, fn)
}
