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

// jobs 记录本进程创建的 Job Object，按组长 PID 索引。句柄在组长退出后由 watch 协程关闭；
// 关闭时 KILL_ON_JOB_CLOSE 会结束组里剩下的后代，与 Unix 上“组长退出后 kill(-pid)”的做法一致。
var jobs struct {
	sync.Mutex
	m map[int]windows.Handle
}

// configure：新的控制台进程组（服务端自己收到 Ctrl+C 时不会传给子进程，对应 Unix 的 Setpgid 脱离终端），
// 不弹控制台窗口（服务端由桌面壳在后台拉起时，node.exe 这类控制台程序否则会各开一个窗口）。
func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
}

// started 为进程建一个 Job Object（KILL_ON_JOB_CLOSE：句柄全部关闭时整组结束，服务端崩溃也不留孤儿），
// 把进程放进去并开始守护。Windows 8 起 Job 可以嵌套，服务端自己在别的 Job 里（桌面壳、CI runner）也能成功。
// 进程从 CreateProcess 到 Assign 之间有一小段窗口，这期间它拉起的子进程不在 Job 里；node 启动远慢于这段时间。
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
	if old, ok := jobs.m[pid]; ok { // PID 被复用而旧记录还在：旧 Job 里早已没有进程，关掉即可
		windows.CloseHandle(old)
	}
	jobs.m[pid] = job
	jobs.Unlock()
	go watch(pid, proc, job)
	return nil
}

// watch 等组长退出后关闭 Job 句柄：关闭即结束仍存活的后代。
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

// withJob 在持锁期间把本进程登记的 Job 句柄交给 fn：watch 协程只在拿到锁之后才从表里删掉并关闭句柄，
// fn 里用的句柄因此一定有效。没有登记时返回 false。
func withJob(pid int, fn func(windows.Handle) error) (bool, error) {
	jobs.Lock()
	defer jobs.Unlock()
	job, ok := jobs.m[pid]
	if !ok {
		return false, nil
	}
	return true, fn(job)
}

// jobProcessIDs 返回 Job 里登记的全部进程 PID（JobObjectBasicProcessIdList）。已经退出但进程对象还没销毁的进程
// （父进程或本包的 watch 协程还握着它的句柄）仍在列表里，也仍计入 ActiveProcesses，所以存活要另行判断。
func jobProcessIDs(job windows.Handle) ([]uint32, error) {
	const header = 8 // NumberOfAssignedProcesses、NumberOfProcessIdsInList 两个 DWORD
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
			// 列表装不下：按内核报的总数放大再查一次（数量在两次查询之间还可能变化，所以留余量）。
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

// jobAlive 报告 Job 里是否还有没退出的进程。
//
// 不能只看 ActiveProcesses：组长退出后内核先让它的进程句柄进入有信号状态（cmd.Wait 由此返回），
// 直到进程对象销毁才把它从 Job 的计数里去掉；而 Go 的 os.Process 与本包的 watch 协程此时可能还握着句柄。
// 在这个窗口里查 ActiveProcesses 会得到 1，让“进程已退出且组里没人”的确认偶发失败（CI 上的 Windows runner 就撞到过）。
// 这里改为枚举组里的 PID，逐个看退出码是否仍是 STILL_ACTIVE。
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

// kill 结束整组：本进程建的 Job 用 TerminateJobObject；上一个服务端实例留下的进程（只有 pid，没有 Job）退回到
// 结束该进程本身——它的后代在上一个实例退出、Job 句柄关闭时已经随之结束。
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
