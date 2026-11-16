package admincli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/admincli"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/db"
	"loop-now/internal/server/pgimport"
	"loop-now/internal/server/pgimport/pgimporttest"
	"loop-now/internal/testutil/sqlitetest"

	"os"

	"path/filepath"

	"github.com/google/uuid"
)

type cli struct {
	t       *testing.T
	dataDir string
	env     map[string]string
}

func (c cli) run(args ...string) (code int, stdout, stderr string) {
	c.t.Helper()
	var out, errb bytes.Buffer
	code = admincli.Run(context.Background(), args, admincli.Options{
		Getenv: func(k string) string {
			if k == config.EnvDataDir {
				return c.dataDir
			}
			return c.env[k]
		},
		Stdout: &out, Stderr: &errb,
	})
	return code, out.String(), errb.String()
}

func (c cli) ok(args ...string) (stdout, stderr string) {
	c.t.Helper()
	code, out, errs := c.run(args...)
	if code != 0 {
		c.t.Fatalf("loop-server %s: exit code %d: %s", strings.Join(args, " "), code, errs)
	}
	return out, errs
}

func TestAdminCommands(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(config.DBDir(dataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	st := sqlitetest.NewStoreIn(t, config.DBDir(dataDir))
	t.Cleanup(st.Close)
	c := cli{t: t, dataDir: dataDir}

	if out, _ := c.ok("version"); strings.TrimSpace(out) != buildinfo.Version() {
		t.Fatalf("version should print the server version: %q", out)
	}
	if code, _, _ := c.run("identity"); code != 2 {
		t.Fatalf("a missing subcommand should be a usage error: %d", code)
	}
	// With no database in the data directory it reports an error instead of creating an empty one.
	empty := cli{t: t, dataDir: t.TempDir()}
	if code, _, errs := empty.run("identity", "list"); code != 2 || !strings.Contains(errs, "no database") {
		t.Fatalf("no database in the data directory should be a usage error: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(config.DBDir(empty.dataDir), "data.db")); !os.IsNotExist(err) {
		t.Fatalf("should not create an empty database: %v", err)
	}

	// External identities: create, duplicate name, name rules, list.
	out, _ := c.ok("identity", "create", "ci-bot")
	if !strings.Contains(out, "@ci-bot") {
		t.Fatalf("create external identity: %s", out)
	}
	if code, _, errs := c.run("identity", "create", "ci-bot"); code != 1 || !strings.Contains(errs, "ci-bot") {
		t.Fatalf("a duplicate name should fail: %d %s", code, errs)
	}
	if code, _, _ := c.run("identity", "create", "Console"); code != 1 {
		t.Fatal("an invalid name should fail")
	}
	if out, _ := c.ok("identity", "list"); !strings.Contains(out, "ci-bot") {
		t.Fatalf("list external identities: %s", out)
	}

	// Tokens: printed to standard output exactly once when issued; the list has only metadata; invalid once revoked.
	tok, info := c.ok("token", "issue", "ci-bot", "--expires-in", "720h")
	tok = strings.TrimSpace(tok)
	if _, id, _, ok := auth.ParseToken(tok); !ok || !strings.HasPrefix(tok, "lne_") || !strings.Contains(info, id.String()) {
		t.Fatalf("issued token: %q %q", tok, info)
	}
	forever, _ := c.ok("token", "issue", "--expires-in", "0s", "ci-bot")
	list, _ := c.ok("token", "list", "ci-bot")
	if strings.Contains(list, tok[12:]) || !strings.Contains(list, tok[:12]) || !strings.Contains(list, "never") {
		t.Fatalf("the token list should hold only metadata: %s", list)
	}
	_, id, _, _ := auth.ParseToken(tok)
	a, err := st.LookupExternalToken(context.Background(), id)
	if err != nil || a.Token.ExpiresAt == nil || a.Token.ExpiresAt.Sub(time.Now()) < 719*time.Hour {
		t.Fatalf("the lifetime should be 720h: %+v %v", a.Token, err)
	}
	c.ok("token", "revoke", id.String())
	c.ok("token", "revoke", id.String()) // revoking again is not an error
	if a, _ := st.LookupExternalToken(context.Background(), id); a.Token.RevokedAt == nil {
		t.Fatal("should have a revocation time after revoking")
	}
	if code, _, _ := c.run("token", "revoke", "not-a-uuid"); code != 1 {
		t.Fatal("an invalid token ID should fail")
	}

	// Deleting an identity: all its tokens are revoked and the name can be reused.
	c.ok("identity", "delete", "ci-bot")
	_, fid, _, _ := auth.ParseToken(strings.TrimSpace(forever))
	if a, _ := st.LookupExternalToken(context.Background(), fid); a.Token.RevokedAt == nil {
		t.Fatal("deleting an identity should revoke all its tokens")
	}
	if code, _, _ := c.run("token", "list", "ci-bot"); code != 1 {
		t.Fatal("listing tokens of a deleted identity should fail")
	}
	c.ok("identity", "create", "ci-bot")

	// Audit: the actor is cli, with no token.
	audits, err := st.ListAudit(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]int{}
	for _, e := range audits {
		if e.ActorKind != "cli" || e.RequestID != nil {
			t.Fatalf("the audit actor of the command line should be cli: %+v", e)
		}
		ops[e.Operation]++
		for _, v := range e.Target {
			if strings.HasPrefix(v, "lne_") {
				t.Fatalf("the audit should contain no token: %+v", e)
			}
		}
	}
	if ops["identity.create"] != 3 || ops["token.issue"] != 2 || ops["token.revoke"] != 2 || ops["identity.delete"] != 1 {
		t.Fatalf("every state-changing command should be audited (failed ones too): %v", ops)
	}
}

// TestAdminBackup: backup writes a complete backup that can be opened into the given directory, even while the server
// has the database open; a missing directory argument is a usage error.
func TestAdminBackup(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(config.DBDir(dataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	st := sqlitetest.NewStoreIn(t, config.DBDir(dataDir))
	t.Cleanup(st.Close)
	if _, err := st.EnsureDefaults(context.Background(), "o", "p"); err != nil {
		t.Fatal(err)
	}
	c := cli{t: t, dataDir: dataDir}
	if code, _, _ := c.run("backup"); code != 2 {
		t.Fatalf("a missing directory should be a usage error: %d", code)
	}
	target := t.TempDir()
	out, _ := c.ok("backup", target)
	path := strings.TrimSpace(out)
	if filepath.Dir(path) != target {
		t.Fatalf("the backup should be in the target directory: %q", path)
	}
	restored, err := db.Open(context.Background(), db.Options{Dir: path})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var projects int
	if err := restored.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&projects); err != nil || projects != 1 {
		t.Fatalf("unexpected backup contents: %d %v", projects, err)
	}
}

// TestAdminImportPostgresRefusals: import-postgres reads the source database only from LOOP_NOW_DATABASE_URL: unset is
// a usage error; when it cannot connect it fails, leaves no database behind and does not print connection parameters.
func TestAdminImportPostgresRefusals(t *testing.T) {
	dataDir := t.TempDir()
	c := cli{t: t, dataDir: dataDir}
	if code, _, errs := c.run("import-postgres"); code != 2 || !strings.Contains(errs, pgimport.EnvSourceURL) {
		t.Fatalf("no source database should be a usage error: %d %s", code, errs)
	}
	c.env = map[string]string{pgimport.EnvSourceURL: "postgres://fixture-user:fixture-secret@127.0.0.1:1/fixture-db?connect_timeout=2"}
	if code, _, _ := c.run("import-postgres", "extra"); code != 2 {
		t.Fatalf("extra arguments should be a usage error: %d", code)
	}
	code, out, errs := c.run("import-postgres")
	if code != 1 {
		t.Fatalf("an unreachable source database should fail: %d", code)
	}
	for _, s := range []string{"fixture-user", "fixture-secret", "fixture-db"} {
		if strings.Contains(out+errs, s) {
			t.Fatalf("the output contains connection parameters: %s", errs)
		}
	}
	if _, err := os.Stat(filepath.Join(config.DBDir(dataDir), db.DataFile)); !os.IsNotExist(err) {
		t.Fatal("a failed import left a database behind", err)
	}
}

// TestAdminImportPostgres: after a successful import there is one line per table, "table name<TAB>row count", and the
// new database can be opened by the other subcommands.
func TestAdminImportPostgres(t *testing.T) {
	pool, src := pgimporttest.Source(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO external_identities(id,name) VALUES($1,'imported-ci')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	c := cli{t: t, dataDir: t.TempDir(), env: map[string]string{pgimport.EnvSourceURL: src}}
	out, _ := c.ok("import-postgres")
	if !strings.Contains(out, "external_identities\t1\n") {
		t.Fatalf("the output lacks the imported row counts: %s", out)
	}
	if list, _ := c.ok("identity", "list"); !strings.Contains(list, "imported-ci") {
		t.Fatalf("the imported identity is not in the new database: %s", list)
	}
}
