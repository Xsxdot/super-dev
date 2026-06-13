// pidresolve.go 解析 Go 服务的真实 debuggee PID。
//
// 职责：
//   - 直接可执行命令：debuggee 即 deployment 主进程
//   - `go run`：真实 debuggee 是编译后在同进程组内 exec 的子进程，需在进程组里定位
//
// 边界：
//   - 不杀进程、不附加，只定位 PID
//   - 进程枚举由调用方注入（listProcessGroup），便于测试与跨平台
package codedebug

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// procInfo 描述进程组内一个进程的最小信息。
type procInfo struct {
	pid  int
	comm string // 可执行名（basename）
}

// goDebuggeeHints 是解析 Go debuggee PID 的输入。
type goDebuggeeHints struct {
	command          string
	mainPID          int
	pgid             int
	listProcessGroup func(pgid int) []procInfo
}

// resolveGoDebuggeePID 返回 Go 服务真实 debuggee 的 PID。
func resolveGoDebuggeePID(h goDebuggeeHints) (int, error) {
	if !isGoRunCommand(h.command) {
		// 直接可执行：主进程即 debuggee
		if h.mainPID <= 0 {
			return 0, ErrAttachTargetUnresolved
		}
		return h.mainPID, nil
	}
	// go run：在进程组里找非 go/sh 的子进程（编译产物）
	if h.listProcessGroup == nil || h.pgid <= 0 {
		return 0, ErrAttachTargetUnresolved
	}
	for _, p := range h.listProcessGroup(h.pgid) {
		comm := strings.ToLower(strings.TrimSpace(p.comm))
		if comm == "go" || comm == "sh" || comm == "bash" || comm == "" {
			continue
		}
		if p.pid > 0 {
			return p.pid, nil
		}
	}
	return 0, fmt.Errorf("%w: go run child not found in process group %d", ErrAttachTargetUnresolved, h.pgid)
}

func isGoRunCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	fields = stripInlineEnvFields(fields)
	if len(fields) < 2 {
		return false
	}
	head := fields[0]
	if idx := strings.LastIndex(head, "/"); idx >= 0 {
		head = head[idx+1:]
	}
	return head == "go" && fields[1] == "run"
}

func stripInlineEnvFields(fields []string) []string {
	for len(fields) > 0 && isInlineEnvField(fields[0]) {
		fields = fields[1:]
	}
	return fields
}

func isInlineEnvField(field string) bool {
	idx := strings.Index(field, "=")
	if idx <= 0 {
		return false
	}
	return isShellVariableName(field[:idx])
}

func isShellVariableName(name string) bool {
	for i, r := range name {
		if r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') {
			continue
		}
		if i > 0 && '0' <= r && r <= '9' {
			continue
		}
		return false
	}
	return name != ""
}

// listProcessGroupOS 用 ps 枚举某进程组内的进程（darwin/linux 通用）。
func listProcessGroupOS(pgid int) []procInfo {
	if pgid <= 0 {
		return nil
	}
	out, err := exec.Command("ps", "-axo", "pid=,pgid=,comm=").Output()
	if err != nil {
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
	return procs
}
