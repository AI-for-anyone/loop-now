package larksidecar

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBoundedInputSnapshot(t *testing.T) {
	home := ownedInputHome(t)
	small := filepath.Join(home, "small.json")
	large := filepath.Join(home, "large.json")
	if os.WriteFile(small, []byte(`{"message":"synthetic"}`), 0600) != nil || os.WriteFile(large, []byte(`{"message":"`+strings.Repeat("x", 257)+`"}`), 0600) != nil {
		t.Fatal("fixture write")
	}
	args := []string{"api", "POST", "/open-apis/im/v1/messages", "--data", "@small.json", "--params={\"receive_id_type\":\"chat_id\"}"}
	got, e := boundedInputs(args, home, 256)
	if e != nil || got[4] != `{"message":"synthetic"}` || args[4] != "@small.json" {
		t.Fatal("input snapshot changed original args or failed")
	}
	_ = os.WriteFile(small, []byte(`{"message":"changed"}`), 0600)
	if got[4] != `{"message":"synthetic"}` {
		t.Fatal("snapshot kept mutable file reference")
	}
	for _, bad := range [][]string{{"api", "--data", "@large.json"}, {"api", "--data=-"}, {"api", "--params", "-"}, {"api", "--file", "@small.json"}, {"api", "--data", "@missing"}, {"api", "--data", "@."}} {
		if _, e := boundedInputs(bad, home, 256); e == nil {
			t.Fatal("unbounded/invalid input admitted")
		}
	}
	for _, bad := range [][]string{{}, {"auth", "login"}, {"api", "--profile=trap"}, {"api", "--as=bot"}, {"api", "--as"}, {"api", strings.Repeat("x", 65)}} {
		if safeArgs(bad, 64) {
			t.Fatal("invalid launcher arguments admitted")
		}
	}
}

func TestBoundedInputRejectsFIFOWithoutWaiting(t *testing.T) {
	home := ownedInputHome(t)
	p := filepath.Join(home, "synthetic-input.fifo")
	if e := syscall.Mkfifo(p, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := boundedInputs([]string{"api", "--data", "@" + p}, home, 1024); done <- e }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO admitted")
		}
	case <-time.After(200 * time.Millisecond):
		// Safe first-failure cleanup: unblock only this self-owned reader, so the
		// regression never leaves a goroutine or named-pipe handle hanging.
		fd, e := os.OpenFile(p, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if e != nil {
			t.Fatal(e)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			fd.Close()
			t.Fatal("owned FIFO reader failed to release")
		}
		fd.Close()
		t.Fatal("non-regular input blocked before validation")
	}
}

