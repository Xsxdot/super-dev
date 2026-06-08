// writer.go 定义日志写入抽象接口。
//
// 职责：
//   - 定义 LogWriter 接口（AppendBatch）
//
// 边界：
//   - 只定义接口，不含实现
//   - 与偏读语义的 LogReader 对称：读可一对多，写一对一（一个 agent 一个写目标）
package logbackend

import (
	"context"

	"github.com/xsxdot/super-dev/agent/model"
)

// LogWriter 抽象「写入日志」，一对一（一个 agent 一个写目标）。
//
// 实现方失败时不应假设 batch 已落盘：
//   - sqlite 实现：失败即记日志（磁盘满/损坏，重试无意义）。
//   - 中心化实现（PG/云，本阶段不实现）：应保留 pending、退避重试，不静默丢。
type LogWriter interface {
	// AppendBatch 批量写入日志条目。ctx 用于超时/取消（中心化写入是网络调用）。
	AppendBatch(ctx context.Context, entries []model.LogEntry) error
}
