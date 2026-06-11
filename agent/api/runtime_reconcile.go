// Package api 封装服务进程对账的 API 层编排。
//
// 职责：
//   - 调用 process.Manager 的 Reconcile/ReconcileAll
//   - 根据对账结果清理 pidStore
//   - 提供控制指令前同步对账和后台周期对账
//
// 边界：
//   - 不直接探测 OS 进程，探活由 process.Manager 完成
//   - 不修改项目配置，仅维护运行时 pidStore
package api

import (
	"context"
	"log"
	"time"

	"github.com/xsxdot/super-dev/agent/process"
)

func (a *App) reconcileLocalDeployment(projectID, deploymentID string) process.ReconcileResult {
	mgr := a.getOrCreateManager(projectID)
	res := mgr.Reconcile(deploymentID)
	a.applyProcessReconcileResults([]process.ReconcileResult{res})
	return res
}

func (a *App) reconcileAllLocalDeployments() {
	a.mu.RLock()
	mgrs := make([]*process.Manager, 0, len(a.managers))
	for _, mgr := range a.managers {
		mgrs = append(mgrs, mgr)
	}
	a.mu.RUnlock()

	for _, mgr := range mgrs {
		a.applyProcessReconcileResults(mgr.ReconcileAll())
	}
}

func (a *App) applyProcessReconcileResults(results []process.ReconcileResult) {
	changed := false
	for _, res := range results {
		if !res.Corrected {
			continue
		}
		a.pidStore.Remove(res.ID)
		changed = true
	}
	if !changed {
		return
	}
	if err := a.pidStore.Flush(); err != nil {
		log.Printf("[SuperDev] flush pid store after process reconcile failed: %v", err)
	}
}

// startProcessReconcileLoop 每 3s 对所有项目 manager 做一次进程对账。
//
// 注意：
//   - loop 只纠正 SuperDev 之外的进程退出/被 kill 导致的内存态漂移
//   - App.Close 会取消该 loop，避免测试和桌面端退出后残留 goroutine
func (a *App) startProcessReconcileLoop() {
	if a.processReconcileCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.processReconcileCancel = cancel
	ticker := time.NewTicker(3 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.reconcileAllLocalDeployments()
			}
		}
	}()
}
