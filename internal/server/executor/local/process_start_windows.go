//go:build windows

package local

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// processStart returns the process creation time recorded by the kernel (GetProcessTimes, 100-nanosecond precision),
// which together with the PID confirms the PID has not been reused by another process.
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

// processCommandLine does not read other processes' command lines on Windows: an old pi.pid without a recorded start
// time is never confirmed.
func processCommandLine(int) (string, error) { return "", fmt.Errorf("不支持读取进程命令行") }
