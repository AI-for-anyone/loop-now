// Package conn 负责 loopctl 的身份选择、连接参数解析与 HTTP 客户端（令牌、TLS、版本头、禁止重定向、读请求重试）。
package conn

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/output"
)

// 环境变量。
const (
	EnvAgentURL         = "LOOPCTL_AGENT_URL"
	EnvAgentToken       = "LOOPCTL_AGENT_TOKEN"
	EnvProfile          = "LOOPCTL_PROFILE"
	EnvURL              = "LOOPCTL_URL"
	EnvToken            = "LOOPCTL_TOKEN"
	EnvCAFile           = "LOOPCTL_CA_FILE"
	EnvTimeout          = "LOOPCTL_TIMEOUT"
	EnvMaxResponse      = "LOOPCTL_MAX_RESPONSE"
	EnvRetryMax         = "LOOPCTL_RETRY_MAX"
	EnvRetryInitial     = "LOOPCTL_RETRY_INITIAL"
	EnvRetryMaxInterval = "LOOPCTL_RETRY_MAX_INTERVAL"
)

// 身份。与 cli.Identity 的取值相同。
const (
	IdentityAgent    = "agent"
	IdentityExternal = "external"
)

// 令牌前缀。
const (
	AgentTokenPrefix    = "lna_"
	ExternalTokenPrefix = "lne_"
)

// 默认值。
const (
	DefaultTimeout          = 60 * time.Second
	DefaultMaxResponse      = 64 << 20
	DefaultRetryMax         = 2
	DefaultRetryInitial     = 500 * time.Millisecond
	DefaultRetryMaxInterval = 5 * time.Second
)

// EnvDoc 描述一个环境变量，供 schema 输出。
type EnvDoc struct {
	Name        string `json:"name"`
	Identity    string `json:"identity,omitempty"` // 为空表示两种身份都用
	Default     string `json:"default,omitempty"`
	Description string `json:"description"`
}

// EnvDocs 返回 loopctl 读取的全部环境变量。
func EnvDocs() []EnvDoc {
	return []EnvDoc{
		{EnvAgentURL, IdentityAgent, "", "Agent 接口的基址，由服务端注入，例如 http://127.0.0.1:<端口>/agent/v1"},
		{EnvAgentToken, IdentityAgent, "", "Agent 令牌（lna_ 开头），由服务端注入；不为空时使用 Agent 身份，且不读取配置文件"},
		{EnvProfile, IdentityExternal, config.DefaultProfile, "选择配置文件中的 profile"},
		{EnvURL, IdentityExternal, "", "对外端口的根地址（https://），优先于 profile"},
		{EnvToken, IdentityExternal, "", "外部令牌（lne_ 开头），优先于 profile"},
		{EnvCAFile, IdentityExternal, "", "PEM 格式的 CA 证书，指定后只信任其中的 CA，优先于 profile"},
		{EnvTimeout, "", DefaultTimeout.String(), "单次请求的超时"},
		{EnvMaxResponse, "", "64MiB", "JSON 响应的大小上限；下载文件不受此限制"},
		{EnvRetryMax, "", strconv.Itoa(DefaultRetryMax), "读请求遇到连接失败、超时或 502/503/504 时的最多重试次数，0 表示不重试"},
		{EnvRetryInitial, "", DefaultRetryInitial.String(), "首次重试前的等待，之后逐次加倍"},
		{EnvRetryMaxInterval, "", DefaultRetryMaxInterval.String(), "两次重试之间的最长等待"},
	}
}

// DetectIdentity 只按环境判断身份：有 Agent 令牌即为 Agent 身份，否则为外部身份。
func DetectIdentity(getenv func(string) string) string {
	if strings.TrimSpace(getenv(EnvAgentToken)) != "" {
		return IdentityAgent
	}
	return IdentityExternal
}

// Options 是解析连接参数的输入。字符串为空表示未提供。
type Options struct {
	Getenv     func(string) string
	ConfigPath string
	Profile    string        // --profile
	Server     string        // --server
	CAFile     string        // --ca-file
	Timeout    time.Duration // --timeout，0 表示未提供
}

// RetryPolicy 是读请求的重试参数。
type RetryPolicy struct {
	Max         int
	Initial     time.Duration
	MaxInterval time.Duration
}

