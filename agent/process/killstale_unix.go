//go:build !windows

// killstale_unix.go 负责清理 superdev 重启前遗留的 Unix 进程组。
//
// 职责：
//   - 按进程组 PGID 终止遗留进程树
//   - 兼容历史 pid store 中记录普通 PID 的旧数据
//
// 边界：
//   - 不读取或写入 pid store 文件
//   - 不判断 deployment 状态，仅执行本地 stale process cleanup
package process

import (
	"log"
	"syscall"
)

// killStaleGroup 终止重启前遗留的进程组或普通进程。
func killStaleGroup(id int) {
	if id <= 0 {
		return
	}
	groupErr := syscall.Kill(-id, syscall.SIGKILL)
	// 兼容旧数据：历史文件里可能存的是普通 PID，而不是 Setpgid 后的 PGID。
	pidErr := syscall.Kill(id, syscall.SIGKILL)
	if groupErr != nil && groupErr != syscall.ESRCH {
		log.Printf("[process] kill stale group failed id=%d: %v", id, groupErr)
	}
	if pidErr != nil && pidErr != syscall.ESRCH {
		log.Printf("[process] kill stale pid failed id=%d: %v", id, pidErr)
	}
}
