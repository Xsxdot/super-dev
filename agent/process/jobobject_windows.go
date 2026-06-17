//go:build windows

// jobobject_windows.go 用 Windows Job Object 对等 Unix 进程组：
// 把主进程及其全部子进程纳入一个 Job，TerminateJobObject 一次性杀掉整组，
// 不留孤儿，对应 Unix 的 Setpgid + Kill(-pgid)。
//
// 职责：
//   - 创建并配置 Windows Job Object
//   - 将已启动进程分配到 Job Object
//   - 查询和终止 Job Object 内的进程树
//
// 边界：
//   - 不负责进程启动，Runner 在 Start 成功后再调用 assign
//   - 不感知服务、deployment 或配置，仅操作 pid 与 Job 句柄
package process

import (
	"fmt"
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject 封装一个 Windows Job Object 句柄。
type jobObject struct {
	handle windows.Handle
}

// jobObjectBasicAccountingInformation 是 x/sys/windows 未导出的 Windows API 结构体最小镜像。
// 这里只读取 ActiveProcesses，用于判断 Job 内是否仍有存活进程。
type jobObjectBasicAccountingInformation struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

// newJobObject 创建配置了 KILL_ON_JOB_CLOSE 的 Job Object。
//
// 返回：
//   - 可用的 jobObject；失败时返回带 Windows API 上下文的错误
//
// 注意：JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 是防孤儿兜底；显式 terminate
// 仍用于主动停止当前进程树。
func newJobObject() (*jobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Printf("[process] create job object failed: %v", err)
		return nil, fmt.Errorf("create job object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			// 关闭句柄时杀掉组内残留进程，避免 agent 崩溃后留下孤儿进程树。
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(h)
		log.Printf("[process] configure job object failed handle=%v: %v", h, err)
		return nil, fmt.Errorf("set job object info: %w", err)
	}

	log.Printf("[process] job object created handle=%v", h)
	return &jobObject{handle: h}, nil
}

// assign 把指定 pid 的进程加入本 Job Object。
//
// 参数：
//   - pid: 已启动进程的 PID
//
// 注意：进程被分配后再创建的子进程会自动归入同一 Job Object。
func (j *jobObject) assign(pid int) error {
	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		log.Printf("[process] open process for job assign failed pid=%d: %v", pid, err)
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(ph)

	if err := windows.AssignProcessToJobObject(j.handle, ph); err != nil {
		log.Printf("[process] assign process to job failed pid=%d handle=%v: %v", pid, j.handle, err)
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	log.Printf("[process] assigned pid=%d to job handle=%v", pid, j.handle)
	return nil
}

// terminate 终止 Job Object 内的所有进程。
func (j *jobObject) terminate() error {
	if err := windows.TerminateJobObject(j.handle, 1); err != nil {
		log.Printf("[process] terminate job failed handle=%v: %v", j.handle, err)
		return fmt.Errorf("terminate job: %w", err)
	}
	log.Printf("[process] job terminated handle=%v", j.handle)
	return nil
}

// isAlive 通过查询 Job Object 内活动进程数判断进程树是否仍存活。
func (j *jobObject) isAlive() bool {
	var info jobObjectBasicAccountingInformation
	var ret uint32
	err := windows.QueryInformationJobObject(
		j.handle,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		&ret,
	)
	if err != nil {
		log.Printf("[process] query job alive failed handle=%v: %v", j.handle, err)
		return false
	}
	return info.activeProcesses > 0
}

// Close 关闭 Job Object 句柄。
//
// 注意：因为 newJobObject 配置了 KILL_ON_JOB_CLOSE，Close 会兜底终止残留进程。
func (j *jobObject) Close() error {
	if err := windows.CloseHandle(j.handle); err != nil {
		log.Printf("[process] close job object failed handle=%v: %v", j.handle, err)
		return fmt.Errorf("close job object: %w", err)
	}
	log.Printf("[process] job object closed handle=%v", j.handle)
	return nil
}
