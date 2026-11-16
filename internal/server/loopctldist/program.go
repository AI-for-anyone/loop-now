package loopctldist

import "runtime"

// localProgram is the file name installed into the bin directory: with .exe on Windows, otherwise just Program.
func localProgram() string {
	if runtime.GOOS == "windows" {
		return Program + ".exe"
	}
	return Program
}
