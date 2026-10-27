package larksidecar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func launcherDigest(t *testing.T, path string) string {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("fixture artifact unavailable")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func launcherFixture(t *testing.T) (LauncherDescriptor, string) {
	t.Helper()
	root := ownedInputHome(t)
	if os.Chmod(root, 0700) != nil {
		t.Fatal("fixture private directory")
	}
	home, work := filepath.Join(root, "home"), filepath.Join(root, "workspace")
	for _, p := range []string{home, work} {
		if os.Mkdir(p, 0700) != nil {
			t.Fatal("fixture mkdir")
		}
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal("fixture executable")
	}
	b := &Broker{address: "http://127.0.0.1:12345", key: strings.Repeat("a", 64), binding: Binding{AppID: "cli_synthetic", UserID: "ou_synthetic", Brand: "feishu"}, limits: defaults(Limits{})}
	cfg := LaunchConfig{Binary: self, BinarySHA256: launcherDigest(t, self), Home: home, WorkingDir: work, InputRoots: []string{work}}
	d, e := b.LauncherScope(cfg, launcherDigest(t, self))
	if e != nil {
		t.Fatal("export fixture scope")
	}
	return d, filepath.Join(root, "scope.json")
}

func writeLauncherFixture(t *testing.T, path string, d LauncherDescriptor) []byte {
	t.Helper()
	raw, e := json.Marshal(d)
	if e != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("write fixture descriptor")
	}
	return raw
}

func TestLauncherDescriptorPrivateFrame(t *testing.T) {
	d, path := launcherFixture(t)
	raw := writeLauncherFixture(t, path, d)
	got, e := readLauncherDescriptor(path)
	if e != nil || got.Launch.HostHome != d.Launch.HostHome || got.Capability != d.Capability {
		t.Fatal("valid private descriptor rejected")
	}
	for _, test := range []string{"unknown", "duplicate", "nestedDuplicate", "trailing", "oversize", "null", "badCapability", "missingUser", "missingHostHome", "badOrigin", "badProtocol", "workspace", "symlink", "parentSymlink", "hardlink", "mode", "parentMode", "fifo"} {
		t.Run(test, func(t *testing.T) {
			d, p := launcherFixture(t)
			input := append([]byte(nil), raw...)
			// Use this subtest's independent metadata for every mutation.
			input = writeLauncherFixture(t, p, d)
			switch test {
			case "unknown":
				input = append([]byte(`{"unknown":true,`), input[1:]...)
			case "duplicate":
				input = append([]byte(`{"brand":"lark",`), input[1:]...)
			case "nestedDuplicate":
				input = bytes.Replace(input, []byte(`"launch":{`), []byte(`"launch":{"Home":"/synthetic",`), 1)
			case "trailing":
				input = append(input, []byte(` {}`)...)
			case "oversize":
				input = bytes.Repeat([]byte(" "), launcherDescriptorLimit+1)
			case "null":
				input = []byte("null")
			case "badCapability":
				d.Capability = strings.Repeat("z", 64)
				input, _ = json.Marshal(d)
			case "missingHostHome":
				d.Launch.HostHome = ""
				input, _ = json.Marshal(d)
			case "missingUser":
				d.UserID = ""
				input, _ = json.Marshal(d)
			case "badOrigin":
				d.BrokerURL = "http://localhost:12345"
				input, _ = json.Marshal(d)
			case "badProtocol":
				d.Protocol = "unrecognized"
				input, _ = json.Marshal(d)
			case "workspace":
				p = filepath.Join(d.Launch.WorkingDir, "scope.json")
			case "symlink":
				link := filepath.Join(filepath.Dir(p), "alias")
				if os.Symlink(p, link) != nil {
					t.Fatal("fixture symlink")
				}
				p = link
			case "parentSymlink":
				link := filepath.Join(ownedInputHome(t), "alias")
				if os.Symlink(filepath.Dir(p), link) != nil {
					t.Fatal("fixture parent link")
				}
				p = filepath.Join(link, filepath.Base(p))
			case "hardlink":
				if os.Link(p, p+"-alias") != nil {
					t.Fatal("fixture hardlink")
				}
			case "mode":
				if os.Chmod(p, 0644) != nil {
					t.Fatal("fixture chmod")
				}
			case "parentMode":
				if os.Chmod(filepath.Dir(p), 0755) != nil {
					t.Fatal("fixture parent chmod")
				}
			case "fifo":
				if os.Remove(p) != nil || syscall.Mkfifo(p, 0600) != nil {
					t.Fatal("fixture fifo")
				}
			}
			switch test {
			case "symlink", "parentSymlink", "hardlink", "mode", "parentMode", "fifo":
			default:
				if os.WriteFile(p, input, 0600) != nil {
					t.Fatal("fixture write")
				}
			}
			start := time.Now()
			if _, e := readLauncherDescriptor(p); e == nil {
				t.Fatal("unsafe descriptor admitted")
			}
			if time.Since(start) > time.Second {
				t.Fatal("descriptor read blocked")
			}
		})
	}
}

