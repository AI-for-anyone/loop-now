package commands

import (
	"bufio"
	"context"
	"path/filepath"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func addLocal(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:    []string{"version"},
		Summary: "输出本地 loopctl 的版本、操作系统、架构与 Go 版本，不发出请求",
		Output:  "{version, os, arch, goVersion}",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			s := inv.Sys
			return map[string]string{"version": s.Version, "os": s.GOOS, "arch": s.GOARCH, "goVersion": s.GoVersion}, nil
		},
	})
	r.Add(&cli.Command{
		Path:    []string{"schema"},
		Summary: "输出机器可读的命令说明：全部命令、错误类别与退出码、身份与连接参数、全局选项；不需要连接配置，不发出请求",
		Args:    []cli.Arg{{Name: "path", Description: "命令路径，例如 inbox list；路径是命令组时输出其直接子命令；不写时输出全部", Variadic: true}},
		Flags: []cli.Flag{
			{Name: "identity", Type: cli.TypeEnum, Values: []string{"agent", "external"}, Description: "只输出该身份可用的命令"},
		},
		Output: "完整说明 {program, version, envelope, exitCodes, errorCodes, identities, environment, globalFlags, commands}；指定路径时为该命令或命令组的说明",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			data, e := cli.Schema(inv, inv.Args, cli.Identity(inv.String("identity")))
			if e != nil {
				return nil, e
			}
			return data, nil
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"whoami"},
		Summary:    "查询当前身份的类型、地址与名称",
		Identities: both,
		Network:    true,
		Retry:      true,
		Output:     "{kind, address, name}；服务端版本见 meta.serverVersion",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "whoami")
		},
	})

	r.Add(&cli.Command{
		Path:       []string{"config", "set"},
		Summary:    "新建或修改一个 profile，至少修改一项；令牌只能用 --token-stdin 从标准输入写入；不连接服务端",
		Identities: externalOnly,
		Stdin:      true,
		Args:       []cli.Arg{{Name: "profile", Description: "profile 名称：字母或数字开头，可含 . _ -，最长 64 个字符"}},
		Flags: []cli.Flag{
			{Name: "server", Type: cli.TypeString, Description: "对外端口的根地址，必须是 https://"},
			{Name: "ca-file", Type: cli.TypePath, Description: "PEM 格式的 CA 证书，至少含一张证书；保存为绝对路径"},
			{Name: "clear-ca", Type: cli.TypeBool, Description: "清除已保存的 CA 文件，改用系统根证书"},
			{Name: "token-stdin", Type: cli.TypeBool, Description: "从标准输入读取第一行作为外部令牌（lne_ 开头）"},
		},
		Constraints: []cli.Constraint{
			{Kind: cli.AtLeastOne, Flags: []string{"server", "ca-file", "clear-ca", "token-stdin"}},
			{Kind: cli.AtMostOne, Flags: []string{"ca-file", "clear-ca"}},
		},
		Output: "profile 摘要 {name, server, caFile, tokenSet, tokenId, tokenPrefix}，不含令牌的密钥部分",
		Run:    configSet,
	})
	r.Add(&cli.Command{
		Path:       []string{"config", "list"},
		Summary:    "列出全部 profile 的摘要，并标出按当前选项与环境变量会选中哪一个及其依据；不连接服务端",
		Identities: externalOnly,
		Flags: []cli.Flag{
			{Name: "profile", Type: cli.TypeString, Description: "按这个 profile 计算选中结果，与连接命令的 --profile 相同"},
		},
		Output: "{path, exists, profiles: [profile 摘要], selected: {name, by, exists}, envOverrides: [环境变量名]}",
		Run:    configList,
	})
	r.Add(&cli.Command{
		Path:        []string{"config", "delete"},
		Summary:     "删除一个 profile；不连接服务端",
		Identities:  externalOnly,
		Destructive: true,
		Args:        []cli.Arg{{Name: "profile", Description: "profile 名称"}},
		Output:      "{name, deleted}",
		Run:         configDelete,
	})
}

// profileSummary 是 profile 的对外摘要，只含令牌 ID 与显示前缀。
type profileSummary struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"`
	CAFile      string  `json:"caFile"`
	TokenSet    bool    `json:"tokenSet"`
	TokenID     *string `json:"tokenId"`
	TokenPrefix *string `json:"tokenPrefix"`
}

func summarize(name string, p config.Profile) profileSummary {
	s := profileSummary{Name: name, Server: p.Server, CAFile: p.CAFile, TokenSet: p.Token != ""}
	if id, prefix, ok := conn.TokenID(p.Token); ok {
		s.TokenID, s.TokenPrefix = &id, &prefix
	}
	return s
}

func loadConfig(inv *cli.Invocation) (string, *config.File, bool, error) {
	path, e := inv.Sys.ConfigPath()
	if e != nil {
		return "", nil, false, e
	}
	f, exists, e := config.Load(path)
	if e != nil {
		return "", nil, false, e
	}
	return path, f, exists, nil
}

