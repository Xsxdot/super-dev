//go:build !windows

// node_attach_unix.go 实现 Unix Node attach：SIGUSR1 唤醒 inspector 后连接端口。
//
// 职责：
//   - 给真实 Node debuggee 发送 SIGUSR1
//   - 从 stderr/fallback 解析 inspector 端口并填充 readiness 请求
//
// 边界：
//   - 不解析 Windows prearm --inspect，Windows 逻辑见 node_attach_windows.go
//   - 不构造 DAP attach 参数，后续由 provider.AttachArguments 完成
package codedebug

import (
	"fmt"
	"log"

	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

// fillNodeAttach 为 Unix 填充 Node attach 请求，使用 SIGUSR1 惰性打开 inspector。
func (m *Manager) fillNodeAttach(req *readinessRequest, dep model.Deployment, pid int) error {
	log.Printf("[codedebug] node attach via signal deployment=%s pid=%d", dep.ID, pid)
	req.pid = pid
	defaultInspectorAlreadyOpen := tcpPortOpen(defaultNodeInspectorPort)
	if err := m.signalProcess(pid, "SIGUSR1"); err != nil {
		log.Printf("[codedebug] signal node debuggee failed deployment=%s pid=%d: %v", dep.ID, pid, err)
		return fmt.Errorf("signal node debuggee: %w", err)
	}
	inspectorPort, err := m.waitInspectorPort(dep.ID, nodeInspectorFallback{
		port:    defaultNodeInspectorPort,
		enabled: !defaultInspectorAlreadyOpen,
	})
	if err != nil {
		log.Printf("[codedebug] wait node inspector failed deployment=%s pid=%d: %v", dep.ID, pid, err)
		return err
	}
	req.readiness = langruntime.ReadinessPrearmListen
	req.port = inspectorPort
	log.Printf("[codedebug] node attach inspector ready deployment=%s pid=%d port=%d", dep.ID, pid, inspectorPort)
	return nil
}
