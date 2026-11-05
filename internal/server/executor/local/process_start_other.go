//go:build !darwin && !linux && !windows

package local

import "errors"

// processStart 在其他平台上不可用：pi.pid 不记录启动时刻，遗留回收退回到比对命令行。
func processStart(int) (string, error) { return "", errors.New("不支持读取进程启动时刻") }
