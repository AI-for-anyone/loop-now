package conn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/output"
)

const (
	extToken   = "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_SECRETsecretSECRET"
	agentToken = "lna_0192f1a2b3c47d8e9f0a1b2c3d4e5f61_AGENTsecretAGENT"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// writeConfig 写入配置文件并返回路径。
func writeConfig(t *testing.T, profiles map[string]config.Profile) string {
	t.Helper()
	path := config.Path(t.TempDir())
	if err := config.Save(path, &config.File{SchemaVersion: 1, Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveAgent(t *testing.T) {
	s, e := Resolve(Options{Getenv: envOf(map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://127.0.0.1:8780/agent/v1/"})})
	if e != nil {
		t.Fatal(e)
	}
	if s.Identity != IdentityAgent || s.Base.String() != "http://127.0.0.1:8780/agent/v1" || s.token != agentToken {
		t.Fatalf("Agent 身份解析错误：%+v", s)
	}
}

// Agent 身份不读取配置文件：即使配置文件权限过宽也不受影响。
func TestResolveAgentIgnoresConfigFile(t *testing.T) {
	path := writeConfig(t, map[string]config.Profile{"default": {Server: "https://x.example", Token: extToken}})
	os.Chmod(path, 0o644)
	_, e := Resolve(Options{ConfigPath: path, Getenv: envOf(map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://127.0.0.1:1/agent/v1"})})
	if e != nil {
		t.Fatalf("Agent 身份不应读取配置文件：%v", e)
	}
}

func TestResolveErrors(t *testing.T) {
	agentEnv := map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://127.0.0.1:1/agent/v1"}
	with := func(m map[string]string, k, v string) map[string]string {
		out := map[string]string{k: v}
		for a, b := range m {
			out[a] = b
		}
		return out
	}
	cases := []struct {
		name string
		o    Options
		code string
	}{
		{"Agent 令牌加外部令牌", Options{Getenv: envOf(with(agentEnv, EnvToken, extToken))}, output.CodeIdentityConflict},
		{"Agent 令牌加 LOOPCTL_PROFILE", Options{Getenv: envOf(with(agentEnv, EnvProfile, "x"))}, output.CodeIdentityConflict},
		{"Agent 令牌加 LOOPCTL_URL", Options{Getenv: envOf(with(agentEnv, EnvURL, "https://x"))}, output.CodeIdentityConflict},
		{"Agent 令牌加 LOOPCTL_CA_FILE", Options{Getenv: envOf(with(agentEnv, EnvCAFile, "/x"))}, output.CodeIdentityConflict},
		{"Agent 身份带 --server", Options{Server: "https://x", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"Agent 身份带 --ca-file", Options{CAFile: "/x", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"Agent 身份带 --profile", Options{Profile: "p", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"Agent 令牌变量中是外部令牌", Options{Getenv: envOf(map[string]string{EnvAgentToken: extToken, EnvAgentURL: "http://127.0.0.1:1/agent/v1"})}, output.CodeTokenKindMismatch},
		{"Agent 身份缺少地址", Options{Getenv: envOf(map[string]string{EnvAgentToken: agentToken})}, output.CodeConnectionNotConfigured},
		{"Agent 地址不是回环", Options{Getenv: envOf(map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://10.0.0.1/agent/v1"})}, output.CodeInvalidServerURL},
		{"没有任何令牌", Options{Getenv: envOf(nil)}, output.CodeConnectionNotConfigured},
		{"外部令牌变量中是 Agent 令牌", Options{Getenv: envOf(map[string]string{EnvToken: agentToken, EnvURL: "https://x.example"})}, output.CodeTokenKindMismatch},
		{"外部身份缺少地址", Options{Getenv: envOf(map[string]string{EnvToken: extToken})}, output.CodeConnectionNotConfigured},
		{"外部身份使用明文地址", Options{Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "http://loop.example.com"})}, output.CodeInvalidServerURL},
		{"外部身份使用回环明文地址", Options{Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "http://127.0.0.1:8781"})}, output.CodeInvalidServerURL},
		{"--profile 指定的不存在", Options{Profile: "prod", Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeProfileNotFound},
		{"LOOPCTL_PROFILE 指定的不存在", Options{Getenv: envOf(map[string]string{EnvProfile: "prod", EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeProfileNotFound},
		{"CA 文件读不了", Options{CAFile: "/nonexistent/ca.pem", Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeCAFileUnreadable},
		{"超时格式", Options{Getenv: envOf(map[string]string{EnvTimeout: "5"})}, output.CodeConfigInvalid},
		{"响应上限格式", Options{Getenv: envOf(map[string]string{EnvMaxResponse: "64MB"})}, output.CodeConfigInvalid},
		{"重试次数", Options{Getenv: envOf(map[string]string{EnvRetryMax: "-1"})}, output.CodeConfigInvalid},
		{"重试间隔", Options{Getenv: envOf(map[string]string{EnvRetryInitial: "0s"})}, output.CodeConfigInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.o.ConfigPath == "" {
				c.o.ConfigPath = filepath.Join(t.TempDir(), "none.json")
			}
			_, e := Resolve(c.o)
			if e == nil || e.Code != c.code {
				t.Fatalf("应以 %s 失败，得到 %v", c.code, e)
			}
			if c.code != output.CodeConfigInvalid && e.Category != output.CategoryConfig {
				t.Errorf("类别应为 config，得到 %s", e.Category)
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	ca := writeCA(t)
	ca2 := writeCA(t)
	path := writeConfig(t, map[string]config.Profile{
		"default": {Server: "https://profile.example", Token: extToken, CAFile: ca},
		"staging": {Server: "https://staging.example", Token: extToken},
	})
	t.Run("只有 profile", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Getenv: envOf(nil)})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://profile.example" || s.Profile != "default" || s.CAFile != ca || s.Base.Path != "/api/v1" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("环境变量优先于配置文件", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Getenv: envOf(map[string]string{EnvURL: "https://env.example", EnvCAFile: ca2})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://env.example" || s.CAFile != ca2 {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("命令行选项优先于环境变量", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Server: "https://flag.example", Getenv: envOf(map[string]string{EnvURL: "https://env.example"})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://flag.example" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("--profile 优先于 LOOPCTL_PROFILE", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Profile: "staging", Getenv: envOf(map[string]string{EnvProfile: "default"})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Profile != "staging" || s.Root.String() != "https://staging.example" || s.CAFile != "" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("LOOPCTL_PROFILE", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Getenv: envOf(map[string]string{EnvProfile: "staging"})})
		if e != nil || s.Profile != "staging" {
			t.Fatalf("%v %+v", e, s)
		}
	})
	t.Run("没有 default 时视为没有 profile", func(t *testing.T) {
		p := writeConfig(t, map[string]config.Profile{"other": {Server: "https://o.example", Token: extToken}})
		s, e := Resolve(Options{ConfigPath: p, Getenv: envOf(map[string]string{EnvURL: "https://env.example", EnvToken: extToken})})
		if e != nil || s.Profile != "" {
			t.Fatalf("%v %+v", e, s)
		}
	})
	t.Run("配置文件权限过宽", func(t *testing.T) {
		p := writeConfig(t, map[string]config.Profile{"default": {Server: "https://o.example", Token: extToken}})
		os.Chmod(p, 0o644)
		_, e := Resolve(Options{ConfigPath: p, Getenv: envOf(nil)})
		if e == nil || e.Code != output.CodeConfigInsecurePermissions {
			t.Fatalf("得到 %v", e)
		}
	})
	t.Run("运行参数", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Timeout: 7 * time.Second, Getenv: envOf(map[string]string{
			EnvTimeout: "9s", EnvMaxResponse: "2MiB", EnvRetryMax: "0", EnvRetryInitial: "1s", EnvRetryMaxInterval: "3s"})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Timeout != 7*time.Second || s.MaxResponse != 2<<20 || s.Retry != (RetryPolicy{0, time.Second, 3 * time.Second}) {
			t.Fatalf("%+v", s)
		}
		s, _ = Resolve(Options{ConfigPath: path, Getenv: envOf(nil)})
		if s.Timeout != DefaultTimeout || s.MaxResponse != DefaultMaxResponse || s.Retry != (RetryPolicy{2, 500 * time.Millisecond, 5 * time.Second}) {
			t.Fatalf("默认值不对：%+v", s)
		}
	})
}

func TestParseServerURL(t *testing.T) {
	ok := map[string]string{
		"https://loop.example.com:8781/":   "https://loop.example.com:8781",
		"HTTPS://loop.example.com/prefix/": "https://loop.example.com/prefix",
		"http://127.0.0.1:8780/agent/v1":   "http://127.0.0.1:8780/agent/v1",
		"http://localhost:1/agent/v1":      "http://localhost:1/agent/v1",
		"http://[::1]:1/agent/v1":          "http://[::1]:1/agent/v1",
	}
	for in, want := range ok {
		u, e := ParseServerURL(in, false)
		if e != nil || u.String() != want {
			t.Errorf("%s：得到 %v %v，应为 %s", in, u, e, want)
		}
	}
	bad := []string{"loop.example.com", "ftp://x", "http://10.0.0.1", "https://user:pass@x", "https://x/?a=1", "https://x/#f", "://"}
	for _, in := range bad {
		if _, e := ParseServerURL(in, false); e == nil || e.Code != output.CodeInvalidServerURL {
			t.Errorf("%s：应以 invalid_server_url 失败，得到 %v", in, e)
		} else if in == "https://user:pass@x" && strings.Contains(e.Message, "pass") {
			t.Errorf("错误说明中带出了密码：%s", e.Message)
		}
	}
	if _, e := ParseServerURL("http://127.0.0.1:1", true); e == nil {
		t.Error("要求 https 时应拒绝 http")
	}
}

func TestTokenID(t *testing.T) {
	id, prefix, ok := TokenID("lne_0192F1A2b3c47d8e9f0a1b2c3d4e5f60_abc-_DEF")
	if !ok || id != "0192f1a2-b3c4-7d8e-9f0a-1b2c3d4e5f60" || prefix != "lne_0192f1a2" {
		t.Fatalf("%s %s %v", id, prefix, ok)
	}
	for _, bad := range []string{"lne_0192f1a2_abc", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_", "xyz_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_a", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5fzz_a"} {
		if _, _, ok := TokenID(bad); ok {
			t.Errorf("%s 不应被接受", bad)
		}
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"10": 10, "10B": 10, "2KiB": 2048, "64MiB": 64 << 20, "1GiB": 1 << 30} {
		if n, ok := ParseSize(in); !ok || n != want {
			t.Errorf("%s：得到 %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "1MB", "-1", "1.5MiB", "99999999999999999999GiB"} {
		if _, ok := ParseSize(in); ok {
			t.Errorf("%s 不应被接受", in)
		}
	}
}