func saveConfig(path string, f *config.File) error {
	if err := config.Save(path, f); err != nil {
		return output.Errorf(output.CodeOutputUnwritable, "无法写入配置文件 %s：%v", path, cause(err))
	}
	return nil
}

func profileArg(inv *cli.Invocation) (string, error) {
	name := inv.Args[0]
	if !config.ValidProfileName(name) {
		return "", output.Errorf(output.CodeInvalidArgument, "profile 名称 %q 不合法：字母或数字开头，可含 . _ -，最长 64 个字符", name)
	}
	return name, nil
}

func configSet(_ context.Context, inv *cli.Invocation) (any, error) {
	name, err := profileArg(inv)
	if err != nil {
		return nil, err
	}
	// 先检查全部输入，再读写配置文件，失败时文件保持不变。
	var server, ca, token string
	if inv.Has("server") {
		u, e := conn.ParseServerURL(inv.String("server"), true)
		if e != nil {
			return nil, e
		}
		server = u.String()
	}
	if inv.Has("ca-file") {
		abs, err := filepath.Abs(inv.String("ca-file"))
		if err != nil {
			return nil, output.Errorf(output.CodeCAFileUnreadable, "无法确定 CA 文件的绝对路径：%v", err)
		}
		if _, e := conn.LoadCA(abs); e != nil {
			return nil, e
		}
		ca = abs
	}
	if inv.Bool("token-stdin") {
		if token, err = readToken(inv); err != nil {
			return nil, err
		}
	}
	path, f, _, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	p := f.Profiles[name]
	if server != "" {
		p.Server = server
	}
	if ca != "" {
		p.CAFile = ca
	}
	if inv.Bool("clear-ca") {
		p.CAFile = ""
	}
	if token != "" {
		p.Token = token
	}
	f.Profiles[name] = p
	if err := saveConfig(path, f); err != nil {
		return nil, err
	}
	return summarize(name, p), nil
}

// readToken 从标准输入读取第一行作为外部令牌。错误说明中不包含读到的内容。
func readToken(inv *cli.Invocation) (string, error) {
	line, err := bufio.NewReaderSize(inv.Sys.Stdin, 4096).ReadString('\n')
	if err != nil && line == "" {
		return "", output.Errorf(output.CodeInvalidArgument, "标准输入中没有令牌")
	}
	tok := strings.TrimSpace(line)
	switch {
	case tok == "":
		return "", output.Errorf(output.CodeInvalidArgument, "标准输入中没有令牌")
	case strings.HasPrefix(tok, conn.AgentTokenPrefix):
		return "", output.Errorf(output.CodeTokenKindMismatch, "读到的是 Agent 令牌；配置文件只保存外部令牌（%s 开头）", conn.ExternalTokenPrefix)
	case !strings.HasPrefix(tok, conn.ExternalTokenPrefix):
		return "", output.Errorf(output.CodeInvalidArgument, "读到的不是外部令牌：外部令牌以 %s 开头", conn.ExternalTokenPrefix)
	}
	if _, _, ok := conn.TokenID(tok); !ok {
		return "", output.Errorf(output.CodeInvalidArgument, "外部令牌的格式不正确：应为 %s<32 位十六进制 ID>_<密钥>", conn.ExternalTokenPrefix)
	}
	return tok, nil
}

func configList(_ context.Context, inv *cli.Invocation) (any, error) {
	path, f, exists, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	name, by := config.DefaultProfile, "default"
	if inv.Has("profile") {
		name, by = inv.String("profile"), "--profile"
	} else if v := inv.Sys.Getenv(conn.EnvProfile); v != "" {
		name, by = v, conn.EnvProfile
	}
	_, found := f.Profiles[name]
	profiles := []profileSummary{}
	for _, n := range f.Names() {
		profiles = append(profiles, summarize(n, f.Profiles[n]))
	}
	overrides := []string{}
	for _, env := range []string{conn.EnvURL, conn.EnvToken, conn.EnvCAFile} {
		if inv.Sys.Getenv(env) != "" {
			overrides = append(overrides, env)
		}
	}
	return map[string]any{
		"path":         path,
		"exists":       exists,
		"profiles":     profiles,
		"selected":     map[string]any{"name": name, "by": by, "exists": found},
		"envOverrides": overrides,
	}, nil
}

func configDelete(_ context.Context, inv *cli.Invocation) (any, error) {
	name, err := profileArg(inv)
	if err != nil {
		return nil, err
	}
	path, f, _, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	if _, ok := f.Profiles[name]; !ok {
		return nil, output.Errorf(output.CodeProfileNotFound, "配置文件中没有 profile %q", name)
	}
	delete(f.Profiles, name)
	if err := saveConfig(path, f); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "deleted": true}, nil
}