func TestBoundedInputsAggregateBeforeExpansion(t *testing.T) {
	home := ownedInputHome(t)
	payload := []byte(`{"v":"` + strings.Repeat("x", 80) + `"}`)
	for _, name := range []string{"a.json", "b.json"} {
		if e := os.WriteFile(filepath.Join(home, name), payload, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := boundedInputs([]string{"api", "--data", "@a.json", "--params=@b.json"}, home, 128); e == nil {
		t.Fatal("aggregate input was expanded beyond configured byte budget")
	}
}

func TestBoundedInputsRejectOutsideScopeAndParentLink(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	outside, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(outside, "synthetic.json")
	if os.WriteFile(p, []byte(`{"fixture":"outside-scope"}`), 0600) != nil {
		t.Fatal("fixture write")
	}
	link := filepath.Join(home, "parent-link")
	if os.Symlink(outside, link) != nil {
		t.Fatal("fixture symlink")
	}
	for _, path := range []string{p, filepath.Join(link, "synthetic.json")} {
		if _, e := boundedInputs([]string{"api", "--data", "@" + path}, home, 1024); e == nil {
			t.Fatal("server pre-read JSON outside owned input root")
		}
	}
}

func ownedInputHome(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestTrustedInputRootsStayExplicit(t *testing.T) {
	home := ownedInputHome(t)
	workspace := ownedInputHome(t)
	p := filepath.Join(workspace, "qa.json")
	if e := os.WriteFile(p, []byte(`{"text":"scope fixture"}`), 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"api", "--data", "@" + p}
	if _, e := boundedInputs(args, home, 1024); e == nil {
		t.Fatal("unlisted workspace root accepted")
	}
	out, e := boundedInputsWithRoots(args, home, []string{workspace}, 1024)
	if e != nil || out[2] != `{"text":"scope fixture"}` {
		t.Fatal("trusted Go root was not used")
	}
	alias := filepath.Join(home, "linked-workspace")
	if e := os.Symlink(workspace, alias); e != nil {
		t.Fatal(e)
	}
	if _, e := boundedInputsWithRoots([]string{"api", "--data", "@" + filepath.Join(alias, "qa.json")}, home, []string{workspace}, 1024); e == nil {
		t.Fatal("parent symlink bypassed explicit root path")
	}
	if _, e := boundedInputs([]string{"api", "--data", "@" + p, "--input-root", workspace}, home, 1024); e == nil {
		t.Fatal("argv added a trusted root")
	}
}

// Test-only convenience: a single synthetic workspace is explicitly trusted.
func boundedInputs(args []string, base string, max int64) ([]string, error) {
	return boundedInputsWithRoots(args, base, []string{base}, max)
}

func TestInputDotDotCannotEscapeWorkspace(t *testing.T) {
	scope := ownedInputHome(t)
	work := filepath.Join(scope, "workspace")
	outside := filepath.Join(scope, "outside")
	for _, p := range []string{work, outside} {
		if os.Mkdir(p, 0700) != nil {
			t.Fatal("fixture mkdir")
		}
	}
	if os.WriteFile(filepath.Join(outside, "fixture.json"), []byte(`{"outside":true}`), 0600) != nil {
		t.Fatal("fixture write")
	}
	if _, e := boundedInputsWithRoots([]string{"api", "--data", "@../outside/fixture.json"}, work, []string{work}, 1024); e == nil {
		t.Fatal("dotdot escaped workspace")
	}
}

func TestInputDirectorySwapCannotRedirectPinnedFD(t *testing.T) {
	work := ownedInputHome(t)
	outside := ownedInputHome(t)
	parent := filepath.Join(work, "data")
	park := filepath.Join(work, "park")
	if os.Mkdir(parent, 0700) != nil {
		t.Fatal("fixture mkdir")
	}
	if os.WriteFile(filepath.Join(parent, "fixture.json"), []byte(`{"inside":true}`), 0600) != nil || os.WriteFile(filepath.Join(outside, "fixture.json"), []byte(`{"outside":true}`), 0600) != nil {
		t.Fatal("fixture write")
	}
	rootfd, e := openScopeRoot(work)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(rootfd)
	// Deterministic TOCTOU barrier: acquire/validate the real production root fd,
	// replace the parent component, then perform the production relative open.
	swap := make(chan struct{})
	swapped := make(chan error, 1)
	restore := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-swap
		if e := os.Rename(parent, park); e != nil {
			swapped <- e
			return
		}
		if e := os.Symlink(outside, parent); e != nil {
			_ = os.Rename(park, parent)
			swapped <- e
			return
		}
		swapped <- nil
		<-restore
		_ = os.Remove(parent)
		_ = os.Rename(park, parent)
	}()
	close(swap)
	if e := <-swapped; e != nil {
		<-done
		t.Fatal(e)
	}
	f, e := openInputAt(rootfd, "data/fixture.json")
	if f != nil {
		f.Close()
	}
	close(restore)
	<-done
	if e == nil {
		t.Fatal("directory swap redirected scoped open")
	}
	f, e = openInputAt(rootfd, "data/fixture.json")
	if e != nil {
		t.Fatal("restored positive control failed")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 1024))
	if e != nil || string(raw) != `{"inside":true}` {
		t.Fatal("scoped descriptor did not preserve inside data")
	}
}
