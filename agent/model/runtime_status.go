// Package model 定义项目概览运行态接口的共享数据模型。
//
// 职责：
//   - 表达服务实例进程级指标和健康状态
//   - 表达按环境分段的 runtime-status 响应
//
// 边界：
//   - 不采集指标，不访问进程或远端节点
//   - 不保存历史快照
package model

// Health 表示项目概览中单个服务实例的健康状态。
type Health string

const (
	// HealthRunning 表示实例进程正在运行，但没有额外健康探针结论。
	HealthRunning Health = "running"
	// HealthHealthy 表示实例运行且健康探针通过。
	HealthHealthy Health = "healthy"
	// HealthRestarting 表示实例由运行基座接管并处于重启过程中。
	HealthRestarting Health = "restarting"
	// HealthStopped 表示实例已停止。
	HealthStopped Health = "stopped"
	// HealthFailed 表示实例运行基座报告失败。
	HealthFailed Health = "failed"
	// HealthUnknown 表示实例状态无法确认。
	HealthUnknown Health = "unknown"
)

// DebuggerState 表示实例上调试器的附着/暂停状态（与 Health 正交）。
type DebuggerState string

const (
	// DebuggerStateNone 表示没有调试器附着。
	DebuggerStateNone DebuggerState = "none"
	// DebuggerStateAttached 表示调试器已附着但未暂停。
	DebuggerStateAttached DebuggerState = "attached"
	// DebuggerStatePaused 表示调试器已在某源码位置暂停。
	DebuggerStatePaused DebuggerState = "paused"
)

// DebuggerOrigin 表示调试器如何接入：launch 启动 vs attach 附加。
//
// origin 决定停调试的语义：launched 需 stop/restart，attached 只 detach、进程照跑。
type DebuggerOrigin string

const (
	DebuggerOriginLaunched DebuggerOrigin = "launched"
	DebuggerOriginAttached DebuggerOrigin = "attached"
)

// PausedLocation 描述调试器暂停的源码位置。
type PausedLocation struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
}

// DebuggerStatus 描述实例上调试器的附着与暂停状态。
type DebuggerStatus struct {
	State       DebuggerState   `json:"state"`
	Language    ServiceLanguage `json:"language,omitempty"`
	Origin      DebuggerOrigin  `json:"origin,omitempty"`
	LeaseActive bool            `json:"lease_active,omitempty"`
	PausedAt    *PausedLocation `json:"paused_at,omitempty"`
}

// InstanceMetrics 表示单个服务实例的进程级运行指标。
//
// 数值字段使用指针是为了让未知值序列化为 JSON null，避免把未知误判为真实的 0。
type InstanceMetrics struct {
	CPUPercent *float64 `json:"cpu_percent"`
	MemBytes   *int64   `json:"mem_bytes"`
	UptimeSec  *int64   `json:"uptime_sec"`
	Restarts   *int     `json:"restarts"`
	Health     Health   `json:"health"`
	Base       string   `json:"base"`
}

// RuntimeStatusResponse 表示项目级运行状态快照。
type RuntimeStatusResponse struct {
	Environments []EnvStatus `json:"environments"`
}

// EnvStatus 表示一个环境下的服务实例运行状态集合。
type EnvStatus struct {
	EnvName   string           `json:"env_name"`
	Instances []InstanceStatus `json:"instances"`
}

// InstanceStatus 表示一个 deployment 在一个节点上的运行状态。
type InstanceStatus struct {
	ServiceID    string          `json:"service_id"`
	ServiceName  string          `json:"service_name"`
	DeploymentID string          `json:"deployment_id"`
	NodeID       string          `json:"node_id"`
	NodeName     string          `json:"node_name"`
	IsLocal      bool            `json:"is_local"`
	Error        string          `json:"error,omitempty"`
	Metrics      InstanceMetrics `json:"metrics"`
	// Debugger 描述该实例上调试器的状态，nil 表示无调试器。与 Metrics.Health 正交。
	Debugger *DebuggerStatus `json:"debugger,omitempty"`
}
