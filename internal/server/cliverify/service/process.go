package service

import (
	"bytes"
	"context"
	"errors"
	"loop-now/internal/server/cliverify/probe"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// CommandSpec is private execution data. Only an explicit Go fixture may replace
// a command; no environment flag, HTTP field or credential can select it.
type CommandSpec struct {
	CLI, Phase, Executable, Home string
	Args, Environment            []string
}

func (CommandSpec) String() string   { return "[private CLI verification command]" }
func (CommandSpec) GoString() string { return "[private CLI verification command]" }
func (CommandSpec) MarshalJSON() ([]byte, error) {
	return nil, errors.New("验证执行环境不可序列化")
}

type CommandFactory func(context.Context, CommandSpec) (*exec.Cmd, error)
type privateOutput struct {
	Stdout, Stderr                []byte
	ExitCode                      int
	Cancelled, Overflow, TimedOut bool
}

func (privateOutput) String() string   { return "[private CLI verification output]" }
func (privateOutput) GoString() string { return "[private CLI verification output]" }

type limitedCapture struct {
	b        bytes.Buffer
	overflow chan struct{}
	once     *sync.Once
}

func (w *limitedCapture) Write(p []byte) (int, error) {
	n := len(p)
	left := probe.MaxOutputBytes - w.b.Len()
	if left < len(p) {
		w.once.Do(func() { close(w.overflow) })
		if left > 0 {
			w.b.Write(p[:left])
		}
	} else {
		w.b.Write(p)
	}
	return n, nil
}
func runProcess(ctx context.Context, spec CommandSpec, grace time.Duration, factory CommandFactory) privateOutput {
	out := privateOutput{ExitCode: -1}
	if ctx.Err() != nil {
		out.Cancelled = true
		return out
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	if factory != nil {
		var e error
		cmd, e = factory(ctx, spec)
		if e != nil || cmd == nil {
			return out
		}
	}
	cmd.Dir = spec.Home
	// Only the trusted Go fixture may deliberately supply an environment
	// (e.g. local Unix transport/CA). Production always uses the private spec.
	if factory == nil || cmd.Env == nil {
		cmd.Env = append([]string{}, spec.Environment...)
	}
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = grace + time.Second
	overflow := make(chan struct{})
	once := &sync.Once{}
	stdout, stderr := &limitedCapture{overflow: overflow, once: once}, &limitedCapture{overflow: overflow, once: once}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if e := cmd.Start(); e != nil {
		return out
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		out.Cancelled = true
		err = stopProcessGroup(cmd, done, grace)
	case <-overflow:
		out.Overflow = true
		err = stopProcessGroup(cmd, done, grace)
	}
	select {
	case <-overflow:
		out.Overflow = true
	default:
	}
	// The parent may exit while a detached-from-stdio child remains in its PG.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	out.Stdout = stdout.b.Bytes()
	out.Stderr = stderr.b.Bytes()
	if err == nil {
		out.ExitCode = 0
	} else {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out.ExitCode = exit.ExitCode()
		}
	}
	return out
}
func stopProcessGroup(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	var err error
	reaped := false
	for {
		select {
		case err = <-done:
			reaped = true
			done = nil
		case <-timer.C:
			// Even an already reaped parent does not prove that its process group is
			// empty. Kill TERM-resistant children before releasing private HOME.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if reaped {
				return err
			}
			return <-done
		}
	}
}
