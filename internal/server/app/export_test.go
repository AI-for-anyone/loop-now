package app

// ReleaseDataDirLock 在同一进程里模拟进程被强制结束：那时操作系统会放开数据目录锁。只放开锁，不关库、不停 Pi。
func (a *App) ReleaseDataDirLock() { _ = a.dirLock.Release() }

// WriteReadyFile 与 ConsoleFiles 给测试直接检验就绪文件与控制台文件的选择。
var (
	WriteReadyFile = writeReadyFile
	ConsoleFiles   = consoleFiles
)
