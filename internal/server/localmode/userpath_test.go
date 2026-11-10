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
}

func TestUserPathFallsBackToProcessPath(t *testing.T) {
	proc := filepath.Join(t.TempDir(), "proc-bin")
	t.Setenv("PATH", proc)
	u := NewUserPath(nil, time.Second)
	u.resolve = func(context.Context) (string, string, error) { return "", "shell", errors.New("boom") }
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("失败时应退回进程 PATH：%v", got)
	}
	// 只解析一次。
	u.resolve = func(context.Context) (string, string, error) { t.Fatal("不应再解析"); return "", "", nil }
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("第二次 = %v", got)
	}
}

func TestUserPathDirsHonoursCallerContext(t *testing.T) {
	u := NewUserPath(nil, time.Minute)
	release := make(chan struct{})
	u.resolve = func(context.Context) (string, string, error) { <-release; return "/x", "test", nil }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := u.Dirs(ctx); got != nil {
		t.Fatalf("调用方超时应返回空：%v", got)
	}
	close(release)
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{filepath.Clean("/x")}) && runtime.GOOS != "windows" {
		t.Fatalf("解析完成后 = %v", got)
	}
}

// writeShellProfile 准备一个只含 rc 文件的临时 HOME，SHELL 指向 /bin/sh（以交互式登录方式运行时读 ~/.profile 与 $ENV）。
func writeShellProfile(t *testing.T, profile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不读登录 shell")
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
	writeShellProfile(t, "echo 欢迎信息\nexport PATH='"+custom+"':\"$PATH\"\necho 更多输出\n")
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
		t.Fatalf("应在时限内失败：%v %s", err, time.Since(begin))
	}
	// 整个解析器退回进程 PATH。
	proc := filepath.Join(t.TempDir(), "proc-bin")
	t.Setenv("PATH", proc)
	u := NewUserPath(nil, 300*time.Millisecond)
	if got := u.Dirs(context.Background()); !slices.Equal(got, []string{proc}) {
		t.Fatalf("超时后应退回进程 PATH：%v", got)
	}
}
