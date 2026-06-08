// Package logbackend 定义日志后端抽象接口及公共数据类型。
//
// 职责：
//   - 定义 LogReader/LogBackend 接口（Query / Search / Subscribe）
//   - 定义 QueryFilter、SearchQuery、Cursor、SubscribeOptions、LogStream 公共类型
//
// 边界：
//   - 不包含任何实现，只有接口和类型定义
//   - 不依赖具体存储（store）或网络（tunnel）包
package logbackend

import (
	"context"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// QueryFilter 定义历史日志拉取的过滤和分页参数。
type QueryFilter struct {
	// DeploymentID 按部署过滤；空字符串表示不过滤。
	DeploymentID string
	// RunID 按运行会话过滤；空字符串表示不过滤。
	RunID string
	// Limit 返回条数上限；0 时实现方使用自身默认值。
	Limit int
	// Before 游标分页：只返回早于此游标的记录；零值表示从最新记录开始。
	Before Cursor
}

// SearchQuery 定义关键字搜索参数。
type SearchQuery struct {
	// Text 搜索关键字，大小写不敏感包含匹配。
	Text string
	// DeploymentIDs 限定搜索范围；nil 或空时实现方可拒绝（避免无边界全库扫描）。
	DeploymentIDs []string
	// Limit 返回条数上限；0 时实现方使用自身默认值。
	Limit int
	// Cursor 分页游标；零值表示从最新开始。
	Cursor Cursor
	// From / To 时间范围过滤；零值表示不限制。
	From time.Time
	To   time.Time
}

// Cursor 表示分页游标，由 (Time, ID) 确定唯一位置。
// Time 是一等公民（Federated 归并排序依赖它）；
// ID 不透明，由各后端自行编码（sqlite 用 rowid 十进制串，PG 用 bigserial，云用自家 token）。
// 上层只透传、用 == 比较，不解释 ID 内容。
// 零值（Time.IsZero() && ID == ""）表示无游标，从最新记录开始。
type Cursor struct {
	Time time.Time
	ID   string
}

// SubscribeOptions 定义实时订阅参数。
type SubscribeOptions struct {
	// DeploymentID 按部署过滤；空字符串表示不过滤。
	DeploymentID string
	// ReplayLast 是回溯窗口：先推最近 N 条历史再无缝转实时；0 表示纯增量。
	ReplayLast int
	// Since 是重连去重锚点。只推 > Since 的条目；零值表示不限。
	Since Cursor
}

// LogStream 是 Subscribe 返回的实时日志流。
//
// Ch 接收新日志；Cancel 通知后端停止推送并关闭 Ch。
// 调用方必须在不再需要流时调用 Cancel，否则后端 goroutine 泄漏。
type LogStream struct {
	Ch     <-chan model.LogEntry
	Cancel func()
}

// LogReader 抽象「读取一个 Deployment 的日志」。
//
// handler 只调此接口，不关心日志实际存在本地 SQLite、
// 远程 agent，还是分布在多个节点。
type LogReader interface {
	// Query 按 ID 游标拉取历史日志，结果按 timestamp ASC, id ASC 排序。
	Query(ctx context.Context, f QueryFilter) (entries []model.LogEntry, next Cursor, err error)

	// Search 按关键字搜索历史日志，结果按 timestamp ASC, id ASC 排序。
	Search(ctx context.Context, q SearchQuery) (entries []model.LogEntry, next Cursor, hasMore bool, err error)

	// Subscribe 订阅实时日志流。调用方通过 LogStream.Cancel 取消订阅。
	// 实现方在 Cancel 调用后应关闭 LogStream.Ch。
	// ctx 取消和 Cancel 调用均可停止流；实现方应同时响应两者。
	Subscribe(ctx context.Context, opts SubscribeOptions) LogStream
}

// LogBackend 抽象「一个 Deployment 的所有日志能力」。
type LogBackend interface {
	LogReader
}
