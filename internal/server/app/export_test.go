package app

// ReleaseInstanceLocks simulates, within the same process, the process being killed: the operating system would then
// release the instance locks on the data directory and the work root. It only releases the locks; it does not close the
// database or stop Pi.
func (a *App) ReleaseInstanceLocks() {
	_ = a.workLock.Release()
	_ = a.dirLock.Release()
}

// WriteReadyFile and ConsoleFiles let tests check the ready file and the choice of console files directly.
var (
	WriteReadyFile = writeReadyFile
	ConsoleFiles   = consoleFiles
)
