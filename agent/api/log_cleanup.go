// log_cleanup.go 提供 agent 本机日志的后台周期淘汰任务。
//
// 职责：
//   - 周期性执行两类淘汰：时间维度（DeleteOlderThan）与容量维度（DeleteToMaxBytes）
//   - 修复"仅启动时淘汰一次、常驻不重启则失效"的缺口
//
// 边界：
//   - 只调用 store 的淘汰方法，不感知日志内容或折叠
//   - 配置来自 AgentSettings，由调用方注入快照（变更生效在下次重启或重读）
package api

import (
	"context"
	"time"

	"github.com/xsxdot/super-dev/agent/store"
)

// cleanupConfig 是一次淘汰所需的配置快照。
type cleanupConfig struct {
	RetentionDays int
	MaxBytes      int64
}

// logCleaner 周期性执行日志淘汰。
type logCleaner struct {
	store *store.Store
	cfg   cleanupConfig
}

func newLogCleaner(s *store.Store, cfg cleanupConfig) *logCleaner {
	return &logCleaner{store: s, cfg: cfg}
}

// runOnce 执行一次完整淘汰：先按时间删过期，再按容量兜底。
//
// 返回：
//   - 任一步失败时返回第一个错误；两类淘汰互不短路，避免单侧失败挡住另一侧
func (c *logCleaner) runOnce(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var firstErr error
	if err := c.store.DeleteOlderThan(c.cfg.RetentionDays); err != nil {
		firstErr = err
	}
	if _, err := c.store.DeleteToMaxBytes(c.cfg.MaxBytes); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Start 启动后台周期淘汰，直到 ctx 取消。立即跑一次，之后按 interval 周期跑。
//
// 参数：
//   - ctx: 取消信号
//   - interval: 周期间隔
func (c *logCleaner) Start(ctx context.Context, interval time.Duration) {
	_ = c.runOnce(ctx)
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.runOnce(ctx)
		}
	}
}
