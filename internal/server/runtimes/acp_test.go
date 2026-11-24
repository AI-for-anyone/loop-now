package runtimes_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/acpcli"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/testutil/fakeacpbin"
)

func statusOf(t *testing.T, list []runtimes.Status, id string) runtimes.Status {
	t.Helper()
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no status for %s: %+v", id, list)
	return runtimes.Status{}
}

// copyAs copies the fake ACP agent under another name (it plays Antigravity when its name starts with agy).
func copyAs(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(fakeacpbin.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	// The system checks a new executable the first time it runs (slow on macOS); pay that here, not in a check.
	if out, err := exec.Command(p, "--version").CombinedOutput(); err != nil {
		t.Fatalf("pre-run %s: %v %s", name, err, out)
	}
	return p
}

// Grok and Antigravity are checked like Codex: found, their version, and the login state their CLI reports.
func TestACPStatus(t *testing.T) {
	home := t.TempDir()
	env := func(context.Context) []string {
		return acpcli.Env(acpcli.EnvOptions{Home: home, Path: []string{"/usr/bin", "/bin"}})
	}
	c := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ACPEnv: env, GrokBin: copyAs(t, "grok"), AntigravityBin: copyAs(t, "agy"),
		RealHome: func() (string, error) { return t.TempDir(), nil }}
	list := c.List(context.Background(), true)
	for id, version := range map[string]string{"grok": "1.0.88", "antigravity": "1.5.14"} {
		s := statusOf(t, list, id)
		if !s.Available || s.Version == nil || *s.Version != version || s.BinaryPath == nil || s.Login == nil || s.Login.State != "logged_in" {
			t.Fatalf("%s: %+v %+v", id, s, s.Login)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".fakeacp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fakeacp", ".logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	list = c.List(context.Background(), true)
	for _, id := range []string{"grok", "antigravity"} {
		if s := statusOf(t, list, id); s.Login == nil || s.Login.State != "logged_out" {
			t.Fatalf("%s logged out: %+v", id, s.Login)
		}
	}
	missing := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, UserPath: func(context.Context) []string { return []string{t.TempDir()} }, ACPEnv: env}
	for _, id := range []string{"grok", "antigravity"} {
		if s := statusOf(t, missing.List(context.Background(), true), id); s.Available || s.UnavailableReason == nil || *s.UnavailableReason != "not_found" {
			t.Fatalf("%s not found: %+v", id, s)
		}
	}
}

// A local ACP command is available when its executable is there; its version and login are not checked.
func TestLocalCommandStatus(t *testing.T) {
	bin := fakeacpbin.Path(t)
	found, missing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agentruntime.SetLocalCommands([]agentruntime.Runtime{
		agentruntime.LocalCommandRuntime(found, "Found", agentruntime.LocalCommand{Command: bin}),
		agentruntime.LocalCommandRuntime(missing, "Missing", agentruntime.LocalCommand{Command: "no-such-agent"}),
	})
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	c := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, UserPath: func(context.Context) []string { return []string{t.TempDir()} }}
	list := c.List(context.Background(), true)
	if s := statusOf(t, list, agentruntime.LocalCommandRuntimeID(found)); !s.Available || s.BinaryPath == nil || *s.BinaryPath != bin || s.Version != nil || s.Login == nil || s.Login.State != "unknown" {
		t.Fatalf("found: %+v", s)
	}
	if s := statusOf(t, list, agentruntime.LocalCommandRuntimeID(missing)); s.Available || s.UnavailableReason == nil || *s.UnavailableReason != "not_found" {
		t.Fatalf("missing: %+v", s)
	}
}

// acpChecker checks Grok and Antigravity on copies of the fake agent with the given extra environment; the login state
// of both comes from login (nil runs their models command).
func acpChecker(t *testing.T, login func(string) string, extra ...string) *runtimes.Checker {
	home := t.TempDir()
	return &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, GrokBin: copyAs(t, "grok"), AntigravityBin: copyAs(t, "agy"), ACPLogin: login,
		RealHome: func() (string, error) { return t.TempDir(), nil },
		ACPEnv: func(context.Context) []string {
			return append(acpcli.Env(acpcli.EnvOptions{Home: home, Path: []string{"/usr/bin", "/bin"}}), extra...)
		}}
}

