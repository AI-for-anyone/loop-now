//go:build windows

package procgroup

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNotFound = errors.New("procgroup: no such process group")

const stillActive = 259 // STILL_ACTIVE

// jobs records the Job Objects this process created, indexed by the group leader's PID. A handle is closed by the
// watch goroutine after the group leader exits; on close, KILL_ON_JOB_CLOSE ends the descendants left in the group,
// matching the Unix approach of "kill(-pid) after the group leader exits".
var jobs struct {
	sync.Mutex
	m map[int]windows.Handle
}

// configure: a new console process group (a Ctrl+C the server itself receives is not passed on to the child,
// the counterpart of detaching from the terminal with Setpgid on Unix), and no console window (when the desktop
// shell starts the server in the background, console programs such as node.exe would otherwise each open a
// window).
func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
}

// started creates a Job Object for the process (KILL_ON_JOB_CLOSE: the whole group ends when all handles are
// closed, so even a server crash leaves no orphans), puts the process into it and starts watching it. Since
// Windows 8 Jobs can be nested, so this also works when the server itself runs inside another Job (desktop shell,
// CI runner).
// There is a small window between CreateProcess and Assign during which children the process starts are not in
// the Job; node takes far longer than that to start.
func started(cmd *exec.Cmd) error {
	pid := cmd.Process.Pid
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	if err = windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(proc)
		windows.CloseHandle(job)
		return err
	}
	jobs.Lock()
	if jobs.m == nil {
		jobs.m = map[int]windows.Handle{}
	}
	if old, ok := jobs.m[pid]; ok { // PID reused while the old entry is still there: the old Job has long been empty, just close it
		windows.CloseHandle(old)
	}
	jobs.m[pid] = job
	jobs.Unlock()
	go watch(pid, proc, job)
	return nil
}

// watch waits for the group leader to exit and then closes the Job handle; closing it ends any descendants still
// alive.
func watch(pid int, proc, job windows.Handle) {
	_, _ = windows.WaitForSingleObject(proc, windows.INFINITE)
	windows.CloseHandle(proc)
	jobs.Lock()
	if jobs.m[pid] == job {
		delete(jobs.m, pid)
	}
	jobs.Unlock()
	windows.CloseHandle(job)
}

// withJob hands the Job handle registered by this process to fn while holding the lock: the watch goroutine only
// removes a handle from the table and closes it after taking the lock, so the handle used in fn is always valid.
// It returns false when nothing is registered.
func withJob(pid int, fn func(windows.Handle) error) (bool, error) {
	jobs.Lock()
	defer jobs.Unlock()
	job, ok := jobs.m[pid]
	if !ok {
		return false, nil
	}
	return true, fn(job)
}

// jobProcessIDs returns the PIDs of all processes registered in the Job (JobObjectBasicProcessIdList). A process
// that has exited but whose process object has not been destroyed yet (the parent or this package's watch goroutine
// still holds its handle) is still in the list and still counted in ActiveProcesses, so liveness has to be checked
// separately.
func jobProcessIDs(job windows.Handle) ([]uint32, error) {
	const header = 8 // two DWORDs: NumberOfAssignedProcesses, NumberOfProcessIdsInList
	ptr := int(unsafe.Sizeof(uintptr(0)))
	capacity := 64
	for {
		buf := make([]uint64, (header+capacity*ptr+7)/8)
		base := unsafe.Pointer(&buf[0])
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(base), uint32(len(buf)*8), nil)
		assigned := int(*(*uint32)(base))
		listed := int(*(*uint32)(unsafe.Add(base, 4)))
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, err
		}
		if err != nil || listed < assigned {
			// The list did not fit: grow to the total the kernel reported and query again (the count can still
			// change between the two queries, so leave some headroom).
			capacity = max(assigned, capacity*2) + 16
			continue
		}
		pids := make([]uint32, listed)
		for i := range pids {
			pids[i] = uint32(*(*uintptr)(unsafe.Add(base, header+i*ptr)))
		}
		return pids, nil
	}
}

// jobAlive reports whether the Job still has a process that has not exited.
//
// Looking at ActiveProcesses alone is not enough: after the group leader exits, the kernel first signals its
// process handle (which is what makes cmd.Wait return), and only removes it from the Job's count once the process
// object is destroyed; Go's os.Process and this package's watch goroutine may still hold handles at that point.
// Querying ActiveProcesses in that window returns 1, which makes the check "the process has exited and the group is
// empty" fail now and then (the Windows runner on CI has hit this).
// So instead enumerate the PIDs in the group and check for each whether its exit code is still STILL_ACTIVE.
func jobAlive(job windows.Handle) (bool, error) {
	pids, err := jobProcessIDs(job)
	if err != nil {
		return false, err
	}
	for _, pid := range pids {
		if processAlive(int(pid)) {
			return true, nil
		}
	}
	return false, nil
}

// kill ends the whole group: a Job this process created is ended with TerminateJobObject; for a process left by a
// previous server instance (only a pid, no Job) it falls back to ending that process itself - its descendants
// already ended when the previous instance exited and its Job handle was closed.
func kill(pid int) error {
	if ok, err := withJob(pid, func(job windows.Handle) error {
		if live, err := jobAlive(job); err == nil && !live {
			return errNotFound
		}
		return windows.TerminateJobObject(job, 1)
	}); ok {
		return err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return errNotFound
		}
		return err
	}
	defer windows.CloseHandle(proc)
	var code uint32
	if err = windows.GetExitCodeProcess(proc, &code); err == nil && code != stillActive {
		return errNotFound
	}
	return windows.TerminateProcess(proc, 1)
}

func terminate(pid int) error { return kill(pid) }

func probe(pid int) error {
	if ok, err := withJob(pid, func(job windows.Handle) error {
		live, err := jobAlive(job)
		if err != nil {
			return err
		}
		if !live {
			return errNotFound
		}
		return nil
	}); ok {
		return err
	}
	if processAlive(pid) {
		return nil
	}
	return errNotFound
}

func processAlive(pid int) bool {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(proc)
	var code uint32
	if err = windows.GetExitCodeProcess(proc, &code); err != nil {
		return false
	}
	return code == stillActive
}
