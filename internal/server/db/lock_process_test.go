package db

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const lockChildDir = "LOOP_NOW_DB_LOCK_CHILD_DIR"

// TestLockDirAcrossProcesses checks that the instance lock works across processes: taking it fails while another
// process holds it, and it is released automatically once the holding process is killed.
func TestLockDirAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	child := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLockDirChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), lockChildDir+"="+dir, "LOOP_NOW_DB_LOCK_CHILD_MODE="+mode)
		return cmd
	}

	l, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := child("try").CombinedOutput(); err != nil || !strings.Contains(string(out), "locked") {
		t.Fatalf("the child should fail to lock while the parent holds the lock: %v\n%s", err, out)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if out, err := child("try").CombinedOutput(); err != nil || !strings.Contains(string(out), "acquired") {
		t.Fatalf("the child should get the lock after the parent releases it: %v\n%s", err, out)
	}

	holder := child("hold")
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.Contains(line, "acquired") {
		holder.Process.Kill()
		t.Fatalf("the holding child did not get the lock: %q %v", line, err)
	}
	if _, err := LockDir(dir); !errors.Is(err, ErrLocked) {
		holder.Process.Kill()
		t.Fatalf("the parent should get ErrLocked while the child holds the lock, got %v", err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := LockDir(dir)
		if err == nil {
			l.Release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lock should be released automatically after its holder is killed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLockDirChild only runs in the child process started by TestLockDirAcrossProcesses.
func TestLockDirChild(t *testing.T) {
	dir := os.Getenv(lockChildDir)
	if dir == "" {
		t.Skip("only runs in the child process of TestLockDirAcrossProcesses")
	}
	l, err := LockDir(dir)
	switch {
	case errors.Is(err, ErrLocked):
		fmt.Println("locked")
		return
	case err != nil:
		fmt.Println("error", err)
		os.Exit(2)
	}
	fmt.Println("acquired")
	if os.Getenv("LOOP_NOW_DB_LOCK_CHILD_MODE") == "hold" {
		select {}
	}
	l.Release()
}
