//go:build linux

package local

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
)

// processStart 返回进程的启动时刻（/proc/<pid>/stat 第 22 项：开机后的时钟滴答数），与 PID 一起确认它没有被别的进程复用。
func processStart(pid int) (string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// 第 2 项是括号里的进程名，可能含空格与括号：从最后一个右括号之后数，第 3 项起。
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
