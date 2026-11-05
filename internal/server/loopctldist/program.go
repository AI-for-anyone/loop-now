package loopctldist

import "runtime"

// localProgram 是安装到 bin 目录里的文件名：Windows 上带 .exe，否则就是 Program。
func localProgram() string {
	if runtime.GOOS == "windows" {
		return Program + ".exe"
	}
	return Program
}
