//go:build windows

// killstale_windows.go 用 taskkill /T 按进程树清理重启前遗留进程。
//
// 职责：
//   - 在 Job Object 句柄已随上一轮 agent 退出而失效后，按 pid 递归清理子树
//   - 为 pid store 的历史记录提供 Windows stale cleanup
//
// 边界：
//   - 不管理当前运行中 Runner 的 Job Object，实时停止仍由 groupRef.kill 负责
//   - 不读取或写入 pid store 文件
package process

import (
	"log"
	"os/exec"
	"strconv"
)

// killStaleGroup 终止 pid 及其子进程树。
func killStaleGroup(id int) {
	if id <= 0 {
		return
	}
	out, err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(id)).CombinedOutput()
	if err != nil {
		// Windows 重启后只能按 pid 清理；进程已退出是常见状态，日志保留上下文用于排查。
		log.Printf("[process] taskkill stale pid=%d failed: %v (%s)", id, err, out)
		return
	}
	log.Printf("[process] taskkill stale pid=%d succeeded", id)
}
