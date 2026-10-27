package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func info(settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	for i := 0; i+1 < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

func TestResolve(t *testing.T) {
	const rev = "f988ba0c1d2e3f4a5b6c7d8e9f00112233445566"
	cases := []struct {
		name     string
		injected string
		bi       *debug.BuildInfo
		ok       bool
		want     string
	}{
		{"已注入", "1.2.3", info("vcs.revision", rev, "vcs.modified", "true"), true, "1.2.3"},
		{"修订号", "", info("vcs.revision", rev, "vcs.modified", "false"), true, "dev-f988ba0c1d2e"},
		{"未提交改动", "", info("vcs.revision", rev, "vcs.modified", "true"), true, "dev-f988ba0c1d2e-dirty"},
		{"短修订号", "", info("vcs.revision", "abc123"), true, "dev-abc123"},
		{"没有 VCS 信息", "", info("GOOS", "darwin"), true, "dev"},
		{"读不到构建信息", "", nil, false, "dev"},
	}
	for _, c := range cases {
		if got := resolve(c.injected, c.bi, c.ok); got != c.want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, got, c.want)
		}
	}
}

func TestVersionPrefersInjected(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "2.0.0"
	if Version() != "2.0.0" {
		t.Fatalf("已注入时应返回注入值：%q", Version())
	}
	version = ""
	if v := Version(); v != "dev" && !strings.HasPrefix(v, "dev-") {
		t.Fatalf("未注入时应为 dev 或 dev-<修订号>：%q", v)
	}
}

// 打印当前版本，便于用 -ldflags 手动核对注入：
//
//	go test -run TestVersionReport -v -ldflags "-X loop-now/internal/buildinfo.version=1.2.3" ./internal/buildinfo/
func TestVersionReport(t *testing.T) {
	t.Logf("Version() = %q", Version())
}
