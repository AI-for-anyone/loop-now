package conn

import (
	"os"
	"path/filepath"
	"runtime"
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

// writeConfig writes the config file and returns its path.
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
		t.Fatalf("wrong Agent identity resolution: %+v", s)
	}
}

// The Agent identity does not read the config file: even a config file with overly wide permissions does not
// affect it.
func TestResolveAgentIgnoresConfigFile(t *testing.T) {
	path := writeConfig(t, map[string]config.Profile{"default": {Server: "https://x.example", Token: extToken}})
	os.Chmod(path, 0o644)
	_, e := Resolve(Options{ConfigPath: path, Getenv: envOf(map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://127.0.0.1:1/agent/v1"})})
	if e != nil {
		t.Fatalf("the Agent identity should not read the config file: %v", e)
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
		{"Agent token plus external token", Options{Getenv: envOf(with(agentEnv, EnvToken, extToken))}, output.CodeIdentityConflict},
		{"Agent token plus LOOPCTL_PROFILE", Options{Getenv: envOf(with(agentEnv, EnvProfile, "x"))}, output.CodeIdentityConflict},
		{"Agent token plus LOOPCTL_URL", Options{Getenv: envOf(with(agentEnv, EnvURL, "https://x"))}, output.CodeIdentityConflict},
		{"Agent token plus LOOPCTL_CA_FILE", Options{Getenv: envOf(with(agentEnv, EnvCAFile, "/x"))}, output.CodeIdentityConflict},
		{"Agent identity with --server", Options{Server: "https://x", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"Agent identity with --ca-file", Options{CAFile: "/x", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"Agent identity with --profile", Options{Profile: "p", Getenv: envOf(agentEnv)}, output.CodeIdentityConflict},
		{"external token in the Agent token variable", Options{Getenv: envOf(map[string]string{EnvAgentToken: extToken, EnvAgentURL: "http://127.0.0.1:1/agent/v1"})}, output.CodeTokenKindMismatch},
		{"Agent identity without a URL", Options{Getenv: envOf(map[string]string{EnvAgentToken: agentToken})}, output.CodeConnectionNotConfigured},
		{"Agent URL not loopback", Options{Getenv: envOf(map[string]string{EnvAgentToken: agentToken, EnvAgentURL: "http://10.0.0.1/agent/v1"})}, output.CodeInvalidServerURL},
		{"no token at all", Options{Getenv: envOf(nil)}, output.CodeConnectionNotConfigured},
		{"Agent token in the external token variable", Options{Getenv: envOf(map[string]string{EnvToken: agentToken, EnvURL: "https://x.example"})}, output.CodeTokenKindMismatch},
		{"external identity without a URL", Options{Getenv: envOf(map[string]string{EnvToken: extToken})}, output.CodeConnectionNotConfigured},
		{"external identity with a plaintext URL", Options{Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "http://loop.example.com"})}, output.CodeInvalidServerURL},
		{"external identity with a loopback plaintext URL", Options{Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "http://127.0.0.1:8781"})}, output.CodeInvalidServerURL},
		{"--profile names a missing profile", Options{Profile: "prod", Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeProfileNotFound},
		{"LOOPCTL_PROFILE names a missing profile", Options{Getenv: envOf(map[string]string{EnvProfile: "prod", EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeProfileNotFound},
		{"unreadable CA file", Options{CAFile: "/nonexistent/ca.pem", Getenv: envOf(map[string]string{EnvToken: extToken, EnvURL: "https://x.example"})}, output.CodeCAFileUnreadable},
		{"bad timeout format", Options{Getenv: envOf(map[string]string{EnvTimeout: "5"})}, output.CodeConfigInvalid},
		{"bad response limit format", Options{Getenv: envOf(map[string]string{EnvMaxResponse: "64MB"})}, output.CodeConfigInvalid},
		{"bad retry count", Options{Getenv: envOf(map[string]string{EnvRetryMax: "-1"})}, output.CodeConfigInvalid},
		{"bad retry interval", Options{Getenv: envOf(map[string]string{EnvRetryInitial: "0s"})}, output.CodeConfigInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.o.ConfigPath == "" {
				c.o.ConfigPath = filepath.Join(t.TempDir(), "none.json")
			}
			_, e := Resolve(c.o)
			if e == nil || e.Code != c.code {
				t.Fatalf("should fail with %s, got %v", c.code, e)
			}
			if c.code != output.CodeConfigInvalid && e.Category != output.CategoryConfig {
				t.Errorf("category should be config, got %s", e.Category)
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
	t.Run("profile only", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Getenv: envOf(nil)})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://profile.example" || s.Profile != "default" || s.CAFile != ca || s.Base.Path != "/api/v1" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("environment wins over config file", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Getenv: envOf(map[string]string{EnvURL: "https://env.example", EnvCAFile: ca2})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://env.example" || s.CAFile != ca2 {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("command-line flags win over environment", func(t *testing.T) {
		s, e := Resolve(Options{ConfigPath: path, Server: "https://flag.example", Getenv: envOf(map[string]string{EnvURL: "https://env.example"})})
		if e != nil {
			t.Fatal(e)
		}
		if s.Root.String() != "https://flag.example" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("--profile wins over LOOPCTL_PROFILE", func(t *testing.T) {
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
	t.Run("no default means no profile", func(t *testing.T) {
		p := writeConfig(t, map[string]config.Profile{"other": {Server: "https://o.example", Token: extToken}})
		s, e := Resolve(Options{ConfigPath: p, Getenv: envOf(map[string]string{EnvURL: "https://env.example", EnvToken: extToken})})
		if e != nil || s.Profile != "" {
			t.Fatalf("%v %+v", e, s)
		}
	})
	t.Run("config file permissions too broad", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("chmod cannot change the ACL on Windows, so too-broad permissions cannot be set up")
		}
		p := writeConfig(t, map[string]config.Profile{"default": {Server: "https://o.example", Token: extToken}})
		os.Chmod(p, 0o644)
		_, e := Resolve(Options{ConfigPath: p, Getenv: envOf(nil)})
		if e == nil || e.Code != output.CodeConfigInsecurePermissions {
			t.Fatalf("got %v", e)
		}
	})
	t.Run("runtime settings", func(t *testing.T) {
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
			t.Fatalf("wrong defaults: %+v", s)
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
			t.Errorf("%s: got %v %v, want %s", in, u, e, want)
		}
	}
	bad := []string{"loop.example.com", "ftp://x", "http://10.0.0.1", "https://user:pass@x", "https://x/?a=1", "https://x/#f", "://"}
	for _, in := range bad {
		if _, e := ParseServerURL(in, false); e == nil || e.Code != output.CodeInvalidServerURL {
			t.Errorf("%s: should fail with invalid_server_url, got %v", in, e)
		} else if in == "https://user:pass@x" && strings.Contains(e.Message, "pass") {
			t.Errorf("the error message leaks the password: %s", e.Message)
		}
	}
	if _, e := ParseServerURL("http://127.0.0.1:1", true); e == nil {
		t.Error("http should be rejected when https is required")
	}
}

func TestTokenID(t *testing.T) {
	id, prefix, ok := TokenID("lne_0192F1A2b3c47d8e9f0a1b2c3d4e5f60_abc-_DEF")
	if !ok || id != "0192f1a2-b3c4-7d8e-9f0a-1b2c3d4e5f60" || prefix != "lne_0192f1a2" {
		t.Fatalf("%s %s %v", id, prefix, ok)
	}
	for _, bad := range []string{"lne_0192f1a2_abc", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_", "xyz_0192f1a2b3c47d8e9f0a1b2c3d4e5f60_a", "lne_0192f1a2b3c47d8e9f0a1b2c3d4e5fzz_a"} {
		if _, _, ok := TokenID(bad); ok {
			t.Errorf("%s should not be accepted", bad)
		}
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"10": 10, "10B": 10, "2KiB": 2048, "64MiB": 64 << 20, "1GiB": 1 << 30} {
		if n, ok := ParseSize(in); !ok || n != want {
			t.Errorf("%s: got %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "1MB", "-1", "1.5MiB", "99999999999999999999GiB"} {
		if _, ok := ParseSize(in); ok {
			t.Errorf("%s should not be accepted", in)
		}
	}
}
