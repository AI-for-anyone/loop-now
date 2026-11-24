// Package fakeacpbin builds the fakeacp binary for tests (only once per test process).
package fakeacpbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"loop-now/internal/testutil/testenv"
)

var (
	once sync.Once
	path string
	err  error
)

// Path returns the path of the built fakeacp; if the build fails, it fails the test.
func Path(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		var dir string
		dir, err = os.MkdirTemp("", "loopnow-fakeacp-")
		if err != nil {
			return
		}
		path = testenv.Exe(filepath.Join(dir, "fakeacp"))
		cmd := exec.Command("go", "build", "-o", path, "./internal/server/executor/local/fakeacp")
		cmd.Dir = testenv.RepoRoot()
		if out, e := cmd.CombinedOutput(); e != nil {
			err = fmt.Errorf("build fakeacp: %v\n%s", e, out)
			return
		}
		// The first time a freshly built binary runs, the system checks it first (on macOS that is 10 to 25 times
		// slower than later runs when the machine is idle, and can take several seconds when all tests run in
		// parallel). Run it once right after building so this one-time cost is paid here, instead of counting
		// against the readiness deadline of the first test that starts an ACP agent.
		if out, e := exec.Command(path, "--version").CombinedOutput(); e != nil {
			err = fmt.Errorf("pre-run fakeacp: %v\n%s", e, out)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}
