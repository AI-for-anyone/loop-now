package larksidecar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProductionPolicyRejectsIPv6SameBrokerPort(t *testing.T) {
	cfg := nativeConfig(t)
	b, _ := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	template, e := b.Command(ctx, cfg, "api", "GET", "/open-apis/im/v1/messages")
	if e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(b.URL(), "http://"))
	ln, e := net.Listen("tcp6", net.JoinHostPort("::1", port))
	if e != nil {
		t.Fatal("owned same-port IPv6 fixture unavailable")
	}
	defer ln.Close()
	control, e := net.DialTimeout("tcp6", ln.Addr().String(), time.Second)
	if e != nil {
		t.Fatal("IPv6 control not reachable")
	}
	control.Close()
	self, _ := os.Executable()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", template.Args[2], self, "-test.run=^TestNativeNetworkCapabilityChild$")
	cmd.Env = []string{"HOME=" + cfg.Home, "PATH=/usr/bin:/bin", "LARK_TEST_DIAL=" + ln.Addr().String()}
	e = cmd.Run()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 42 {
		t.Fatal("production policy permitted same-port IPv6 outside numeric IPv4 broker")
	}
}

func TestProductionAdmissionFailsClosedBeforeNative(t *testing.T) {
	cfg := nativeConfig(t)
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("admission touched provider") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, test := range []string{"missingSandbox", "missingProbe", "wrongProbeSHA", "ipv6Escape"} {
		t.Run(test, func(t *testing.T) {
			c := cfg
			sandbox := "/usr/bin/sandbox-exec"
			switch test {
			case "missingSandbox":
				sandbox = filepath.Join(cfg.Home, "does-not-exist")
			case "missingProbe":
				c.ProbeBinary = ""
				c.ProbeSHA256 = ""
			case "wrongProbeSHA":
				c.ProbeSHA256 = strings.Repeat("0", 64)
			}
			cmd, e := b.commandWithSandbox(ctx, c, sandbox, "api", "GET", "/open-apis/im/v1/messages")
			if e == nil || cmd != nil {
				t.Fatal("failed admission returned startable native command")
			}
			if calls.Load() != 0 {
				t.Fatal("admission resolved token")
			}
			t.Logf("%s: no native Cmd returned; source=0", test)
		})
	}
	// Rejected SBPL must fail admission with a fixed safe execution category;
	// neither refused/timeout nor a parser rejection is a denial success.
	e := b.admitSandbox(ctx, cfg, "(version 1)(not-a-valid-operation)")
	if e == nil || !strings.Contains(e.Error(), "execution rejected") {
		t.Fatal("invalid policy counted as available")
	}
}

func TestAdmissionProbeCancellationReapsOwnedGroup(t *testing.T) {
	cfg := nativeConfig(t)
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("probe touched provider") }), Limits{})
	// Test-only fake probe, never packaged. It creates one owned descendant;
	// cancellation must kill the separate probe process group and drain Wait.
	probe := filepath.Join(cfg.Home, "test-probe-block.sh")
	script := []byte("#!/bin/sh\n/bin/sleep 30 &\nchild=$!\nprintf '%s %s\\n' \"$$\" \"$child\" > \"$HOME/probe-pids\"\nwait\n")
	if e := os.WriteFile(probe, script, 0700); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(script)
	cfg.ProbeBinary = probe
	cfg.ProbeSHA256 = hex.EncodeToString(sum[:])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.admitSandbox(ctx, cfg, `(version 1)(allow default)(deny network*)`) }()
	var parent, child int
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
waitPID:
	for {
		select {
		case <-ticker.C:
			raw, e := os.ReadFile(filepath.Join(cfg.Home, "probe-pids"))
			n, _ := fmt.Sscanf(string(raw), "%d %d", &parent, &child)
			if e == nil && n == 2 {
				break waitPID
			}
		case <-deadline.C:
			t.Fatal("owned probe did not report pid")
		}
	}
	if parent <= 1 || child <= 1 {
		t.Fatal("invalid owned process ids")
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled probe admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("probe Wait did not drain")
	}
	for _, pid := range []int{parent, child} {
		until := time.Now().Add(time.Second)
		for {
			e := syscall.Kill(pid, 0)
			if e == syscall.ESRCH {
				break
			}
			if time.Now().After(until) {
				t.Fatal("owned probe process still exists after group cancellation")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("probe resolved token")
	}
	t.Log("cancelled production admission killed its owned probe group/descendant and drained Wait; native never dispatched")
}

func TestProductionInputMetadataFailsClosed(t *testing.T) {
	cfg := nativeConfig(t)
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("metadata touched provider") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, name := range []string{"noWorkingDir", "noRoots", "homeRoot", "wrongRoot", "nestedPrivateHome", "workspaceContainsHome"} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			switch name {
			case "noWorkingDir":
				c.WorkingDir = ""
			case "noRoots":
				c.InputRoots = nil
			case "homeRoot":
				c.InputRoots = []string{cfg.Home}
			case "wrongRoot":
				c.InputRoots = []string{filepath.Dir(cfg.WorkingDir)}
			case "nestedPrivateHome":
				c.WorkingDir = filepath.Join(cfg.Home, "private")
				c.InputRoots = []string{c.WorkingDir}
			case "workspaceContainsHome":
				c.WorkingDir = filepath.Dir(cfg.Home)
				c.InputRoots = []string{c.WorkingDir}
			}
			cmd, e := b.Command(ctx, c, "api", "GET", "/open-apis/im/v1/messages")
			if e == nil || cmd != nil || calls.Load() != 0 {
				t.Fatal("untrusted input metadata admitted")
			}
		})
	}
}