// Settings 是解析后的连接参数。令牌不导出，避免被意外写进输出。
type Settings struct {
	Identity    string
	Base        *url.URL // API 基址：Agent 为 LOOPCTL_AGENT_URL，外部为 <根地址>/api/v1
	Root        *url.URL // 外部身份的服务端根地址
	Profile     string   // 使用了配置文件中的 profile 时为其名称
	CAFile      string
	Timeout     time.Duration
	MaxResponse int64
	Retry       RetryPolicy
	token       string
	roots       *x509.CertPool
}

// String 隐去令牌，避免 Settings 被格式化输出时带出令牌。
func (s *Settings) String() string {
	return fmt.Sprintf("{Identity:%s Base:%v Profile:%s CAFile:%s Timeout:%s token:<hidden>}", s.Identity, s.Base, s.Profile, s.CAFile, s.Timeout)
}

// GoString 与 String 相同。
func (s *Settings) GoString() string { return s.String() }

// Resolve 选择身份并解析连接参数。
func Resolve(o Options) (*Settings, *output.Error) {
	s := &Settings{}
	if e := runtimeParams(o, s); e != nil {
		return nil, e
	}
	if DetectIdentity(o.Getenv) == IdentityAgent {
		return s, resolveAgent(o, s)
	}
	return s, resolveExternal(o, s)
}

func resolveAgent(o Options, s *Settings) *output.Error {
	s.Identity = IdentityAgent
	var conflicts []string
	for _, name := range []string{EnvURL, EnvToken, EnvCAFile, EnvProfile} {
		if o.Getenv(name) != "" {
			conflicts = append(conflicts, name)
		}
	}
	for flag, v := range map[string]string{"--server": o.Server, "--ca-file": o.CAFile, "--profile": o.Profile} {
		if v != "" {
			conflicts = append(conflicts, flag)
		}
	}
	if len(conflicts) > 0 {
		sortStrings(conflicts)
		return output.Errorf(output.CodeIdentityConflict, "环境中有 %s，使用 Agent 身份；Agent 身份不能同时使用外部身份的参数：%s", EnvAgentToken, strings.Join(conflicts, "、")).
			WithDetails(map[string]any{"conflicts": conflicts})
	}
	tok := strings.TrimSpace(o.Getenv(EnvAgentToken))
	if !strings.HasPrefix(tok, AgentTokenPrefix) {
		return output.Errorf(output.CodeTokenKindMismatch, "%s 中不是 Agent 令牌（应以 %s 开头）", EnvAgentToken, AgentTokenPrefix)
	}
	raw := strings.TrimSpace(o.Getenv(EnvAgentURL))
	if raw == "" {
		return output.Errorf(output.CodeConnectionNotConfigured, "有 %s 但没有 %s", EnvAgentToken, EnvAgentURL)
	}
	u, e := ParseServerURL(raw, false)
	if e != nil {
		return e
	}
	s.Base, s.token = u, tok
	return nil
}

func resolveExternal(o Options, s *Settings) *output.Error {
	s.Identity = IdentityExternal
	name, explicit := config.DefaultProfile, ""
	if o.Profile != "" {
		name, explicit = o.Profile, "--profile"
	} else if v := o.Getenv(EnvProfile); v != "" {
		name, explicit = v, EnvProfile
	}
	f, _, e := config.Load(o.ConfigPath)
	if e != nil {
		return e
	}
	p, ok := f.Profiles[name]
	if !ok && explicit != "" {
		return output.Errorf(output.CodeProfileNotFound, "配置文件中没有 profile %q（由 %s 指定）", name, explicit)
	}
	if ok {
		s.Profile = name
	}
	tok := firstNonEmpty(strings.TrimSpace(o.Getenv(EnvToken)), p.Token)
	server := firstNonEmpty(o.Server, o.Getenv(EnvURL), p.Server)
	ca := firstNonEmpty(o.CAFile, o.Getenv(EnvCAFile), p.CAFile)
	if tok == "" {
		return output.Errorf(output.CodeConnectionNotConfigured,
			"没有可用的令牌：平台内 Agent 需要 %s；外部身份需要 %s，或用 loopctl config set <profile> --token-stdin 保存到配置文件", EnvAgentToken, EnvToken)
	}
	if !strings.HasPrefix(tok, ExternalTokenPrefix) {
		return output.Errorf(output.CodeTokenKindMismatch, "外部身份的令牌应以 %s 开头", ExternalTokenPrefix)
	}
	if server == "" {
		return output.Errorf(output.CodeConnectionNotConfigured, "没有服务端地址：用 --server、%s 或 profile 的 server 指定", EnvURL)
	}
	root, e := ParseServerURL(server, true)
	if e != nil {
		return e
	}
	base := *root
	base.Path = root.Path + "/api/v1"
	s.Root, s.Base, s.token = root, &base, tok
	if ca != "" {
		pool, e := LoadCA(ca)
		if e != nil {
			return e
		}
		s.CAFile, s.roots = ca, pool
	}
	return nil
}

