package localmode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitDirsKeepsAbsoluteUniqueInOrder(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	sep := string(os.PathListSeparator)
	raw := strings.Join([]string{a, ".", "", "rel/bin", b, a + string(filepath.Separator), b}, sep)
	if got := splitDirs(raw); !slices.Equal(got, []string{a, b}) {
		t.Fatalf("splitDirs = %v", got)
	}
	if runtime.GOOS == "windows" {
		if got := splitDirs(strings.Join([]string{`C:\WINDOWS\system32`, `/usr/bin`, `C:\Windows\System32\`, `C:\Tools`}, sep)); !slices.Equal(got, []string{`C:\WINDOWS\system32`, `C:\Tools`}) {
			t.Fatalf("on Windows, paths without a drive such as /usr/bin should be dropped and duplicates removed case-insensitively: %v", got)
		}
	}
}

func TestUserPathFallsBackToProcessPath(t *testing.T) {
	proc := filepath.Join(t.TempDir(), "proc-bin")
	t.Setenv("PATH", proc)
	u := NewUserPath(nil, time.Second)
	u.resolve = func(context.Context) (string, string, error) { return "", "shell", errors.New("boom") }
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("should fall back to the process PATH on failure: %v", got)
	}
	// Resolve only once.
	u.resolve = func(context.Context) (string, string, error) { t.Fatal("should not resolve again"); return "", "", nil }
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("second call = %v", got)
	}
}

func TestUserPathDirsHonoursCallerContext(t *testing.T) {
	u := NewUserPath(nil, time.Minute)
	release := make(chan struct{})
	u.resolve = func(context.Context) (string, string, error) { <-release; return "/x", "test", nil }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := u.Dirs(ctx); got != nil {
		t.Fatalf("should return nil when the caller times out: %v", got)
	}
	close(release)
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{filepath.Clean("/x")}) && runtime.GOOS != "windows" {
		t.Fatalf("after resolving = %v", got)
	}
}

// writeShellProfile prepares a temporary HOME containing only an rc file, with SHELL pointing to /bin/sh (which reads
// ~/.profile and $ENV when run as an interactive login shell).
func writeShellProfile(t *testing.T, profile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not read a login shell")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("BASH_ENV", "")
}

func TestLoginShellPathReadsProfile(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-bin")
	writeShellProfile(t, "echo Welcome message\nexport PATH='"+custom+"':\"$PATH\"\necho more output\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	raw, source, err := loginShellPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dirs := splitDirs(raw)
	if source != "/bin/sh" || len(dirs) == 0 || dirs[0] != custom || !slices.Contains(dirs, "/usr/bin") {
		t.Fatalf("source=%s dirs=%v raw=%q", source, dirs, raw)
	}
}

func TestLoginShellPathTimesOut(t *testing.T) {
	writeShellProfile(t, "sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, _, err := loginShellPath(ctx)
	if err == nil || time.Since(begin) > 5*time.Second {
		t.Fatalf("should fail within the time limit: %v %s", err, time.Since(begin))
	}
	// The whole resolver falls back to the process PATH.
	proc := filepath.Join(t.TempDir(), "proc-bin")
	t.Setenv("PATH", proc)
	u := NewUserPath(nil, 300*time.Millisecond)
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("should fall back to the process PATH after a timeout: %v", got)
	}
}
