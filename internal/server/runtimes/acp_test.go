package runtimes_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
