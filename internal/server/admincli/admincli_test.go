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
		c.t.Fatalf("loop-server %s：退出码 %d：%s", strings.Join(args, " "), code, errs)
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
		t.Fatalf("version 应打印服务端版本：%q", out)
	}
	if code, _, _ := c.run("identity"); code != 2 {
		t.Fatalf("缺少子命令时应报用法错误：%d", code)
	}
	// 数据目录里没有库时报错，不新建空库。
	empty := cli{t: t, dataDir: t.TempDir()}
	if code, _, errs := empty.run("identity", "list"); code != 2 || !strings.Contains(errs, "没有库") {
		t.Fatalf("数据目录里没有库时应报用法错误：%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(config.DBDir(empty.dataDir), "data.db")); !os.IsNotExist(err) {
		t.Fatalf("不应新建空库：%v", err)
	}

	// 外部身份：创建、重名、名称规则、列出。
	out, _ := c.ok("identity", "create", "ci-bot")
	if !strings.Contains(out, "@ci-bot") {
		t.Fatalf("创建外部身份：%s", out)
	}
	if code, _, errs := c.run("identity", "create", "ci-bot"); code != 1 || !strings.Contains(errs, "ci-bot") {
		t.Fatalf("重名应失败：%d %s", code, errs)
	}
	if code, _, _ := c.run("identity", "create", "Console"); code != 1 {
		t.Fatal("不合规的名称应失败")
	}
	if out, _ := c.ok("identity", "list"); !strings.Contains(out, "ci-bot") {
		t.Fatalf("列出外部身份：%s", out)
	}

	// 令牌：签发时只打印一次到标准输出；列表只有元数据；吊销后无效。
	tok, info := c.ok("token", "issue", "ci-bot", "--expires-in", "720h")
	tok = strings.TrimSpace(tok)
	if _, id, _, ok := auth.ParseToken(tok); !ok || !strings.HasPrefix(tok, "lne_") || !strings.Contains(info, id.String()) {
		t.Fatalf("签发的令牌：%q %q", tok, info)
	}
	forever, _ := c.ok("token", "issue", "--expires-in", "0s", "ci-bot")
	list, _ := c.ok("token", "list", "ci-bot")
	if strings.Contains(list, tok[12:]) || !strings.Contains(list, tok[:12]) || !strings.Contains(list, "永不过期") {
		t.Fatalf("令牌列表只有元数据：%s", list)
	}
	_, id, _, _ := auth.ParseToken(tok)
	a, err := st.LookupExternalToken(context.Background(), id)
	if err != nil || a.Token.ExpiresAt == nil || a.Token.ExpiresAt.Sub(time.Now()) < 719*time.Hour {
		t.Fatalf("有效期应为 720h：%+v %v", a.Token, err)
	}
	c.ok("token", "revoke", id.String())
	c.ok("token", "revoke", id.String()) // 重复吊销不报错
	if a, _ := st.LookupExternalToken(context.Background(), id); a.Token.RevokedAt == nil {
		t.Fatal("吊销后应带吊销时间")
	}
	if code, _, _ := c.run("token", "revoke", "not-a-uuid"); code != 1 {
		t.Fatal("令牌 ID 不合法时应失败")
	}

	// 删除身份：令牌全部吊销，名称可以再用。
	c.ok("identity", "delete", "ci-bot")
	_, fid, _, _ := auth.ParseToken(strings.TrimSpace(forever))
	if a, _ := st.LookupExternalToken(context.Background(), fid); a.Token.RevokedAt == nil {
		t.Fatal("删除身份后它的令牌应全部吊销")
	}
	if code, _, _ := c.run("token", "list", "ci-bot"); code != 1 {
		t.Fatal("已删除的身份不能列出令牌")
	}
	c.ok("identity", "create", "ci-bot")

	// 审计：操作者为 cli，没有令牌。
	audits, err := st.ListAudit(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]int{}
	for _, e := range audits {
		if e.ActorKind != "cli" || e.RequestID != nil {
			t.Fatalf("命令行的审计操作者应为 cli：%+v", e)
		}
		ops[e.Operation]++
		for _, v := range e.Target {
			if strings.HasPrefix(v, "lne_") {
				t.Fatalf("审计中不应有令牌：%+v", e)
			}
		}
	}
	if ops["identity.create"] != 3 || ops["token.issue"] != 2 || ops["token.revoke"] != 2 || ops["identity.delete"] != 1 {
		t.Fatalf("改变状态的命令都应记审计（含失败的）：%v", ops)
	}
}

// backup 在给定目录下写一份能打开的完整备份，服务开着库时也可以；目录参数缺失时报用法错误。
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
		t.Fatalf("缺少目录时应报用法错误：%d", code)
	}
	target := t.TempDir()
	out, _ := c.ok("backup", target)
	path := strings.TrimSpace(out)
	if filepath.Dir(path) != target {
		t.Fatalf("备份应在目标目录下：%q", path)
	}
	restored, err := db.Open(context.Background(), db.Options{Dir: path})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var projects int
	if err := restored.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&projects); err != nil || projects != 1 {
		t.Fatalf("备份内容不对：%d %v", projects, err)
	}
}

// import-postgres 只从 LOOP_NOW_DATABASE_URL 读源库：没设时报用法错误；连不上时失败，不留下库，也不打印连接参数。
func TestAdminImportPostgresRefusals(t *testing.T) {
	dataDir := t.TempDir()
	c := cli{t: t, dataDir: dataDir}
	if code, _, errs := c.run("import-postgres"); code != 2 || !strings.Contains(errs, pgimport.EnvSourceURL) {
		t.Fatalf("没有源库时应报用法错误：%d %s", code, errs)
	}
	c.env = map[string]string{pgimport.EnvSourceURL: "postgres://fixture-user:fixture-secret@127.0.0.1:1/fixture-db?connect_timeout=2"}
	if code, _, _ := c.run("import-postgres", "extra"); code != 2 {
		t.Fatalf("多余参数应报用法错误：%d", code)
	}
	code, out, errs := c.run("import-postgres")
	if code != 1 {
		t.Fatalf("连不上源库应失败：%d", code)
	}
	for _, s := range []string{"fixture-user", "fixture-secret", "fixture-db"} {
		if strings.Contains(out+errs, s) {
			t.Fatalf("输出里带了连接参数：%s", errs)
		}
	}
	if _, err := os.Stat(filepath.Join(config.DBDir(dataDir), db.DataFile)); !os.IsNotExist(err) {
		t.Fatal("失败的导入留下了库", err)
	}
}

// 成功导入后每张表一行“表名<TAB>行数”，新库能被其他子命令打开。
func TestAdminImportPostgres(t *testing.T) {
	pool, src := pgimporttest.Source(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO external_identities(id,name) VALUES($1,'imported-ci')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	c := cli{t: t, dataDir: t.TempDir(), env: map[string]string{pgimport.EnvSourceURL: src}}
	out, _ := c.ok("import-postgres")
	if !strings.Contains(out, "external_identities\t1\n") {
		t.Fatalf("输出里没有导入的行数：%s", out)
	}
	if list, _ := c.ok("identity", "list"); !strings.Contains(list, "imported-ci") {
		t.Fatalf("导入的身份不在新库里：%s", list)
	}
}
