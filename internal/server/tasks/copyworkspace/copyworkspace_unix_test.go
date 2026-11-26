//go:build !windows

package copyworkspace

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Review focus: links pointing outside, a socket and a pipe are left out; nothing outside is followed or written.
func TestMakeLeavesOutWhatReachesOutside(t *testing.T) {
	src := folder(t)
	outside := filepath.Join(filepath.Dir(src), "outside")
	write(t, filepath.Join(outside, "secret.txt"), "secret")
	for name, target := range map[string]string{
		"abs":           filepath.Join(outside, "secret.txt"),
		"abs-original":  filepath.Join(src, "a.md"),
		"sub/rel-out":   "../../outside/secret.txt",
		"dir-out":       "..",
		"sub/rel-in":    "../a.md",
		"sub/dangling":  "missing.txt",
		"to-outsidedir": outside,
	} {
		if err := os.Symlink(target, filepath.Join(src, name)); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(src, "pkg", ".git"), "gitdir: "+filepath.Join(outside, ".git", "worktrees", "pkg"))
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(src, "s.sock")
	if len(sock) < 100 {
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
	}
	before, outsideBefore := snapshot(t, src), snapshot(t, outside)

	dst := copyPath(t)
	done := make(chan error, 1)
	var l Listing
	go func() {
		var err error
		l, err = Make(src, dst)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the copy hangs (a pipe was opened?)")
	}
	for _, gone := range []string{"abs", "abs-original", "sub/rel-out", "dir-out", "to-outsidedir", "pkg/.git", "pipe", "s.sock"} {
		if _, err := os.Lstat(filepath.Join(dst, gone)); err == nil {
			t.Errorf("%s is in the copy", gone)
		}
	}
	for _, kept := range []string{"sub/rel-in", "sub/dangling", "to-b"} {
		if _, err := os.Lstat(filepath.Join(dst, kept)); err != nil {
			t.Errorf("%s is not in the copy", kept)
		}
	}
	// Five links and the .git file; on macOS the clone also brings the pipe and the socket, which are then removed
	// (the plain copy elsewhere never copies them).
	if l.Left < 6 {
		t.Fatalf("left out %d entries, want at least 6", l.Left)
	}
	if snapshot(t, src) != before || snapshot(t, outside) != outsideBefore {
		t.Fatal("the original or the outside folder changed")
	}
	if err := Remove(dst); err != nil {
		t.Fatal(err)
	}
	if snapshot(t, src) != before || snapshot(t, outside) != outsideBefore {
		t.Fatal("removing the copy changed the original or the outside folder")
	}
}
