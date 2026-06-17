// pidresolve.go 解析语言服务的真实 debuggee PID。
//
// 职责：
//   - 直接可执行命令：debuggee 即 deployment 主进程
//   - `go run`：真实 debuggee 是编译后在同进程组内 exec 的子进程，需在进程组里定位
//   - `pnpm/npm` 启动 Node：真实 debuggee 是同进程组内的 node 子进程
//
// 边界：
//   - 不杀进程、不附加，只定位 PID
//   - 进程枚举由调用方注入（listProcessGroup），便于测试与跨平台
package codedebug

import (
	"fmt"
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

// nodeDebuggeeHints 描述定位 node debuggee 进程所需信息。
type nodeDebuggeeHints struct {
	mainPID          int
	pgid             int
	mainIsNode       bool
	listProcessGroup func(pgid int) []procInfo
}

// resolveNodeDebuggeePID 定位真正的 node 进程：
// 高层启动时 mainPID 即 node；逃生口（pnpm/npm 包一层）时 node 是进程组里的子进程。
func resolveNodeDebuggeePID(h nodeDebuggeeHints) (int, error) {
	if h.listProcessGroup == nil || h.pgid <= 0 {
		if h.mainPID <= 0 {
			return 0, ErrAttachTargetUnresolved
		}
		return h.mainPID, nil
	}
	for _, p := range h.listProcessGroup(h.pgid) {
		if p.pid == h.mainPID && !h.mainIsNode {
			continue
		}
		if isNodeProcess(p.comm) && p.pid > 0 {
			return p.pid, nil
		}
	}
	if h.mainIsNode && h.mainPID > 0 {
		return h.mainPID, nil
	}
	return 0, fmt.Errorf("%w: node child not found in process group %d", ErrAttachTargetUnresolved, h.pgid)
}

func isNodeProcess(comm string) bool {
	comm = strings.TrimSpace(comm)
	if idx := strings.LastIndex(comm, "/"); idx >= 0 {
		comm = comm[idx+1:]
	}
	comm = strings.ToLower(comm)
	comm = strings.TrimSuffix(comm, ".exe")
	return comm == "node"
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
