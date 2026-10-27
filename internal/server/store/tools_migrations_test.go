package store_test

import (
	"context"
	"testing"
)

// An intentional relation collision occurs after each migration's first DDL.
// Both the preceding DDL and goose version insert must roll back together.
func TestToolsMigrationDDLFailureRollsBack(t *testing.T) {
	e := dbConnectionFixtureAt(t, 25)
	ctx := context.Background()
	if err := e.s.MigrateDownTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		version           int64
		collision, absent string
	}{
		{13, "agent_db_connection_members", "agent_db_connection_sets"},
		{14, "google_cli_grants", "project_cli_auth_operations"},
	} {
		if _, err := e.s.Pool.Exec(ctx, "CREATE TABLE "+c.collision+" (fixture integer)"); err != nil {
			t.Fatal(err)
		}
		if err := e.s.MigrateUpTo(ctx, c.version); err == nil {
			t.Fatal("migration collision accepted")
		}
		var absent bool
		if err := e.s.Pool.QueryRow(ctx, "SELECT to_regclass($1) IS NULL", c.absent).Scan(&absent); err != nil || !absent {
			t.Fatal("failed migration left DDL", err)
		}
		var version int64
		if err := e.s.Pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil || version != c.version-1 {
			t.Fatal("failed migration advanced ledger", err)
		}
		if c.version == 14 {
			var columns int
			if err := e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='project_cli_versions' AND column_name='auth_method'`).Scan(&columns); err != nil || columns != 0 {
				t.Fatal("failed 014 retained preceding ALTER", err)
			}
		}
		if _, err := e.s.Pool.Exec(ctx, "DROP TABLE "+c.collision); err != nil {
			t.Fatal(err)
		}
		if err := e.s.MigrateUpTo(ctx, c.version); err != nil {
			t.Fatal("clean retry failed", err)
		}
	}
	if err := e.s.MigrateDownTo(ctx, 12); err != nil {
		t.Fatal("empty 014/013 Down failed", err)
	}
	if err := e.s.MigrateUpTo(ctx, 14); err != nil {
		t.Fatal("empty 013/014 Up failed", err)
	}
}
