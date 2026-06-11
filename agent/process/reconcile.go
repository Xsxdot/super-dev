// Package process 提供服务进程对账能力。
//
// 职责：
//   - Reconcile：以 OS 进程组存活性为真相，纠正单个 runner 的内存态漂移
//   - ReconcileAll：批量对账所有 runner，返回发生纠正的结果
//
// 边界：
//   - 只读探活，绝不在对账中 Wait；回收由 Runner 唯一 Wait goroutine 完成
//   - 不主动 kill 进程，仅清理 Manager 内存映射
//   - 不持有 pidStore；持久化清理由 agent/api 根据返回结果完成
package process

import "github.com/xsxdot/super-dev/agent/model"

// ReconcileResult 描述一次对账结果，供 API 层清理 pidStore 或测试断言。
type ReconcileResult struct {
	ID          string
	PGID        int
	Corrected   bool
	Status      model.ServiceStatus
	ExitInfo    ExitInfo
	HasExitInfo bool
}

// Reconcile 对单个 id 对账：发现进程组整体已死则清理并翻状态。
//
// 返回：
//   - ReconcileResult: 包含被纠正的 id、PGID、状态和退出证据
//
// 注意：
//   - 运行态真相只看进程组；shell 已退出但 PGID 仍 alive 时，说明后台子进程仍在运行
//   - 对账器只读探活，不 Wait、不 kill，避免和 Runner/Stop 职责交叉
func (m *Manager) Reconcile(id string) ReconcileResult {
	m.mu.Lock()
	r := m.runners[id]
	backgrounded := m.backgrounded[id]
	m.mu.Unlock()
	if r == nil {
		return ReconcileResult{ID: id}
	}

	pgid := r.ProcessGroupID()
	if pgid == 0 {
		return ReconcileResult{ID: id}
	}
	if r.ProcessGroupAlive() {
		m.mu.Lock()
		if m.runners[id] == r && m.status[id] != model.StatusStarting {
			m.status[id] = model.StatusRunning
			if _, hasInfo := r.ExitInfo(); hasInfo {
				m.backgrounded[id] = true
			}
		}
		m.mu.Unlock()
		return ReconcileResult{ID: id, PGID: pgid}
	}

	info, hasInfo := r.ExitInfo()

	m.mu.Lock()
	if m.runners[id] != r {
		m.mu.Unlock()
		return ReconcileResult{ID: id, PGID: pgid}
	}
	status := reconcileDeadStatus(m.status[id], info, hasInfo, backgrounded || m.backgrounded[id])
	delete(m.runners, id)
	delete(m.runtimes, id)
	delete(m.launchdDeps, id)
	delete(m.backgrounded, id)
	m.status[id] = status
	m.mu.Unlock()

	m.emitReconcileCorrection(id, status, info, hasInfo)
	return ReconcileResult{
		ID:          id,
		PGID:        pgid,
		Corrected:   true,
		Status:      status,
		ExitInfo:    info,
		HasExitInfo: hasInfo,
	}
}

// ReconcileAll 对所有已知 runner 逐个对账，返回发生纠正的结果。
func (m *Manager) ReconcileAll() []ReconcileResult {
	m.mu.Lock()
	ids := make([]string, 0, len(m.runners))
	for id := range m.runners {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	results := make([]ReconcileResult, 0, len(ids))
	for _, id := range ids {
		if res := m.Reconcile(id); res.Corrected {
			results = append(results, res)
		}
	}
	return results
}

func reconcileDeadStatus(current model.ServiceStatus, info ExitInfo, hasInfo, backgrounded bool) model.ServiceStatus {
	if backgrounded {
		return model.StatusFailed
	}
	if current == model.StatusStopped {
		return model.StatusStopped
	}
	if hasInfo && info.Reason == ExitReasonExited && info.ExitCode == 0 {
		return model.StatusStopped
	}
	return model.StatusFailed
}

func (m *Manager) emitReconcileCorrection(id string, status model.ServiceStatus, info ExitInfo, hasInfo bool) {
	if status == model.StatusFailed && hasInfo && (info.Signaled || info.ExitCode != 0) {
		m.emitExitFailure(id, info)
		return
	}
	if status == model.StatusFailed {
		m.emitLog(id, "ERROR", "stderr", "对账检测到进程组已退出，状态已纠正为 failed")
		return
	}
	m.emitLog(id, "INFO", "stdout", "对账检测到进程组已退出，状态已纠正为 stopped")
}
