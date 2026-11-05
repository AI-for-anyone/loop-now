//go:build unix

package local

import (
	"os/exec"
	"strconv"
)

// processCommandLine 返回 pid 的命令行（ps），供没有记录启动时刻的旧 pi.pid 比对。
func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
