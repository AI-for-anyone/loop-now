//go:build linux

package local

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
)

// processStart returns the process start time (field 22 of /proc/<pid>/stat: clock ticks since boot), which together
// with the PID confirms the PID has not been reused by another process.
func processStart(pid int) (string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// Field 2 is the process name in parentheses, which may contain spaces and parentheses: count from after the last
	// closing parenthesis, starting at field 3.
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return "", errors.New("无法解析进程状态")
	}
	fields := strings.Fields(string(raw[i+1:]))
	if len(fields) < 20 {
		return "", errors.New("无法解析进程状态")
	}
	return fields[19], nil
}
