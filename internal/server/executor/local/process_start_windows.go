//go:build windows

package local

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// processStart 返回内核记录的进程创建时刻（GetProcessTimes，100 纳秒精度），与 PID 一起确认它没有被别的进程复用。
func processStart(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", creation.Nanoseconds()), nil
}

// processCommandLine 在 Windows 上不读别的进程的命令行：没有记录启动时刻的旧 pi.pid 一律不确认。
func processCommandLine(int) (string, error) { return "", fmt.Errorf("不支持读取进程命令行") }