// The runtimes are checked at the same time: two CLIs that each take a second answer in about a second.
func TestChecksRunAtOnce(t *testing.T) {
	c := acpChecker(t, func(string) string { return acpcli.LoggedIn }, "FAKEACP_SLOW=1s")
	start := time.Now()
	list := c.List(context.Background(), true)
	if took := time.Since(start); took > 1800*time.Millisecond {
		t.Fatalf("the checks ran one after another: %v", took)
	}
	for _, id := range []string{"grok", "antigravity"} {
		if s := statusOf(t, list, id); !s.Available {
			t.Fatalf("%s: %+v", id, s)
		}
	}
}

// Once there is a result, a read returns it at once; an old one is checked again in the background and the next read
// has the new result. Only the first read and refresh wait.
func TestOldResultFirst(t *testing.T) {
	var mu sync.Mutex
	state := acpcli.LoggedIn
	login := func(string) string { mu.Lock(); defer mu.Unlock(); return state }
	c := acpChecker(t, login, "FAKEACP_SLOW=300ms")
	now := time.Unix(1000, 0)
	var clock sync.Mutex
	c.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	if s := statusOf(t, c.List(context.Background(), false), "grok"); s.Login == nil || s.Login.State != "logged_in" {
		t.Fatalf("first read waits for the check: %+v", s)
	}
	mu.Lock()
	state = acpcli.LoggedOut
	mu.Unlock()
	clock.Lock()
	now = now.Add(10 * time.Second)
	clock.Unlock()
	if s := statusOf(t, c.List(context.Background(), false), "grok"); s.Login.State != "logged_in" {
		t.Fatalf("a fresh result is reused: %+v", s.Login)
	}
	clock.Lock()
	now = now.Add(time.Minute)
	clock.Unlock()
	start := time.Now()
	s := statusOf(t, c.List(context.Background(), false), "grok")
	if took := time.Since(start); took > 150*time.Millisecond || s.Login.State != "logged_in" {
		t.Fatalf("an old result comes back at once (%v): %+v", took, s.Login)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if s := statusOf(t, c.List(context.Background(), false), "grok"); s.Login.State == "logged_out" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background check did not bring the new result")
		}
	}
}

// A CLI that hangs is given up after the time limit (its version could not be read) and holds no other runtime back.
func TestHungCLI(t *testing.T) {
	c := acpChecker(t, func(string) string { return acpcli.LoggedIn }, "FAKEACP_HANG=grok")
	c.ProbeTimeout = 500 * time.Millisecond
	start := time.Now()
	list := c.List(context.Background(), true)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("a hung CLI held the check: %v", took)
	}
	if s := statusOf(t, list, "grok"); s.Available || s.UnavailableReason == nil || *s.UnavailableReason != "version_failed" {
		t.Fatalf("grok: %+v", s)
	}
	if s := statusOf(t, list, "antigravity"); !s.Available || s.Login.State != "logged_in" {
		t.Fatalf("antigravity: %+v", s)
	}
}

// With a login conclusion at hand the status runs no command that goes online (grok models, agy models).
func TestStatusReadsTheLoginConclusion(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	c := acpChecker(t, func(id string) string {
		if id == "grok" {
			return acpcli.LoggedOut
		}
		return ""
	}, "FAKEACP_ARGS_FILE="+argsFile)
	list := c.List(context.Background(), true)
	if s := statusOf(t, list, "grok"); s.Login == nil || s.Login.State != "logged_out" {
		t.Fatalf("grok from its conclusion: %+v", s.Login)
	}
	if s := statusOf(t, list, "antigravity"); s.Login == nil || s.Login.State != "unknown" {
		t.Fatalf("no conclusion yet is unknown: %+v", s.Login)
	}
	b, _ := os.ReadFile(argsFile)
	if strings.Contains(string(b), `"models"`) {
		t.Fatalf("a models command ran: %s", b)
	}
}
