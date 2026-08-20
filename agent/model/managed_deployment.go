// Package model 定义远端 agent 自治所需的声明式下发模型。
//
// 职责：
//   - 表达桌面端投影给某个远端 host 的 deployment 清单
//   - 表达远端 agent 应用清单后的 collector reconcile 结果
//
// 边界：
//   - 不执行持久化、采集或运行态采样
//   - 不包含桌面端 host 凭据或隧道状态
package model

// ManagedDeployment 是桌面端下发给远端 agent 的单个 deployment。
//
// 参数语义：
//   - DeploymentID: 原始 deployment.ID，runtime-status 按它匹配
//   - ServiceID/ServiceName/ProjectID/EnvName: 远端重建本地 project 视图所需字段
//   - Runtime: 远端本机视角的运行态采样配置；本载荷不承载 Deployment 的
//     Language 或 StartCommand 启动信息，因此下发式 deployment 不能由目标机 agent 拉起
//   - Logs: 远端本机视角的日志采集配置
//   - Location: 必须是 LocationLocal，避免远端 runtime-status 二次转发
//
// 注意：
//   - 桌面端投影 remote deployment 时必须把 Location 改写为 LocationLocal。
//   - 该结构不携带 HostIDs；它已经是单 host 的远端视角清单。
type ManagedDeployment struct {
	DeploymentID string         `json:"deployment_id"`
	ServiceID    string         `json:"service_id"`
	ServiceName  string         `json:"service_name"`
	ProjectID    string         `json:"project_id"`
	EnvName      string         `json:"env_name"`
	Runtime      *RuntimeConfig `json:"runtime,omitempty"`
	Logs         *LogConfig     `json:"logs,omitempty"`
	Location     DeployLocation `json:"location"`
	// Ports 是该 deployment 声明的监听端口，随下发清单传到目标机。
	//
	// 为什么必须在这个载荷里：下发式 deployment 在目标机上没有 project.yaml
	// （目标机的项目是由本清单合成的内存对象），端口声明没有第二条路可走。
	// 缺了它，目标机上报的节点帧端口恒空，本机端口镜像永远建立不起来。
	Ports []int `json:"ports,omitempty"`
}

// ManagedCollectorFailure 表示某个期望 collector 未能启动。
type ManagedCollectorFailure struct {
	CollectorID string        `json:"collector_id,omitempty"`
	Name        string        `json:"name"`
	Type        LogSourceType `json:"type"`
	Error       string        `json:"error"`
}

// ManagedDeploymentReconcileResult 表示远端应用声明式清单后的状态。
//
// 注意：
//   - Persisted=false 且 Error 非空时，说明内存已尝试应用但落盘失败。
//   - FailedCollectors 只描述失败项，其它 collector 仍可正常运行。
type ManagedDeploymentReconcileResult struct {
	DeploymentCount   int                       `json:"deployment_count"`
	CollectorCount    int                       `json:"collector_count"`
	StartedCollectors []Collector               `json:"started_collectors"`
	StoppedCollectors []string                  `json:"stopped_collectors"`
	FailedCollectors  []ManagedCollectorFailure `json:"failed_collectors"`
	Persisted         bool                      `json:"persisted"`
	Error             string                    `json:"error,omitempty"`
}

// ManagedCollectorStatus 描述远端某个 managed deployment 对应 collector 的当前状态。
type ManagedCollectorStatus struct {
	DeploymentID string        `json:"deployment_id"`
	ServiceName  string        `json:"service_name,omitempty"`
	EnvName      string        `json:"env_name,omitempty"`
	Name         string        `json:"name,omitempty"`
	Type         LogSourceType `json:"type,omitempty"`
	CollectorID  string        `json:"collector_id,omitempty"`
	Desired      bool          `json:"desired"`
	Running      bool          `json:"running"`
	Status       ServiceStatus `json:"status,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// ManagedDeploymentStatus 是远端 agent 当前 managed 编排视图的只读诊断状态。
//
// 注意：
//   - CollectorCount 保留现有合同，表示期望 collector 数
//   - ActiveCollectorCount 来自 collector.Manager.List 的实际 StatusRunning 数
type ManagedDeploymentStatus struct {
	DeploymentCount      int                              `json:"deployment_count"`
	CollectorCount       int                              `json:"collector_count"`
	ActiveCollectorCount int                              `json:"active_collector_count"`
	LastResult           ManagedDeploymentReconcileResult `json:"last_result"`
	Collectors           []ManagedCollectorStatus         `json:"collectors"`
}

// HostManagedDeploymentStatus 是桌面端按 host 聚合后的远端编排诊断状态。
type HostManagedDeploymentStatus struct {
	HostID                 string                   `json:"host_id"`
	HostName               string                   `json:"host_name,omitempty"`
	DesiredDeploymentCount int                      `json:"desired_deployment_count"`
	DesiredCollectorCount  int                      `json:"desired_collector_count"`
	ActiveCollectorCount   int                      `json:"active_collector_count"`
	TunnelConnected        bool                     `json:"tunnel_connected"`
	Remote                 *ManagedDeploymentStatus `json:"remote,omitempty"`
	Error                  string                   `json:"error,omitempty"`
}
