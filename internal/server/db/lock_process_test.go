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
		t.Fatalf("父进程持锁时子进程应取锁失败：%v\n%s", err, out)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if out, err := child("try").CombinedOutput(); err != nil || !strings.Contains(string(out), "acquired") {
		t.Fatalf("父进程释放后子进程应能取锁：%v\n%s", err, out)
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
		t.Fatalf("持锁子进程没有取到锁：%q %v", line, err)
	}
	if _, err := LockDir(dir); !errors.Is(err, ErrLocked) {
		holder.Process.Kill()
		t.Fatalf("子进程持锁时父进程应得 ErrLocked，得到 %v", err)
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
			t.Fatalf("持锁进程被杀后锁应自动释放：%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLockDirChild only runs in the child process started by TestLockDirAcrossProcesses.
func TestLockDirChild(t *testing.T) {
	dir := os.Getenv(lockChildDir)
	if dir == "" {
		t.Skip("只在 TestLockDirAcrossProcesses 的子进程里运行")
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
