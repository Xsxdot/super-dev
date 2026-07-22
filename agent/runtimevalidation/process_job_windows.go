//go:build windows

// process_job_windows.go 使用 kill-on-close Job Object 回收 Windows runner-owned 子进程树。
//
// 职责：创建受限 Job、attach 真实进程并在 cleanup 时终止全部后代。
// 边界：不管理非本 campaign 进程，也不把 Job Object 当作 recovery ledger。
package runtimevalidation

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessJob struct {
	handle windows.Handle
}

func newProcessTreeController() (processTreeController, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Windows process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("configure Windows process job: %w", err)
	}
	return &windowsProcessJob{handle: handle}, nil
}

func (j *windowsProcessJob) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func (j *windowsProcessJob) Attach(process *os.Process) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(j.handle, handle)
}

func (j *windowsProcessJob) Terminate() error {
	if j.handle == 0 {
		return nil
	}
	return windows.TerminateJobObject(j.handle, 0)
}

func (j *windowsProcessJob) Kill() error { return j.Terminate() }

func (j *windowsProcessJob) Close() error {
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}

func (j *windowsProcessJob) ID() string {
	return "job:" + strconv.FormatUint(uint64(j.handle), 10)
}
