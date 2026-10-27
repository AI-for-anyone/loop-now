package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

// System 是 loopctl 与外界的全部接触面，测试可以整体替换。
type System struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Getenv    func(string) string
	HomeDir   func() (string, error)
	Version   string
	GOOS      string
	GOARCH    string
	GoVersion string
	Dial      conn.DialFunc                                    // 为空时真实拨号
	Sleep     func(ctx context.Context, d time.Duration) error // 为空时真实等待
}

// OSSystem 返回使用真实进程环境的 System。
func OSSystem(version string) *System {
	return &System{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Getenv:    os.Getenv,
		HomeDir:   os.UserHomeDir,
		Version:   version,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
	}
}

// ConfigPath 返回本地连接配置文件的路径。
func (s *System) ConfigPath() (string, *output.Error) {
	home, err := s.HomeDir()
	if err != nil || home == "" {
		return "", output.Errorf(output.CodeConfigInvalid, "无法确定用户主目录，找不到本地连接配置")
	}
	return config.Path(home), nil
}

// Invocation 是一次命令调用：解析后的参数、所在环境与连接服务端的客户端。
type Invocation struct {
	Cmd          *Command
	Args         []string
	Registry     *Registry
	Sys          *System
	Identity     Identity     // 按环境判断的身份
	Client       *conn.Client // 只有连接服务端的命令才有
	Meta         *output.Meta // 命令可以补充 meta（例如本地命令选中的 profile）
	flags        map[string]string
	repeated     map[string][]string
	accessed     map[string]bool
	secretOutput *secretOutput
}

// Has 判断选项是否在命令行中出现。
func (inv *Invocation) Has(name string) bool {
	inv.accessed[name] = true
	_, ok := inv.flags[name]
	return ok
}

// String 返回选项的值；没有出现时返回声明的默认值。
func (inv *Invocation) String(name string) string {
	inv.accessed[name] = true
	if v, ok := inv.flags[name]; ok {
		return v
	}
	if f, ok := inv.Cmd.flag(name); ok {
		return f.Default
	}
	return ""
}

// Strings returns all explicit values in order, or the declared default if absent.
// Return a copy so command handlers cannot mutate parsed option values.
func (inv *Invocation) Strings(name string) []string {
	inv.accessed[name] = true
	if values, ok := inv.repeated[name]; ok {
		return append([]string(nil), values...)
	}
	if value, ok := inv.flags[name]; ok {
		return []string{value}
	}
	if f, ok := inv.Cmd.flag(name); ok && f.Default != "" {
		return []string{f.Default}
	}
	return nil
}

// Bool 返回布尔选项。
func (inv *Invocation) Bool(name string) bool { return inv.String(name) == "true" }

// Int 返回整数选项；解析时已校验格式。
func (inv *Invocation) Int(name string) int {
	n, _ := strconv.Atoi(inv.String(name))
	return n
}

// Accessed 返回执行过程中读取过的选项名，供测试检查“声明的选项都被使用”。
func (inv *Invocation) Accessed() map[string]bool { return inv.accessed }

// Hook 在命令执行后被调用，供测试检查调用细节；生产代码中为空。
var Hook func(inv *Invocation)

