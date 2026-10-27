package pirpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var fakepiBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pirpc-fakepi-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakepiBin, err = buildFakepi(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func buildFakepi(dir string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "fakepi")
	cmd := exec.Command("go", "build", "-o", out, "./internal/server/executor/local/fakepi")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("构建 fakepi 失败：%v\n%s", err, b)
	}
	return out, nil
}

// moduleRoot 从当前目录向上查找 go.mod。
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("未找到 go.mod")
		}
		dir = parent
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type fakeProc struct {
	*Client
	cmd *exec.Cmd
	dir string
}

// startFake 启动 fakepi RPC 进程；测试结束时关闭 stdin 并等待退出，超时则强杀。
func startFake(t *testing.T, env []string, args ...string) *fakeProc {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(fakepiBin, append([]string{"--mode", "rpc", "--offline", "--no-approve"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin",
		"HOME=" + dir,
		"PI_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"),
	}, env...)
	c, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.CloseStdin()
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-c.Done()
		}
	})
	return &fakeProc{Client: c, cmd: cmd, dir: dir}
}

func waitDone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("等待进程结束超时")
	}
}

// recorder 记录 TurnSink 回调。
type recorder struct {
	mu     sync.Mutex
	deltas []string
	tools  []string
	first  chan struct{}
	once   sync.Once
}

func newRecorder() *recorder { return &recorder{first: make(chan struct{})} }

func (r *recorder) TextDelta(d string) {
	r.mu.Lock()
	r.deltas = append(r.deltas, d)
	r.mu.Unlock()
	r.once.Do(func() { close(r.first) })
}

func (r *recorder) ToolStarted(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, "start "+id+" "+name)
}

func (r *recorder) ToolEnded(id, name string, isError bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, fmt.Sprintf("end %s %s %v", id, name, isError))
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.deltas, "")
}

func (r *recorder) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.deltas...), append([]string(nil), r.tools...)
}

func (r *recorder) waitFirst(t *testing.T) {
	t.Helper()
	select {
	case <-r.first:
	case <-time.After(10 * time.Second):
		t.Fatal("等待首个文本增量超时")
	}
}

type promptOut struct {
	res TurnResult
	err error
}

func goPrompt(ctx context.Context, c *Client, text string, sink TurnSink) <-chan promptOut {
	ch := make(chan promptOut, 1)
	go func() {
		res, err := c.Prompt(ctx, text, sink)
		ch <- promptOut{res, err}
	}()
	return ch
}

func recvPrompt(t *testing.T, ch <-chan promptOut) promptOut {
	t.Helper()
	select {
	case out := <-ch:
		return out
	case <-time.After(15 * time.Second):
		t.Fatal("等待 Prompt 返回超时")
		return promptOut{}
	}
}