func TestLauncherScopeCaptureAndRelease(t *testing.T) {
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") }), Limits{})
	d, _ := launcherFixture(t)
	cfg := d.Launch
	cfg.HostHome = ""
	exported, e := b.ExportLaunchDescriptor(cfg, d.LauncherSHA256)
	if e != nil || exported.BrokerURL != b.URL() || exported.Capability != b.Capability() || exported.UserID != b.binding.UserID || exported.Launch.HostHome == "" {
		t.Fatal("capture export mismatch")
	}
	cfg.InputRoots[0] = "/untrusted-mutated"
	if exported.Launch.InputRoots[0] == cfg.InputRoots[0] {
		t.Fatal("export retained mutable input roots")
	}
	_ = b.Close()
	if _, e := b.LauncherScope(d.Launch, d.LauncherSHA256); e == nil || calls.Load() != 0 {
		t.Fatal("released scope exported or export resolved token")
	}
}

func TestLauncherBootstrapRejectsBeforeNative(t *testing.T) {
	d, path := launcherFixture(t)
	for _, test := range []string{"selfHash", "nativeHash", "management", "outsideFile", "missingProbe", "cancelled"} {
		t.Run(test, func(t *testing.T) {
			v := d
			args := []string{"api", "GET", "/open-apis/im/v1/messages"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch test {
			case "selfHash":
				v.LauncherSHA256 = strings.Repeat("0", 64)
			case "nativeHash":
				v.Launch.BinarySHA256 = strings.Repeat("0", 64)
			case "management":
				args = []string{"auth", "login"}
			case "outsideFile":
				outside := filepath.Join(ownedInputHome(t), "outside.json")
				if os.WriteFile(outside, []byte(`{"synthetic":true}`), 0600) != nil {
					t.Fatal("fixture write")
				}
				args = []string{"api", "--data", "@" + outside}
			case "cancelled":
				cancel()
			}
			writeLauncherFixture(t, path, v)
			cmd, e := StandaloneCommand(ctx, path, args...)
			if e == nil || cmd != nil || e.Error() != "lark user launcher rejected" {
				t.Fatal("bootstrap returned startable command or unsafe error")
			}
		})
	}
}

// Real shell/PATH -> separately built production shim. A missing probe must
// reject before native dispatch even if Pi's inherited HOME equals scope HOME.
// No production fixture switch or alternate sandbox is involved.
func TestLauncherBootstrapPATH(t *testing.T) {
	shim := os.Getenv("LARK_USER_LAUNCHER_BINARY")
	if shim == "" {
		t.Skip("explicit separately built launcher required")
	}
	shim, e := filepath.EvalSymlinks(shim)
	if e != nil {
		t.Fatal("launcher unavailable")
	}
	d, path := launcherFixture(t)
	root := filepath.Dir(path)
	native := filepath.Join(root, "native-marker")
	if os.WriteFile(native, []byte("#!/bin/sh\nprintf unexpected > \"$HOME/native-started\"\n"), 0700) != nil {
		t.Fatal("fixture native")
	}
	d.Launch.Binary, d.Launch.BinarySHA256 = native, launcherDigest(t, native)
	d.LauncherSHA256 = launcherDigest(t, shim)
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") }), Limits{})
	d.BrokerURL, d.Capability = b.URL(), b.Capability()
	writeLauncherFixture(t, path, d)
	bin := filepath.Join(root, "bin")
	if os.Mkdir(bin, 0700) != nil {
		t.Fatal("fixture wrapper directory")
	}
	quoted := "'" + strings.ReplaceAll(shim, "'", "'\"'\"'") + "'"
	if os.WriteFile(filepath.Join(bin, "lark-cli"), []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0700) != nil {
		t.Fatal("fixture wrapper")
	}
	for _, test := range []string{"missingDescriptor", "missingProbe", "management", "outsideFile"} {
		t.Run(test, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			args := []string{"api", "GET", "/open-apis/im/v1/messages"}
			p := path
			switch test {
			case "missingDescriptor":
				p = path + "-absent"
			case "management":
				args = []string{"auth", "login"}
			case "outsideFile":
				outside := filepath.Join(root, "outside.json")
				if os.WriteFile(outside, []byte(`{"fixture":true}`), 0600) != nil {
					t.Fatal("fixture input")
				}
				args = []string{"api", "--data", "@" + outside}
			}
			cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", `exec lark-cli "$@"`, "path-fixture"}, args...)...)
			cmd.Dir = d.Launch.WorkingDir
			cmd.Env = []string{"HOME=" + d.Launch.Home, "PATH=" + bin + ":/usr/bin:/bin", LauncherScopeEnv + "=" + p, "LARKSUITE_CLI_USER_ACCESS_TOKEN=synthetic-must-not-be-used"}
			out, e := cmd.CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 2 || string(out) != "lark_user_launcher_rejected\n" || ctx.Err() != nil {
				t.Fatal("PATH launcher did not reject safely and promptly")
			}
			if _, e := os.Stat(filepath.Join(d.Launch.Home, "native-started")); !os.IsNotExist(e) || calls.Load() != 0 {
				t.Fatal("rejected PATH invocation dispatched native or resolved token")
			}
		})
	}
	t.Log("real shell/PATH -> separately built shim: rejected descriptor/probe/args/outside @file, native=0 source=0; not a supported-platform/native-success claim")
}