// runtimeParams 解析超时、响应上限与重试参数，两种身份都适用。
func runtimeParams(o Options, s *Settings) *output.Error {
	s.Timeout = DefaultTimeout
	if o.Timeout > 0 {
		s.Timeout = o.Timeout
	} else if v := o.Getenv(EnvTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s 需要正的时长，例如 30s", EnvTimeout)
		}
		s.Timeout = d
	}
	s.MaxResponse = DefaultMaxResponse
	if v := o.Getenv(EnvMaxResponse); v != "" {
		n, ok := ParseSize(v)
		if !ok || n <= 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s 需要正的字节数，例如 64MiB", EnvMaxResponse)
		}
		s.MaxResponse = n
	}
	s.Retry = RetryPolicy{DefaultRetryMax, DefaultRetryInitial, DefaultRetryMaxInterval}
	if v := o.Getenv(EnvRetryMax); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s 需要不小于 0 的整数", EnvRetryMax)
		}
		s.Retry.Max = n
	}
	for _, x := range []struct {
		name string
		dst  *time.Duration
	}{{EnvRetryInitial, &s.Retry.Initial}, {EnvRetryMaxInterval, &s.Retry.MaxInterval}} {
		if v := o.Getenv(x.name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return output.Errorf(output.CodeConfigInvalid, "%s 需要正的时长，例如 500ms", x.name)
			}
			*x.dst = d
		}
	}
	return nil
}

var sizePattern = regexp.MustCompile(`^(\d+)(B|KiB|MiB|GiB)?$`)

// ParseSize 解析字节数，支持 B、KiB、MiB、GiB 后缀。
func ParseSize(v string) (int64, bool) {
	m := sizePattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30}[m[2]]
	if n > (1<<62)>>shift {
		return 0, false
	}
	return n << shift, true
}

// ParseServerURL 检查服务端地址：只允许 http 与 https；http 只允许回环主机；requireHTTPS 时只允许 https。
// 错误说明中不回显原值，避免带出其中可能含有的凭据。
func ParseServerURL(raw string, requireHTTPS bool) (*url.URL, *output.Error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, output.Errorf(output.CodeInvalidServerURL, "服务端地址不是合法的 http(s) 地址")
	}
	if u.User != nil {
		return nil, output.Errorf(output.CodeInvalidServerURL, "服务端地址中不能含有用户名或密码")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, output.Errorf(output.CodeInvalidServerURL, "服务端地址中不能含有查询参数或片段")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if requireHTTPS {
			return nil, output.Errorf(output.CodeInvalidServerURL, "外部身份必须使用 https:// 地址（对外端口只提供 TLS）")
		}
		if !IsLoopbackHost(u.Hostname()) {
			return nil, output.Errorf(output.CodeInvalidServerURL, "http:// 只允许回环地址，%s 不是回环地址", u.Hostname())
		}
	default:
		return nil, output.Errorf(output.CodeInvalidServerURL, "服务端地址必须以 https:// 开头")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// IsLoopbackHost 判断主机名是否为回环地址。
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoadCA 读取 PEM 格式的 CA 证书，至少要有一张。
func LoadCA(path string) (*x509.CertPool, *output.Error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, output.Errorf(output.CodeCAFileUnreadable, "无法读取 CA 文件 %s：%v", path, unwrapPath(err))
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, output.Errorf(output.CodeCAFileUnreadable, "CA 文件 %s 中没有 PEM 格式的证书", path)
	}
	return pool, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
