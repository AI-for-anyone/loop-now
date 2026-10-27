package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestReleaseReadOnlyHomePreservesWorkspaceAndSessions(t *testing.T) {
	e := &Executor{o: Options{WorkRoot: t.TempDir()}, handoffs: map[uuid.UUID]*RuntimeHandoff{}}
	id := uuid.New()
	d := e.dirs(id)
	if err := d.ensure(); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(d.home, "go/pkg/mod/fixture@v1/nested")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(cache, "source.go"), filepath.Join(d.workspace, "kept"), filepath.Join(d.sessions, "kept.jsonl"), d.pidFile} {
		if err := os.WriteFile(p, []byte("preserved fixture"), 0444); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{cache, filepath.Dir(cache)} {
		if err := os.Chmod(p, 0555); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := e.Release(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{d.home, d.pidFile} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("release retained %s: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(d.workspace, "kept"), filepath.Join(d.sessions, "kept.jsonl")} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "preserved fixture" {
			t.Fatalf("release changed retained data: %q %v", b, err)
		}
	}
}

func TestReleaseReadOnlyHomeDoesNotFollowLinks(t *testing.T) {
	e := &Executor{o: Options{WorkRoot: t.TempDir()}, handoffs: map[uuid.UUID]*RuntimeHandoff{}}
	id := uuid.New()
	d := e.dirs(id)
	if err := d.ensure(); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	proof := filepath.Join(outside, "proof")
	if err := os.WriteFile(proof, []byte("outside"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outside, 0700) })
	if err := os.Symlink(outside, filepath.Join(d.home, "host-cache")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(d.home, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("outside directory mode changed: %v %v", info, err)
	}
	if b, err := os.ReadFile(proof); err != nil || string(b) != "outside" {
		t.Fatalf("outside contents changed: %q %v", b, err)
	}
}

func TestReleaseReadOnlyHomeRejectsManagedPathLinks(t *testing.T) {
	for _, component := range []string{"root", "agents", "runtime", "home"} {
		t.Run(component, func(t *testing.T) {
			e := &Executor{o: Options{WorkRoot: filepath.Join(t.TempDir(), "root")}, handoffs: map[uuid.UUID]*RuntimeHandoff{}}
			id := uuid.New()
			d := e.dirs(id)
			if err := d.ensure(); err != nil {
				t.Fatal(err)
			}
			p := map[string]string{"root": e.o.WorkRoot, "agents": filepath.Dir(d.root), "runtime": d.root, "home": d.home}[component]
			moved := p + "-original"
			if err := os.Rename(p, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, p); err != nil {
				t.Fatal(err)
			}
			if err := e.Release(context.Background(), id); err == nil {
				t.Fatal("managed path symlink accepted")
			}
			if _, err := os.Lstat(moved); err != nil {
				t.Fatalf("link target removed: %v", err)
			}
		})
	}
}

func TestReleaseReadOnlyHomeRejectsOtherUID(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0555); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Exercise the actual owner check without requiring chown or a second user.
	if err = removeOwnedDirectoryAt(int(f.Fd()), "child", uint32(os.Getuid())+1); err == nil {
		t.Fatal("foreign directory owner accepted")
	}
	info, err := os.Stat(child)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("rejected directory changed: %v %v", info, err)
	}
}

func TestReleaseReadOnlyHomePermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("OS directory-access denial requires an unprivileged test uid")
	}
	e := &Executor{o: Options{WorkRoot: t.TempDir()}, handoffs: map[uuid.UUID]*RuntimeHandoff{}}
	id := uuid.New()
	d := e.dirs(id)
	if err := d.ensure(); err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(d.home, "proof")
	if err := os.WriteFile(proof, []byte("do not lose"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.home, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d.home, 0700) })
	if err := e.Release(context.Background(), id); err == nil {
		t.Fatal("unreadable HOME should fail without widening permissions")
	}
	info, err := os.Lstat(d.home)
	if err != nil || info.Mode().Perm() != 0000 {
		t.Fatalf("access-denied directory changed: %v %v", info, err)
	}
	if err := os.Chmod(d.home, 0700); err != nil { // inspect fixture after the failed release
		t.Fatal(err)
	}
	if b, err := os.ReadFile(proof); err != nil || string(b) != "do not lose" {
		t.Fatalf("failed release lost data: %q %v", b, err)
	}
}