// Run 执行一次 loopctl 调用，向 sys.Stdout 写出一个 JSON 对象，返回退出码。
func Run(ctx context.Context, r *Registry, argv []string, sys *System) (code int) {
	meta := output.Meta{ClientVersion: sys.Version}
	jsonOutput := &secretOutput{Writer: sys.Stdout}
	var client *conn.Client
	fail := func(e *output.Error) int {
		var warnings []output.Warning
		if client != nil {
			warnings = clientMeta(client, &meta)
		}
		_ = output.WriteFailure(jsonOutput, e, warnings, meta)
		return output.ExitCode(e.Category)
	}
	defer func() {
		if p := recover(); p != nil {
			code = fail(output.Errorf(output.CodeInternalError, "loopctl 内部错误：%v", p))
		}
	}()

	p, e := parse(r, argv)
	if e != nil {
		return fail(e)
	}
	if p.help {
		data, e := schemaFor(r, sys, p.path, "")
		if e != nil {
			return fail(e)
		}
		_ = output.WriteSuccess(jsonOutput, data, nil, meta)
		return 0
	}
	c := p.cmd
	inv := &Invocation{Cmd: c, Args: p.args, Registry: r, Sys: sys, Meta: &meta, flags: p.flags, repeated: p.repeated, accessed: map[string]bool{}, secretOutput: jsonOutput}
	if len(c.Identities) > 0 {
		inv.Identity = Identity(conn.DetectIdentity(sys.Getenv))
		if !c.AllowedFor(inv.Identity) {
			return fail(output.Errorf(output.CodeCommandNotAllowed, "%s 身份不能使用 %s", inv.Identity, c.Name()).
				WithDetails(map[string]any{"identity": inv.Identity, "allowed": c.Identities}))
		}
	}
	if c.Destructive && !inv.Bool(YesFlag.Name) {
		return fail(output.Errorf(output.CodeConfirmationRequired, "%s 是删除或覆盖操作，需要带 --yes 确认；loopctl 不会交互询问", c.Name()))
	}
	if c.Network {
		s, e := resolve(inv)
		if e != nil {
			return fail(e)
		}
		meta.Identity, meta.Profile = s.Identity, s.Profile
		client = conn.NewClient(s, conn.ClientOptions{Version: sys.Version, GOOS: sys.GOOS, GOARCH: sys.GOARCH, Dial: sys.Dial, Sleep: sys.Sleep})
		inv.Client = client
	}
	data, err := c.Run(ctx, inv)
	if Hook != nil {
		Hook(inv)
	}
	if err != nil {
		var oe *output.Error
		if !errors.As(err, &oe) {
			oe = output.Errorf(output.CodeInternalError, "%v", err)
		}
		return fail(oe)
	}
	var warnings []output.Warning
	if client != nil {
		warnings = clientMeta(client, &meta)
	}
	if err := output.WriteSuccess(jsonOutput, data, warnings, meta); err != nil {
		return fail(output.Errorf(output.CodeInternalError, "无法编码输出：%v", err))
	}
	return 0
}

// Check 只解析不执行：检查命令行能否被解析、该身份能否使用这条命令。供内置 Skill 的测试使用。
func Check(r *Registry, argv []string, id Identity) *output.Error {
	p, e := parse(r, argv)
	if e != nil {
		return e
	}
	if p.help || p.cmd == nil {
		return output.Errorf(output.CodeUnknownCommand, "不是一条可执行的命令")
	}
	if !p.cmd.AllowedFor(id) {
		return output.Errorf(output.CodeCommandNotAllowed, "%s 身份不能使用 %s", id, p.cmd.Name())
	}
	return nil
}

// resolve 解析连接参数。
func resolve(inv *Invocation) (*conn.Settings, *output.Error) {
	o := conn.Options{
		Getenv:  inv.Sys.Getenv,
		Profile: inv.String("profile"),
		Server:  inv.String("server"),
		CAFile:  inv.String("ca-file"),
	}
	if v := inv.String("timeout"); v != "" {
		o.Timeout, _ = time.ParseDuration(v)
	}
	if conn.DetectIdentity(inv.Sys.Getenv) == conn.IdentityExternal {
		path, e := inv.Sys.ConfigPath()
		if e != nil {
			return nil, e
		}
		o.ConfigPath = path
	}
	return conn.Resolve(o)
}

func clientMeta(c *conn.Client, meta *output.Meta) []output.Warning {
	v, id, attempts, responded := c.Meta()
	if attempts > 0 {
		meta.Attempts = attempts
	}
	if responded {
		meta.ServerVersion, meta.RequestID = v, id
	}
	return c.Warnings()
}

// Errorf 是 output.Errorf 的简写，供命令使用。
func Errorf(code, format string, args ...any) *output.Error {
	return output.Errorf(code, format, args...)
}
