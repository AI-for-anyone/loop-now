package store_test

import (
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/testutil/pgtest"
)

// Self-owned PG18 only. Set the explicit pgtest URL before its first lookup so
// testenv never falls back to .env.local or an existing database. No test DDL.
func constraintsOwnPG(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/opt/homebrew/opt/postgresql@18/bin/initdb"); err != nil {
		t.Skip("required fixed offline dependency unavailable: /opt/homebrew/opt/postgresql@18/bin/initdb")
	}
	root, err := os.MkdirTemp("/private/tmp", "ln-constraints-")
	if err != nil {
		t.Fatal(err)
	}
	data, socket := filepath.Join(root, "data"), filepath.Join(root, "socket")
	if err = os.Mkdir(socket, 0700); err != nil {
		t.Fatal(err)
	}
	command := func(name string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/opt/homebrew/opt/postgresql@18/bin/"+name, args...)
		cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "LC_ALL=C"}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run()
	}
	if err = command("initdb", "-D", data, "-U", "fixture", "-A", "trust", "--no-locale", "-E", "UTF8"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG init failed", err)
	}
	if err = command("pg_ctl", "-D", data, "-l", filepath.Join(root, "pg.log"), "-o", "-F -h '' -k "+socket+" -p 55433", "-w", "-t", "10", "start"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG start failed", err)
	}
	t.Cleanup(func() {
		if err := command("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "10", "stop"); err != nil {
			t.Errorf("own PG stop failed: %v", err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error("own PG cleanup failed")
		}
	})
	if err = command("createdb", "-h", socket, "-p", "55433", "-U", "fixture", "loop_now_test"); err != nil {
		t.Fatal("own test database creation failed", err)
	}
	t.Setenv(pgtest.EnvTestDatabaseURL, "postgres://fixture@/loop_now_test?host="+url.QueryEscape(socket)+"&port=55433&sslmode=disable")
}

type credentialConstraintCase struct {
	name, table, code, constraint, column, sql string
	args                                       []any
	deferred                                   bool
}
