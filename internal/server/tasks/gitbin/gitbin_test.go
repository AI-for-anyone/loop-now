package gitbin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// script writes an executable shell script; when marker is not empty, running it also creates that file.
func script(t *testing.T, path, body, marker string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "#!/bin/sh\n"
	if marker != "" {
		text += "touch '" + marker + "'\n"
	}
	if err := os.WriteFile(path, []byte(text+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func dirs(d ...string) func(context.Context) []string {
	return func(context.Context) []string { return d }
}

func TestNoGitOnThePath(t *testing.T) {
	empty := t.TempDir()
	f := &Finder{Dirs: dirs(empty, filepath.Join(empty, "missing"))}
	if p, ok := f.Available(context.Background()); ok {
		t.Fatalf("found %q in a PATH without git", p)
	}
}

func TestAFailingGitDoesNotCount(t *testing.T) {
	d := t.TempDir()
	script(t, filepath.Join(d, "git"), "exit 1", "")
	f := &Finder{Dirs: dirs(d)}
	if _, ok := f.Available(context.Background()); ok {
		t.Fatal("a git whose --version fails counted as working")
	}
}

func TestTheFirstWorkingGitIsFoundAndRemembered(t *testing.T) {
	broken, good := t.TempDir(), t.TempDir()
	script(t, filepath.Join(broken, "git"), "exit 1", "")
	g := script(t, filepath.Join(good, "git"), "echo 'git version 2.50.0'", "")
	f := &Finder{Dirs: dirs(broken, good)}
	if p, ok := f.Available(context.Background()); !ok || p != g {
		t.Fatalf("Available = %q, %v; want %q", p, ok, g)
	}
	if err := os.Remove(g); err != nil {
		t.Fatal(err)
	}
	if p, ok := f.Available(context.Background()); !ok || p != g || f.Path() != g {
		t.Fatalf("a found git is remembered: %q, %v", p, ok)
	}
}

func TestAMissingGitIsLookedForAgain(t *testing.T) {
	d := t.TempDir()
	f := &Finder{Dirs: dirs(d)}
	if _, ok := f.Available(context.Background()); ok {
		t.Fatal("found git in an empty folder")
	}
	g := script(t, filepath.Join(d, "git"), "echo 'git version 2.50.0'", "")
	if p, ok := f.Available(context.Background()); !ok || p != g {
		t.Fatalf("git installed later: %q, %v", p, ok)
	}
}

func TestConfiguredGitComesFirst(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	conf := script(t, filepath.Join(a, "git"), "echo 'git version 2.40.0'", "")
	script(t, filepath.Join(b, "git"), "echo 'git version 2.50.0'", "")
	f := &Finder{Configured: conf, Dirs: dirs(b)}
	if p, _ := f.Available(context.Background()); p != conf {
		t.Fatalf("Available = %q, want the configured %q", p, conf)
	}
}

// Without the developer tools the macOS stub is never run (it would open the install dialog); a real git later on the
// PATH is still found.
func TestTheStubIsNotRunWithoutDeveloperTools(t *testing.T) {
	root := t.TempDir()
	ran := filepath.Join(root, "stub-ran")
	stub := script(t, filepath.Join(root, "usr", "bin", "git"), "echo 'git version 2.39.0'", ran)
	xs := script(t, filepath.Join(root, "usr", "bin", "xcode-select"), "echo 'xcode-select: error: unable to get active developer directory' >&2; exit 2", "")
	link := filepath.Join(root, "linkdir", "git")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stub, link); err != nil {
		t.Fatal(err)
	}
	f := &Finder{Configured: stub, Dirs: dirs(filepath.Dir(link), filepath.Dir(stub)), SystemStub: stub, XcodeSelect: xs}
	if p, ok := f.Available(context.Background()); ok {
		t.Fatalf("found %q without the developer tools", p)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the stub was run")
	}

	// xcode-select printing a folder that does not exist counts as no developer tools too.
	script(t, xs, "echo '"+filepath.Join(root, "gone")+"'", "")
	if _, ok := f.Available(context.Background()); ok {
		t.Fatal("found git with a missing developer folder")
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the stub was run")
	}

	// A real git later in the PATH is used.
	other := t.TempDir()
	real := script(t, filepath.Join(other, "git"), "echo 'git version 2.50.0'", "")
	f.Dirs = dirs(filepath.Dir(stub), other)
	if p, ok := f.Available(context.Background()); !ok || p != real {
		t.Fatalf("Available = %q, %v; want %q", p, ok, real)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the stub was run")
	}
}

func TestTheStubRunsWithDeveloperTools(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "CommandLineTools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := script(t, filepath.Join(root, "usr", "bin", "git"), "echo 'git version 2.39.0'", "")
	xs := script(t, filepath.Join(root, "usr", "bin", "xcode-select"), "echo '"+tools+"'", "")
	f := &Finder{Dirs: dirs(filepath.Dir(stub)), SystemStub: stub, XcodeSelect: xs}
	if p, ok := f.Available(context.Background()); !ok || p != stub {
		t.Fatalf("Available = %q, %v; want the stub with developer tools", p, ok)
	}
}
