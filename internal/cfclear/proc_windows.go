package cfclear

import (
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the browser joins a job object that ends its processes when
// the job's last handle closes. ppgmods holds that handle until it exits,
// however it exits (a crash too), so a browser kept running between reads
// is never left behind.

var (
	jobOnce sync.Once
	job     windows.Handle
)

func killJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
		}
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		job = h
	})
	return job
}

// dieWithParent: nothing to set before the start on Windows (see
// tieToParent).
func dieWithParent(*exec.Cmd) {}

// tieToParent puts a started browser in ppgmods' kill-on-close job; its own
// child processes (tabs, GPU) join it as they start.
func tieToParent(p *os.Process) {
	j := killJob()
	if j == 0 || p == nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	windows.AssignProcessToJobObject(j, h)
}
