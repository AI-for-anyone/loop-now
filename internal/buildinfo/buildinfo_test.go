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
		{"injected", "1.2.3", info("vcs.revision", rev, "vcs.modified", "true"), true, "1.2.3"},
		{"revision", "", info("vcs.revision", rev, "vcs.modified", "false"), true, "dev-f988ba0c1d2e"},
		{"uncommitted changes", "", info("vcs.revision", rev, "vcs.modified", "true"), true, "dev-f988ba0c1d2e-dirty"},
		{"short revision", "", info("vcs.revision", "abc123"), true, "dev-abc123"},
		{"no VCS info", "", info("GOOS", "darwin"), true, "dev"},
		{"build info unavailable", "", nil, false, "dev"},
	}
	for _, c := range cases {
		if got := resolve(c.injected, c.bi, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestVersionPrefersInjected(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "2.0.0"
	if Version() != "2.0.0" {
		t.Fatalf("should return the injected value when injected: %q", Version())
	}
	version = ""
	if v := Version(); v != "dev" && !strings.HasPrefix(v, "dev-") {
		t.Fatalf("should be dev or dev-<revision> when not injected: %q", v)
	}
}

// Print the current version, to check injection by hand with -ldflags:
//
//	go test -run TestVersionReport -v -ldflags "-X loop-now/internal/buildinfo.version=1.2.3" ./internal/buildinfo/
func TestVersionReport(t *testing.T) {
	t.Logf("Version() = %q", Version())
}
