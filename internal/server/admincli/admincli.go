// Package admincli 实现服务端命令行的管理子命令：外部身份与外部令牌的管理、手动备份，以及打印版本号。
// 子命令直接读写数据库（与服务同一个数据目录，SQLite 支持多进程短写）；改变状态的操作记审计，操作者为 cli。
//
//	loop-server version
//	loop-server identity create <名称>
//	loop-server identity list
//	loop-server identity delete <名称>
//	loop-server token issue <身份名称> [--expires-in 720h]
//	loop-server token list <身份名称>
//	loop-server token revoke <令牌 ID>
//	loop-server backup <目录>
package admincli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/config"
	"loop-now/internal/server/db"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/gcloudcli"
	"loop-now/internal/server/identity"
	"loop-now/internal/server/maintenance"
	"loop-now/internal/server/pgimport"
	"loop-now/internal/server/store"

	"path/filepath"
)

// Commands 是全部管理子命令的名称；第一个参数是其中之一时，loop-server 执行子命令而不是启动服务。
var Commands = []string{"version", "identity", "token", "backup", "import-postgres"}

// IsCommand 报告参数是否为管理子命令。
func IsCommand(arg string) bool {
	if arg == "internal-db-cli" || arg == "internal-gcloud-cli" {
		return true
	}
	for _, c := range Commands {
		if arg == c {
			return true
		}
	}
	return false
}

// 退出码：0 成功，1 执行失败，2 用法错误。
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

const usage = `用法：
  loop-server version
  loop-server identity create <名称>
  loop-server identity list
  loop-server identity delete <名称>
  loop-server token issue <身份名称> [--expires-in 720h]
  loop-server token list <身份名称>
  loop-server token revoke <令牌 ID>
  loop-server backup <目录>        在目录下以当前时刻命名的子目录里写一份完整备份（服务运行中也可以）
  loop-server import-postgres      把 LOOP_NOW_DATABASE_URL 指向的 PostgreSQL 库一次性导入到数据目录的新库
                                   （源库须已由最后一个 PostgreSQL 版本完成迁移，服务须已停下）
`

// Options 是运行子命令所需的环境。库在 LOOP_NOW_DATA_DIR（默认 HOME 下的 .loop_now）的 db 子目录。
type Options struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
}

// Run 执行子命令，返回退出码。签发的令牌只打印一次到标准输出，其余提示写到标准错误。
func Run(ctx context.Context, args []string, o Options) int {
	if len(args) > 0 && args[0] == "internal-gcloud-cli" {
		return gcloudcli.Run(ctx, args[1:], gcloudcli.Options{Getenv: o.Getenv, Stdin: os.Stdin, Stdout: o.Stdout, Stderr: o.Stderr})
	}
	if len(args) > 0 && args[0] == "internal-db-cli" {
		return dbcli.Run(ctx, args[1:], dbcli.Options{Getenv: o.Getenv, Stdin: os.Stdin, Stdout: o.Stdout, Stderr: o.Stderr})
	}
	if len(args) == 0 || !IsCommand(args[0]) {
		fmt.Fprint(o.Stderr, usage)
		return exitUsage
	}
	if args[0] == "version" {
		fmt.Fprintln(o.Stdout, buildinfo.Version())
		return exitOK
	}
	if args[0] == "import-postgres" {
		return importPostgres(ctx, args, o)
	}
	if len(args) < 2 {
		fmt.Fprint(o.Stderr, usage)
		return exitUsage
	}
	dbDir := config.DBDir(dataDir(o))
	// 只打开服务已经建好的库：数据目录写错时不能悄悄建一个空库。
	if _, err := os.Stat(filepath.Join(dbDir, db.DataFile)); err != nil {
		fmt.Fprintf(o.Stderr, "数据目录 %s 里没有库（%s 设置对了吗？）\n", dbDir, config.EnvDataDir)
		return exitUsage
	}
	st, err := store.Open(ctx, db.Options{Dir: dbDir})
	if err != nil {
		fmt.Fprintln(o.Stderr, "打开数据库失败：", err)
		return exitFail
	}
	defer st.Close()
	if args[0] == "backup" {
		if len(args) != 2 {
			fmt.Fprint(o.Stderr, usage)
			return exitUsage
		}
		dir, err := filepath.Abs(args[1])
		if err != nil {
			fmt.Fprintln(o.Stderr, err)
			return exitFail
		}
		path, err := maintenance.Backup(ctx, st.Pool, dir, time.Now())
		if err != nil {
			fmt.Fprintln(o.Stderr, "备份失败：", err)
			return exitFail
		}
		fmt.Fprintln(o.Stdout, path)
		return exitOK
	}
	c := &cmd{ctx: ctx, st: st, o: o}
	var run func([]string) error
	switch args[0] + " " + args[1] {
	case "identity create":
		run = c.identityCreate
	case "identity list":
		run = c.identityList
	case "identity delete":
		run = c.identityDelete
	case "token issue":
		run = c.tokenIssue
	case "token list":
		run = c.tokenList
	case "token revoke":
		run = c.tokenRevoke
	default:
		fmt.Fprint(o.Stderr, usage)
		return exitUsage
	}
	if err := run(args[2:]); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(o.Stderr, usage)
			return exitUsage
		}
		if e, ok := apperr.As(err); ok {
			fmt.Fprintln(o.Stderr, e.Message)
			for _, f := range e.Fields {
				fmt.Fprintf(o.Stderr, "  %s：%s\n", f.Field, f.Message)
			}
		} else {
			fmt.Fprintln(o.Stderr, err)
		}
		return exitFail
	}
	return exitOK
}

