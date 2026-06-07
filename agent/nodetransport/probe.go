// probe.go 定义传输链探活与选路状态协议。
//
// 职责：
//   - 定义 probe 的稳定状态枚举
//   - 定义 NodeStatus 暴露给前端的当前选路快照
//   - 定义远端 agent 安全 health 响应解析结构
//
// 边界：
//   - 不直接发起网络请求
//   - 不持久化 probe 结果
//   - 不决定前端展示文案
package nodetransport

import (
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// SecurityHealthPath 返回 agent 版本和自举态；待自举态也会放行。
	SecurityHealthPath = "/api/security/health"
	// SecurityAuthCheckPath 是受 token middleware 保护的轻量业务端点，用于区分 token 401。
	SecurityAuthCheckPath = "/api/exec/health"
)

// ProbeStatus 表示一次短探活的分类结果。
type ProbeStatus string

const (
	ProbeStatusReachable        ProbeStatus = "reachable"
	ProbeStatusUnreachable      ProbeStatus = "unreachable"
	ProbeStatusVersionMismatch  ProbeStatus = "version-mismatch"
	ProbeStatusAuthFailed       ProbeStatus = "auth-failed"
	ProbeStatusPendingBootstrap ProbeStatus = "pending-bootstrap"
)

// ProbeResult 是链上单项 transport 最近一次探活结果。
type ProbeResult struct {
	Index         int                 `json:"index"`
	TransportType model.TransportType `json:"transport_type"`
	Status        ProbeStatus         `json:"status"`
	Reachable     bool                `json:"reachable"`
	Version       string              `json:"version,omitempty"`
	Error         string              `json:"error,omitempty"`
	LatencyMS     int64               `json:"latency_ms,omitempty"`
	CheckedAt     time.Time           `json:"checked_at"`
}

// RouteStatus 描述 Dispatcher 当前为某 host 选中的链路。
type RouteStatus struct {
	SelectedIndex int                 `json:"selected_index"`
	SelectedType  model.TransportType `json:"selected_type,omitempty"`
	Degraded      bool                `json:"degraded"`
	LastResults   []ProbeResult       `json:"last_results,omitempty"`
}

// SecurityHealthResponse 是远端 agent 安全 health 端点的响应。
type SecurityHealthResponse struct {
	Version        string `json:"version"`
	ProvisionState string `json:"provision_state"`
}
