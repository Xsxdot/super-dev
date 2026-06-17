//go:build windows

// node_attach_windows.go 实现 Windows Node attach：启动时 prearm --inspect，不发 SIGUSR1。
//
// 职责：
//   - 从 argv 或 stderr 解析已预埋的 Node inspector 端口
//   - 填充 listen attach readiness，让 js-debug 连接 inspector
//
// 边界：
//   - 不模拟 POSIX SIGUSR1
//   - 不负责启动时注入 --inspect；启动计划由 langruntime Node provider 生成
package codedebug

import (
	"fmt"
	"log"

	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

// fillNodeAttach 为 Windows 填充 Node attach 请求：从 prearm inspector 端口直连。
func (m *Manager) fillNodeAttach(req *readinessRequest, dep model.Deployment, pid int) error {
	log.Printf("[codedebug] node attach via prearm deployment=%s pid=%d", dep.ID, pid)
	req.pid = pid
	port, inspectPresent := m.inspectPortFromArgv(dep.ID)
	if port <= 0 {
		var err error
		port, err = m.waitInspectorPort(dep.ID, nodeInspectorFallback{})
		if err != nil {
			log.Printf("[codedebug] resolve prearmed node inspector failed deployment=%s pid=%d inspectPresent=%t: %v", dep.ID, pid, inspectPresent, err)
			if inspectPresent {
				return fmt.Errorf("%w: node inspector port not ready (windows prearm)", err)
			}
			return fmt.Errorf("%w: node started without --inspect (windows requires prearm)", ErrAttachUnsupported)
		}
	}
	req.readiness = langruntime.ReadinessPrearmListen
	req.port = port
	log.Printf("[codedebug] node prearm inspector ready deployment=%s pid=%d port=%d", dep.ID, pid, port)
	return nil
}
