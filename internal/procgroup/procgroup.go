// Package procgroup 把子进程连同它拉起的所有后代当成一个组来管理：Unix 上是进程组（Setpgid 加 kill(-pid)），
// Windows 上是 Job Object（KILL_ON_JOB_CLOSE 加 TerminateJobObject）。调用方的写法在两个平台一致：
// Start 之前 Configure，Start 之后 Started，之后用组长的 PID 查询或结束整组。
package procgroup

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
)

// ErrNotFound 表示这个组里已经没有任何进程（Unix 上就是 ESRCH）。
var ErrNotFound = errNotFound

// Configure 让 cmd 以新组的组长启动。必须在 cmd.Start 之前调用。
func Configure(cmd *exec.Cmd) { configure(cmd) }

// Started 登记一个已经 Start 成功的 cmd：Windows 上在这里创建 Job Object 并把进程放进去；Unix 上无事可做。
// 返回错误表示这个进程没法纳入整组管理，调用方应当把它结束掉。
func Started(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("procgroup: 进程尚未启动")
	}
	return started(cmd)
}

// Kill 立即结束 pid 为组长的整个组。组已经不存在时返回 ErrNotFound。
func Kill(pid int) error { return kill(pid) }

// Terminate 请整组停止：Unix 上发 SIGTERM；Windows 没有可投递给任意进程的终止信号，等同于 Kill。
func Terminate(pid int) error { return terminate(pid) }

// Probe 查看组里是否还有进程：有返回 nil，没有返回 ErrNotFound，其他错误表示无法确认。
func Probe(pid int) error { return probe(pid) }

// Alive 报告组里是否还有进程。
func Alive(pid int) bool { return probe(pid) == nil }

// ProcessAlive 报告 pid 这个进程本身是否还存在（不看它的组）。
func ProcessAlive(pid int) bool { return processAlive(pid) }

// Cancel 返回适合赋给 cmd.Cancel 的函数：结束整组，组已不存在时按进程已结束处理。
func Cancel(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := kill(cmd.Process.Pid)
		if errors.Is(err, ErrNotFound) {
			return os.ErrProcessDone
		}
		return err
	}
}

// Start 是 Configure、cmd.Start、Started 三步的组合：启动失败或无法纳入整组管理时返回错误（后者已把进程结束）。
// 用 exec.CommandContext 建的 cmd 由调用方自己设置 cmd.Cancel = Cancel(cmd)（没有 Context 的 cmd 不允许设置 Cancel）。
func Start(cmd *exec.Cmd) error {
	configure(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := started(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

// Run 相当于 cmd.Run，但进程以整组方式启动。
func Run(cmd *exec.Cmd) error {
	if err := Start(cmd); err != nil {
		return err
	}
	return cmd.Wait()
}

// Output 相当于 cmd.Output（标准错误未设置时随 *exec.ExitError 返回），但进程以整组方式启动。
func Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("procgroup: Stdout 已经设置")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr *bytes.Buffer
	if cmd.Stderr == nil {
		stderr = &bytes.Buffer{}
		cmd.Stderr = stderr
	}
	err := Run(cmd)
	if err != nil && stderr != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exit.Stderr = stderr.Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput 相当于 cmd.CombinedOutput，但进程以整组方式启动。
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, errors.New("procgroup: Stdout 或 Stderr 已经设置")
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := Run(cmd)
	return out.Bytes(), err
}
