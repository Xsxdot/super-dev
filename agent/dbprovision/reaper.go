// reaper.go —— 临时资源 TTL 巡检与启动对账后台任务。
//
// 职责：定期调用 Manager.Reconcile，回收过期租约和已确认的资源孤儿，并提供可停止的 goroutine 生命周期。
// 边界：不直接访问 PG/Redis、不改变配额或审批策略；所有资源动作委托给 Manager/Provisioner。
package dbprovision

import (
	"context"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const startupReconcileDelay = 10 * time.Second

// Reaper 是可启动和停止的 TTL 巡检器。
type Reaper struct {
	manager  *Manager
	interval time.Duration
	startup  time.Duration
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewReaper 创建巡检器；interval 非正时使用 30 秒。
func NewReaper(manager *Manager, interval time.Duration) *Reaper {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	startup := startupReconcileDelay
	// 短间隔用于测试时缩短启动等待；生产默认间隔仍保留 10 秒装配缓冲。
	if interval < startup {
		startup = interval
	}
	return &Reaper{manager: manager, interval: interval, startup: startup}
}

// Start 启动巡检 goroutine；重复调用不会创建第二个巡检循环。
func (r *Reaper) Start(ctx context.Context) {
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	done := r.done
	interval, startup := r.interval, r.startup
	r.mu.Unlock()
	logger.GetLogger().WithEntryName("DBProvisionReaper").WithFields(map[string]any{
		"interval_seconds": interval.Seconds(), "startup_delay_seconds": startup.Seconds(),
	}).Info("临时资源巡检器启动")
	go func() {
		defer close(done)
		timer := time.NewTimer(startup)
		defer timer.Stop()
		select {
		case <-loopCtx.Done():
			return
		case <-timer.C:
			r.runOnce(loopCtx)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				r.runOnce(loopCtx)
			}
		}
	}()
}

// Stop 停止巡检并等待当前轮次退出；未启动时为空操作。
func (r *Reaper) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	logger.GetLogger().WithEntryName("DBProvisionReaper").Info("临时资源巡检器停止")
}

func (r *Reaper) runOnce(ctx context.Context) {
	report, err := r.manager.Reconcile(ctx)
	if err != nil {
		logger.GetLogger().WithEntryName("DBProvisionReaper").WithErr(err).Error("临时资源巡检失败")
		return
	}
	fields := map[string]any{
		"expired_reclaimed": report.ExpiredReclaimed,
		"orphans_reclaimed": len(report.OrphansReclaimed),
		"errors":            len(report.Errors),
	}
	if report.ExpiredReclaimed > 0 || len(report.OrphansReclaimed) > 0 || len(report.Errors) > 0 {
		logger.GetLogger().WithEntryName("DBProvisionReaper").WithFields(fields).Info("临时资源巡检完成")
	} else {
		logger.GetLogger().WithEntryName("DBProvisionReaper").WithFields(fields).Debug("临时资源巡检无回收动作")
	}
}