var errUsage = errors.New("用法错误")

func dataDir(o Options) string {
	if d := o.Getenv(config.EnvDataDir); d != "" {
		return d
	}
	return config.DefaultDataDir(o.Getenv)
}

// importPostgres 执行一次性导入：源库来自 pgimport.EnvSourceURL，目标是数据目录里还不存在的新库。
// 只打印表名与行数，不打印连接串与任何行内容。
func importPostgres(ctx context.Context, args []string, o Options) int {
	if len(args) != 1 {
		fmt.Fprint(o.Stderr, usage)
		return exitUsage
	}
	src := o.Getenv(pgimport.EnvSourceURL)
	if src == "" {
		fmt.Fprintf(o.Stderr, "设置 %s 为原来的 PostgreSQL 库的连接串再导入\n", pgimport.EnvSourceURL)
		return exitUsage
	}
	dbDir := config.DBDir(dataDir(o))
	report, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dbDir, Log: slog.New(slog.NewTextHandler(o.Stderr, nil))})
	if err != nil {
		fmt.Fprintln(o.Stderr, "导入失败（新库没有留下）：", err)
		return exitFail
	}
	var rows int64
	for _, t := range report.Tables {
		fmt.Fprintf(o.Stdout, "%s\t%d\n", t.Name, t.Rows)
		rows += t.Rows
	}
	fmt.Fprintf(o.Stderr, "导入完成：源库迁移版本 %d，%d 张表，共 %d 行，新库在 %s\n", report.Version, len(report.Tables), rows, dbDir)
	return exitOK
}

type cmd struct {
	ctx context.Context
	st  *store.Store
	o   Options
}

// audit 记录一次改变状态的操作；status 为 0 表示成功，1 表示失败。审计写入失败时提示但不改变结果。
func (c *cmd) audit(op string, target map[string]string, err error) {
	status := 0
	if err != nil {
		status = 1
	}
	if aerr := c.st.InsertAudit(context.WithoutCancel(c.ctx), store.AuditEntry{ActorKind: "cli", ActorAddress: "cli", Method: "CLI",
		Operation: op, Target: target, Status: status}); aerr != nil {
		fmt.Fprintln(c.o.Stderr, "写入审计失败：", aerr)
	}
}

func oneArg(args []string) (string, error) {
	if len(args) != 1 || args[0] == "" {
		return "", errUsage
	}
	return args[0], nil
}

func (c *cmd) identityCreate(args []string) error {
	name, err := oneArg(args)
	if err != nil {
		return err
	}
	if msg := identity.ValidateName(name); msg != "" {
		return apperr.Invalid("name", msg)
	}
	x, err := c.st.CreateExternalIdentity(c.ctx, name)
	target := map[string]string{"name": name}
	if err == nil {
		target["identityId"] = x.ID.String()
	}
	c.audit("identity.create", target, err)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.o.Stdout, "%s\t@%s\t%s\n", x.ID, x.Name, x.CreatedAt.Format(time.RFC3339))
	return nil
}

