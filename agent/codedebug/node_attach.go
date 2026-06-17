// node_attach.go 提供 Node attach 的跨平台公共解析工具。
//
// 职责：
//   - 从运行进程 argv 中解析 --inspect / --inspect-brk 端口
//   - 为 Unix SIGUSR1 与 Windows prearm 两条 attach 路径共享端口解析
//
// 边界：
//   - 不发送 signal，不启动 adapter；平台动作在 node_attach_*.go 中
//   - 不解析 stderr，stderr inspector URL 仍由 inspector.go 负责
package codedebug

import (
	"strconv"
	"strings"
)

// inspectPortFromArgv 从 deployment 运行进程 argv 中解析 Node inspector 端口。
func (m *Manager) inspectPortFromArgv(deploymentID string) (port int, present bool) {
	if m.runningProcessArgv == nil {
		return 0, false
	}
	return parseInspectPort(m.runningProcessArgv(deploymentID))
}

// parseInspectPort 在 argv 中找 --inspect / --inspect-brk 并返回端口与是否出现过该 flag。
func parseInspectPort(argv []string) (port int, present bool) {
	for i, arg := range argv {
		value := ""
		switch {
		case arg == "--inspect" || arg == "--inspect-brk":
			present = true
			if i+1 < len(argv) && inspectValueLooksLikePort(argv[i+1]) {
				value = argv[i+1]
			} else {
				return defaultNodeInspectorPort, true
			}
		case strings.HasPrefix(arg, "--inspect="):
			present = true
			value = strings.TrimPrefix(arg, "--inspect=")
		case strings.HasPrefix(arg, "--inspect-brk="):
			present = true
			value = strings.TrimPrefix(arg, "--inspect-brk=")
		default:
			continue
		}
		if idx := strings.LastIndex(value, ":"); idx >= 0 {
			value = value[idx+1:]
		}
		if port, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && port >= 0 {
			return port, true
		}
		return 0, true
	}
	return 0, present
}

func inspectValueLooksLikePort(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if idx := strings.LastIndex(value, ":"); idx >= 0 {
		value = value[idx+1:]
	}
	_, err := strconv.Atoi(value)
	return err == nil
}
