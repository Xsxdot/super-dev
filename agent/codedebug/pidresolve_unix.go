//go:build !windows

// pidresolve_unix.go 用 ps 枚举 Unix 进程组内的进程。
//
// 职责：
//   - 通过 pid/pgid/comm 三列定位同一进程组成员
//   - 为 Go/Node attach 解析真实 debuggee PID 提供 OS 数据
//
// 边界：
//   - 不决定 debuggee 选择策略，只返回候选进程列表
//   - 不终止或附加任何进程
package codedebug

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// listProcessGroupOS 用 ps 枚举某进程组内的进程（darwin/linux 通用）。
func listProcessGroupOS(pgid int) []procInfo {
	if pgid <= 0 {
		return nil
	}
	out, err := exec.Command("ps", "-axo", "pid=,pgid=,comm=").Output()
	if err != nil {
		log.Printf("[codedebug] list process group failed pgid=%d: %v", pgid, err)
		return nil
	}
	var procs []procInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		pid, convErr := strconv.Atoi(fields[0])
		if convErr != nil {
			continue
		}
		procPGID, convErr := strconv.Atoi(fields[1])
		if convErr != nil || procPGID != pgid {
			continue
		}
		comm := fields[2]
		if idx := strings.LastIndex(comm, "/"); idx >= 0 {
			comm = comm[idx+1:]
		}
		procs = append(procs, procInfo{pid: pid, comm: comm})
	}
	log.Printf("[codedebug] listed process group pgid=%d count=%d", pgid, len(procs))
	return procs
}
