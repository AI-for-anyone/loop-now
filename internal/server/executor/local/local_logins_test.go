package local

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// fakeRealHome creates a fake "real user home directory" holding some things with the same names as Loop Now's own
// files, and returns the directory and a snapshot of all the files in it.
func fakeRealHome(t *testing.T) (string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		"notes.txt":                          "The user's own file",
		".gitconfig":                         "[url \"file:///nonexistent-user-rewrite/\"]\n\tinsteadOf = file:///\n",
		".lark-cli/config.json":              `{"apps":[{"appId":"user-own"}]}`,
		".loop-now/gcloud/config/x":          "user",
		".loop-now/db/psql/connections.json": "[]",
		"home/keep":                          "same name as the agent home",
		"Library/Application Support/lark-cli/master.key.file": "user-key",
	}
	for rel, body := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, snapshotTree(t, home)
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, root string, want map[string]string) {
	t.Helper()
	got := snapshotTree(t, root)
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s in the user's real home directory was changed or deleted: %q", k, g)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("extra %s in the user's real home directory", k)
		}
	}
}

// lookPath finds a command on PATH (the value in the dump), the same way the shell looks it up.
func lookPath(path, name string) string {
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// "Use local logins": Pi's HOME is the user's real home directory and SSH_AUTH_SOCK and XDG_* are passed through; the
// platform directories do not change; managed CLIs with a configured account run through shims with the Agent's
// separate HOME (the same environment as with the switch off), while CLIs without one are not intercepted; the prompt
// mentions interactive authorization; changing the switch restarts the process, and turning it off restores the
// environment and removes the old shims.
func TestLoginsEnvShimsAndPrompt(t *testing.T) {
	skipWithoutCLITemplates(t)
	realHome, before := fakeRealHome(t)
	userBin := t.TempDir()
	for _, name := range []string{"gh", "tea"} { // gh and tea installed locally by the user
		if err := os.WriteFile(filepath.Join(userBin, name), []byte("#!/bin/sh\necho user-"+name+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Use empty temp directories for the base run PATH, so how commands resolve does not depend on whether gh or tea is
	// installed on the host.
	base := strings.Join([]string{t.TempDir(), t.TempDir()}, string(os.PathListSeparator))
	h := newHarness(t, func(o *Options) {
		o.ContextDir = t.TempDir()
		o.ExecBasePath = base
		o.RealHome = func() (string, error) { return realHome, nil }
		o.UserPath = func(context.Context) []string { return []string{userBin} }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	h.env["SSH_AUTH_SOCK"] = "/tmp/ssh-agent.sock"
	h.env["XDG_CONFIG_HOME"] = filepath.Join(realHome, ".xdg-config")
	ghDir := preinstall(t, h.root, "gh", "2.100.0", "gh")
	// The managed gh prints the HOME it sees and the variables passed through only for the login setting.
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte("#!/bin/sh\nprintf '%s|%s|%s|%s' \"$HOME\" \"${XDG_CONFIG_HOME-unset}\" \"${SSH_AUTH_SOCK-unset}\" \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"gh"}
		s.CLIPayloads["gh"] = clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-gh"}}
		s.Local = agentconfig.LocalAccess{Logins: true, UserPath: true}
	})
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	dirs := h.e.dirs(id)
	for k, v := range map[string]string{
		"HOME": realHome, "SSH_AUTH_SOCK": "/tmp/ssh-agent.sock", "XDG_CONFIG_HOME": filepath.Join(realHome, ".xdg-config"),
		"TMPDIR": dirs.tmp, "PI_CODING_AGENT_DIR": dirs.pi, "GH_TOKEN": "synthetic-gh", "GIT_TERMINAL_PROMPT": "0",
	} {
		if d.Env[k] != v {
			t.Errorf("%s = %q, want %q", k, d.Env[k], v)
		}
	}
	shims := filepath.Join(dirs.root, cliShimDir)
	if path := d.Env["PATH"]; path != strings.Join([]string{shims, base, userBin}, string(os.PathListSeparator)) {
		t.Fatalf("PATH should be the shim directory, the run PATH, the user's PATH (no template directories directly any more): %s", path)
	}
	// gh with a configured account resolves to the shim; the shim runs the original gh with the Agent's separate HOME
	// and the environment as with the switch off.
	gh := lookPath(d.Env["PATH"], "gh")
	if gh != filepath.Join(shims, "gh") {
		t.Fatalf("gh should resolve to the shim: %s", gh)
	}
	if fi, err := os.Stat(shims); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the shim directory should be accessible only to its owner: %v %v", fi, err)
	}
	cmd := exec.Command(gh, "auth", "status")
	for k, v := range d.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil || string(out) != dirs.home+"|unset|unset|auth status" {
		t.Fatalf("wrong environment for the managed gh: %q %v", out, err)
	}
	// tea, which is not configured, resolves via PATH to the user's local copy.
	if tea := lookPath(d.Env["PATH"], "tea"); tea != filepath.Join(userBin, "tea") {
		t.Fatalf("an unconfigured CLI should resolve to the user's local copy: %s", tea)
	}
	if !strings.Contains(d.SystemPrompt, "Local logins: your HOME is the user's own home directory "+realHome) || !strings.Contains(d.SystemPrompt, "do not keep retrying") {
		t.Fatalf("the prompt should mention local logins and interactive authorization:\n%s", d.SystemPrompt)
	}
	pid := h.pid(id)

	// Turn it off: the next dispatch restarts the process, the environment is as before, the old shims are removed, and
	// there is no reminder any more.
	h.src.mu.Lock()
	h.src.snaps[id].Local.Logins = false
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "second"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("a change to the logins switch should restart Pi at the next dispatch")
	}
	d = readDump(t, dumpPath)
	if d.Env["HOME"] != dirs.home || d.Env["SSH_AUTH_SOCK"] != "" || d.Env["XDG_CONFIG_HOME"] != "" || strings.Contains(d.SystemPrompt, "Local logins") {
		t.Fatalf("should be restored once turned off: HOME=%s SSH=%s XDG=%s", d.Env["HOME"], d.Env["SSH_AUTH_SOCK"], d.Env["XDG_CONFIG_HOME"])
	}
	if path := d.Env["PATH"]; path != strings.Join([]string{ghDir, base, userBin}, string(os.PathListSeparator)) {
		t.Fatalf("PATH should go back to the template directories once turned off: %s", path)
	}
	if _, err := os.Stat(shims); !os.IsNotExist(err) {
		t.Fatal("no old shims should be left once turned off")
	}
	sameTree(t, realHome, before)
}

// Only managed CLIs with an account configured for this Agent get a shim: an Agent without gh configured does not
// intercept gh, which resolves via PATH to the user's local gh and uses the real HOME.
func TestLoginsDoNotShimUnconfiguredCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shims exist only on Unix")
	}
	realHome, _ := fakeRealHome(t)
	userBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(userBin, "gh"), []byte("#!/bin/sh\necho user-gh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir() // empty base run PATH: a gh installed on the host (e.g. /usr/bin/gh on a CI runner) is not hit first
	h := newHarness(t, func(o *Options) {
		o.ExecBasePath = base
		o.RealHome = func() (string, error) { return realHome, nil }
		o.UserPath = func(context.Context) []string { return []string{userBin} }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "gh", "2.100.0", "gh") // installed, but not configured for this Agent
	id := h.agent(func(s *agentconfig.Snapshot) { s.Local = agentconfig.LocalAccess{Logins: true, UserPath: true} })
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	if path := d.Env["PATH"]; path != base+string(os.PathListSeparator)+userBin {
		t.Fatalf("without managed CLIs PATH should be only the run PATH and the user's PATH, with no shim or template directories: %s", path)
	}
	if gh := lookPath(d.Env["PATH"], "gh"); gh != filepath.Join(userBin, "gh") || d.Env["HOME"] != realHome {
		t.Fatalf("an unconfigured gh should resolve to the user's local copy and use the real HOME: %s HOME=%s", gh, d.Env["HOME"])
	}
	if _, err := os.Stat(filepath.Join(h.e.dirs(id).root, cliShimDir)); !os.IsNotExist(err) {
		t.Fatal("there should be no shim directory without managed CLIs")
	}
}

// With the switch on, run the full start, release, CleanupStale, delete Agent sequence: cleanup only touches the
// Agent's own directories and the user's real home directory stays untouched; clones the platform makes for the Agent
// (old folder sources) do not read the user's git config.
func TestLoginsNeverTouchRealHome(t *testing.T) {
	skipWithoutCLITemplates(t)
	realHome, before := fakeRealHome(t)
	h := newHarness(t, func(o *Options) {
		o.ContextDir = t.TempDir()
		o.RealHome = func() (string, error) { return realHome, nil }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "lark-cli", "1.0.94", "lark-cli")
	bare := gitRepo(t)
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Logins: true}
		s.CLIs = []string{"lark-cli"}
		s.CLIPayloads["lark-cli"] = clitemplate.Payload{Files: []clitemplate.PayloadFile{{Path: ".lark-cli/config.json", Content: []byte(`{"apps":[{"appId":"agent"}]}`)}}}
		// The user's ~/.gitconfig rewrites file:/// to a place that does not exist: a clone that read it would fail.
		s.Sources = []store.FolderSource{{ID: uuid.Must(uuid.NewV7()), Path: "app", Kind: "git", GitURL: "file://" + bare, GitBranch: "main"}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("the start should succeed (the clone does not read the user's git config): %+v", res)
	}
	dirs := h.e.dirs(id)
	if d := readDump(t, dumpPath); d.Env["HOME"] != realHome {
		t.Fatalf("HOME = %s", d.Env["HOME"])
	}
	if b, err := os.ReadFile(filepath.Join(dirs.home, ".lark-cli", "config.json")); err != nil || !strings.Contains(string(b), `"agent"`) {
		t.Fatalf("the managed CLI's credential file should be written in the Agent's own HOME: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dirs.root, cliShimDir, "lark-cli")); !strings.Contains(string(b), "HOME="+shellQuote(dirs.home)) {
		t.Fatalf("the lark-cli shim should restore the Agent's own HOME: %s", b)
	}
	// Release (called when an Agent is deleted) and startup cleanup: only the Agent's own directories are deleted.
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.src.mu.Lock()
	h.src.deleted = append(h.src.deleted, id)
	h.src.mu.Unlock()
	if err := h.e.CleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.home); !os.IsNotExist(err) {
		t.Fatalf("the Agent's own HOME should be reclaimed: %v", err)
	}
	sameTree(t, realHome, before)
	if !slices.Contains(h.src.deleted, id) {
		t.Fatal("unreachable")
	}
}

// buildEnv: with the switch off it is exactly the same as without loginHome; with it on, only the HOME group of
// variables changes and SSH_AUTH_SOCK/XDG_* are passed through; on Windows USERPROFILE and the like follow the real
// home directory, APPDATA/LOCALAPPDATA take the real values from the process environment, and TEMP is still the
// Agent's.
func TestLoginsBuildEnv(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-home")
	env := map[string]string{"SSH_AUTH_SOCK": "/tmp/agent.sock", "XDG_CACHE_HOME": filepath.Join(real, "cache"), "APPDATA": `D:\Roaming`, "LOCALAPPDATA": `D:\Local`, "LANG": "C"}
	tmpls, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	e := &Executor{tmpl: tmpls, o: Options{ExecBasePath: "/usr/bin", Username: "u", WorkRoot: t.TempDir(), Passthrough: []string{"LANG"}, Getenv: func(k string) string { return env[k] }}}
	d := e.dirs([16]byte{2})
	snap := agentconfig.Snapshot{}
	asMap := func(list []string) map[string]string {
		m := map[string]string{}
		for _, kv := range list {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		return m
	}
	log := slog.New(slog.DiscardHandler)
	off := asMap(e.buildEnv(d, snap, nil, nil, "", log))
	on := asMap(e.buildEnv(d, snap, nil, nil, real, log))
	if off["HOME"] != d.home || off["SSH_AUTH_SOCK"] != "" || off["XDG_CACHE_HOME"] != "" {
		t.Fatalf("switch off: %v", off)
	}
	if on["HOME"] != real || on["SSH_AUTH_SOCK"] != "/tmp/agent.sock" || on["XDG_CACHE_HOME"] != env["XDG_CACHE_HOME"] || on["TMPDIR"] != d.tmp || on["PI_CODING_AGENT_DIR"] != d.pi {
		t.Fatalf("switch on: %v", on)
	}
	changed := []string{"HOME", "SSH_AUTH_SOCK", "XDG_CACHE_HOME"}
	if runtime.GOOS == "windows" {
		if on["USERPROFILE"] != real || on["APPDATA"] != `D:\Roaming` || on["LOCALAPPDATA"] != `D:\Local` || on["TEMP"] != d.tmp || on["TMP"] != d.tmp {
			t.Fatalf("the HOME group of variables on Windows: %v", on)
		}
		changed = append(changed, "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA")
	}
	// Apart from this group, every other variable is identical to the switch-off case.
	for k, v := range on {
		if !slices.Contains(changed, k) && off[k] != v {
			t.Errorf("%s should not change with the switch: %q -> %q", k, off[k], v)
		}
	}
	if got := e.loginOnlyEnv(); slices.Contains(got, "LANG") || !slices.Contains(got, "SSH_AUTH_SOCK") {
		t.Fatalf("variables the shim removes: %v", got)
	}
}
