package admincli_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/admincli"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type cli struct {
	t      *testing.T
	schema string
	url    string
}

func (c cli) run(args ...string) (code int, stdout, stderr string) {
	c.t.Helper()
	var out, errb bytes.Buffer
	code = admincli.Run(context.Background(), args, admincli.Options{
		Getenv: func(k string) string {
			if k == config.EnvDatabaseURL {
				return c.url
			}
			return ""
		},
		Stdout: &out, Stderr: &errb, SearchPath: c.schema,
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
	schema := pgtest.NewSchema(t)
	st, err := store.Open(context.Background(), pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	c := cli{t: t, schema: schema, url: pgtest.URL(t)}

	if out, _ := c.ok("version"); strings.TrimSpace(out) != buildinfo.Version() {
		t.Fatalf("version 应打印服务端版本：%q", out)
	}
	if code, _, _ := c.run("identity"); code != 2 {
		t.Fatalf("缺少子命令时应报用法错误：%d", code)
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