func (c *cmd) identityList(args []string) error {
	if len(args) != 0 {
		return errUsage
	}
	xs, err := c.st.ListExternalIdentities(c.ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.o.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t名称\t创建时间")
	for _, x := range xs {
		fmt.Fprintf(w, "%s\t%s\t%s\n", x.ID, x.Name, x.CreatedAt.Format(time.RFC3339))
	}
	return w.Flush()
}

func (c *cmd) identityDelete(args []string) error {
	name, err := oneArg(args)
	if err != nil {
		return err
	}
	x, err := c.st.GetLiveExternalIdentityByName(c.ctx, name)
	if err == nil {
		_, err = c.st.DeleteExternalIdentity(c.ctx, x.ID)
	}
	c.audit("identity.delete", map[string]string{"name": name, "identityId": idOrEmpty(x.ID)}, err)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.o.Stderr, "已删除外部身份 @%s，它的令牌已全部吊销\n", name)
	return nil
}

func idOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func (c *cmd) tokenIssue(args []string) error {
	fs := flag.NewFlagSet("token issue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	expiresIn := fs.Duration("expires-in", 0, "有效期，例如 720h；不传表示永不过期")
	var name string
	// 身份名称可以写在选项前或后。
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() != 0 || name == "" {
		return errUsage
	}
	if *expiresIn < 0 {
		return apperr.Invalid("expires-in", "有效期必须大于 0")
	}
	x, err := c.st.GetLiveExternalIdentityByName(c.ctx, name)
	if err != nil {
		c.audit("token.issue", map[string]string{"name": name}, err)
		return err
	}
	var expiresAt *time.Time
	if *expiresIn > 0 {
		at := time.Now().Add(*expiresIn)
		expiresAt = &at
	}
	t, secret, err := identity.IssueToken(c.ctx, c.st, x.ID, expiresAt)
	target := map[string]string{"name": name, "identityId": x.ID.String()}
	if err == nil {
		target["tokenId"] = t.ID.String()
	}
	c.audit("token.issue", target, err)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.o.Stdout, secret)
	exp := "永不过期"
	if t.ExpiresAt != nil {
		exp = "过期时间 " + t.ExpiresAt.Format(time.RFC3339)
	}
	fmt.Fprintf(c.o.Stderr, "已为 @%s 签发令牌 %s（前缀 %s，%s）。令牌只显示这一次，请妥善保存。\n", name, t.ID, t.Prefix, exp)
	return nil
}

func (c *cmd) tokenList(args []string) error {
	name, err := oneArg(args)
	if err != nil {
		return err
	}
	x, err := c.st.GetLiveExternalIdentityByName(c.ctx, name)
	if err != nil {
		return err
	}
	ts, err := c.st.ListExternalTokens(c.ctx, x.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.o.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t前缀\t创建时间\t过期时间\t吊销时间\t最近使用")
	for _, t := range ts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Prefix, t.CreatedAt.Format(time.RFC3339), timeOr(t.ExpiresAt, "永不过期"),
			timeOr(t.RevokedAt, "-"), timeOr(t.LastUsedAt, "-"))
	}
	return w.Flush()
}

func timeOr(t *time.Time, empty string) string {
	if t == nil {
		return empty
	}
	return t.Format(time.RFC3339)
}

func (c *cmd) tokenRevoke(args []string) error {
	raw, err := oneArg(args)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return apperr.Invalid("tokenId", "不是合法的令牌 ID")
	}
	a, err := c.st.LookupExternalToken(c.ctx, id)
	if err == nil && a.Identity.DeletedAt == nil {
		_, err = c.st.RevokeExternalToken(c.ctx, a.Identity.ID, id)
	}
	c.audit("token.revoke", map[string]string{"tokenId": id.String(), "identityId": idOrEmpty(a.Identity.ID)}, err)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.o.Stderr, "令牌 %s 已吊销\n", id)
	return nil
}
