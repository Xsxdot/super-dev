// Package api 的 Host 级 Agent 生命周期互斥门。
//
// 职责：
//   - 阻止同一 Host 的 Agent 生命周期写操作并发执行
//   - 允许不同 Host 的操作并行
//
// 边界：
//   - 仅维护进程内短期占用，不排队、不持久化
//   - 不决定具体生命周期操作的业务顺序
package api

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/xsxdot/gokit/logger"
)

type hostOperationGate struct {
	mu     sync.Mutex
	active map[string]*hostOperationLease
}

type hostOperationLease struct {
	gate      *hostOperationGate
	hostID    string
	operation string
	once      sync.Once
}

type hostOperationConflict struct {
	HostID          string
	Operation       string
	ActiveOperation string
}

// Error 返回发生冲突的 Host 与当前占用操作。
func (e *hostOperationConflict) Error() string {
	return fmt.Sprintf("host %s already has agent lifecycle operation %s", e.HostID, e.ActiveOperation)
}

func newHostOperationGate() *hostOperationGate {
	return &hostOperationGate{active: make(map[string]*hostOperationLease)}
}

func (g *hostOperationGate) tryAcquire(hostID, operation string) (*hostOperationLease, string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if current := g.active[hostID]; current != nil {
		return nil, current.operation, false
	}
	lease := &hostOperationLease{gate: g, hostID: hostID, operation: operation}
	g.active[hostID] = lease
	return lease, "", true
}

func (l *hostOperationLease) release() bool {
	released := false
	l.once.Do(func() {
		l.gate.mu.Lock()
		defer l.gate.mu.Unlock()
		// 只释放自己持有的租约，避免未来重构中的延迟释放误删后继操作。
		if l.gate.active[l.hostID] == l {
			delete(l.gate.active, l.hostID)
			released = true
		}
	})
	return released
}

func (a *App) beginAgentLifecycleOperation(hostID, operation string) (func(), *hostOperationConflict) {
	log := logger.GetLogger().WithEntryName("AgentLifecycle")
	lease, activeOperation, ok := a.agentLifecycleGate.tryAcquire(hostID, operation)
	fields := map[string]any{"host_id": hostID, "operation": operation}
	if !ok {
		fields["active_operation"] = activeOperation
		log.WithFields(fields).Info("同一 Host 已有 Agent 生命周期操作，拒绝并发请求")
		return nil, &hostOperationConflict{HostID: hostID, Operation: operation, ActiveOperation: activeOperation}
	}
	log.WithFields(fields).Info("已获取 Host Agent 生命周期操作互斥门")
	return func() {
		if lease.release() {
			log.WithFields(fields).Info("已释放 Host Agent 生命周期操作互斥门")
		}
	}, nil
}

func (a *App) acquireAgentLifecycleOperation(w http.ResponseWriter, hostID, operation string) (func(), bool) {
	release, conflict := a.beginAgentLifecycleOperation(hostID, operation)
	if conflict == nil {
		return release, true
	}
	jsonErrorCode(w, http.StatusConflict, "operation_in_progress", "another agent lifecycle operation is in progress", map[string]string{
		"host_id":          conflict.HostID,
		"operation":        conflict.Operation,
		"active_operation": conflict.ActiveOperation,
	})
	return nil, false
}
