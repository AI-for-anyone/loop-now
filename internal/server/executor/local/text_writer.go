package local

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/executor"
)

// textWriter is a driver that writes one short text with the runtime's CLI and its local login ("Write with AI"); the
// registry's TextGeneration is set exactly for these runtimes (a test checks this). dir is an empty folder to run the
// CLI in and scratch a folder for the driver's own files outside it; both are removed afterwards. Failures are
// *executor.TextFailure; when ctx ends, the CLI is stopped at once and WriteText returns ctx's error.
type textWriter interface {
	writeText(ctx context.Context, dir, scratch string, req executor.TextRequest) (string, error)
}

// WriteText writes one short text with the local CLI of req.Runtime (see executor.TextRequest). It returns the CLI's
// reply as is; an empty reply is not an error here.
func (e *Executor) WriteText(ctx context.Context, req executor.TextRequest) (string, error) {
	w, ok := e.driverByID(req.Runtime).(textWriter)
	if !ok {
		return "", &executor.TextFailure{Unavailable: true, Message: "This runtime does not write texts with AI."}
	}
	root, err := os.MkdirTemp(e.o.TextTempDir, "loop-now-write-")
	if err != nil {
		return "", fmt.Errorf("create a temporary folder: %w", err)
	}
	defer os.RemoveAll(root)
	// The CLI sees the folder by its real path (on macOS the temporary folder is behind a symlink).
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	dir := filepath.Join(root, "work")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("create a temporary folder: %w", err)
	}
	text, err := w.writeText(ctx, dir, root, req)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	return text, err
}

// textPrompt is the whole message for a CLI that takes the instructions in the message itself.
func textPrompt(req executor.TextRequest) string {
	if req.Instructions == "" {
		return req.Prompt
	}
	return req.Instructions + "\n\n" + req.Prompt
}

// textUnavailable and textFailed are a writer's failures, in plain words.
func textUnavailable(format string, a ...any) error {
	return &executor.TextFailure{Unavailable: true, Message: fmt.Sprintf(format, a...)}
}

func textFailed(format string, a ...any) error {
	return &executor.TextFailure{Message: fmt.Sprintf(format, a...)}
}

// oneShotStderr is how much of a one-shot command's error output is kept (its end).
const oneShotStderr = 8 << 10

// oneShotResult is how a one-shot command ended: its exit error (nil when it exited 0, or when it was stopped because
// of its output) and the end of its error output.
type oneShotResult struct {
	exit   error
	stderr string
}

// oneShot runs cmd once with stdin as its input, in its own process group, and passes each line of its output to line;
// line returning false ends the process. When ctx ends, the process and its group end at once. The error is for a
// command that could not be started or waited for.
func oneShot(ctx context.Context, cmd *exec.Cmd, stdin string, line func([]byte) bool) (oneShotResult, error) {
	cmd.Stdin = strings.NewReader(stdin)
	stderr := &tailBuffer{limit: oneShotStderr}
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return oneShotResult{}, err
	}
	if err := procgroup.Start(cmd); err != nil {
		return oneShotResult{}, err
	}
	var once sync.Once
	stoppedByLine := false
	kill := func() { once.Do(func() { _ = procgroup.Kill(cmd.Process.Pid) }) }
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-done:
		}
	}()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		if !line(sc.Bytes()) {
			stoppedByLine = true
			kill()
			break
		}
	}
	_, _ = io.Copy(io.Discard, out)
	waitErr := cmd.Wait()
	close(done)
	if stoppedByLine {
		waitErr = nil
	}
	var exit *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exit) && ctx.Err() == nil {
		return oneShotResult{stderr: stderr.String()}, waitErr
	}
	return oneShotResult{exit: waitErr, stderr: stderr.String()}, nil
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	b     []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.limit {
		t.b = append([]byte(nil), t.b[len(t.b)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// lastLines returns the last n non-empty lines of s, joined with spaces, for a message.
func lastLines(s string, n int) string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, " ")
}
