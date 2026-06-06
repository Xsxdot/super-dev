// Package nodetransport 抽象本机 agent 与远端节点的通信方式。
//
// 职责：
//   - 提供按 hostID 寻址的请求和流式通信接口
//   - 隐藏隧道、直连、MQ、桥接等传输差异
//   - 为 NodeRegistry 的多节点状态订阅预留统一入口
//
// 边界：
//   - 不决定远端 agent 安装方式
//   - 不持久化节点状态
//   - 不改变日志 tab 级按需连接策略
package nodetransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// ErrHostUnreachable 表示传输层无法到达指定 host。
var ErrHostUnreachable = errors.New("host unreachable")

// NodeRequest 描述一次发往远端节点的 HTTP 或 WebSocket 请求。
type NodeRequest struct {
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Body    io.Reader
}

// NodeResponse 是 NodeTransport.Do 返回的响应。
type NodeResponse struct {
	StatusCode int
	Headers    http.Header
	Body       io.ReadCloser
}

// NodeStream 是远端节点的双向 JSON 流。
type NodeStream interface {
	ReadJSON(v any) error
	WriteJSON(v any) error
	Close() error
}

// NodeStatus 是 NodeRegistry 状态线的传输无关协议体。
type NodeStatus struct {
	HostID      string                         `json:"host_id"`
	Name        string                         `json:"name,omitempty"`
	Reachable   bool                           `json:"reachable"`
	Agent       model.AgentRuntime             `json:"agent"`
	Deployments []model.InstanceStatus         `json:"deployments"`
	Managed     *model.ManagedDeploymentStatus `json:"managed,omitempty"`
	UpdatedAt   time.Time                      `json:"updated_at"`
	Error       string                         `json:"error,omitempty"`
}

// MarshalJSON 输出 NodeStatus 的稳定桌面端协议形状。
//
// 参数：
//   - 无，序列化当前 NodeStatus 值
//
// 返回：
//   - JSON 字节
//   - 序列化错误
//
// 注意：
//   - deployments 即使为空也必须是 []，不能是 null；前端按数组协议渲染节点状态线。
func (s NodeStatus) MarshalJSON() ([]byte, error) {
	deployments := s.Deployments
	if deployments == nil {
		deployments = []model.InstanceStatus{}
	}
	type nodeStatusJSON struct {
		HostID      string                         `json:"host_id"`
		Name        string                         `json:"name,omitempty"`
		Reachable   bool                           `json:"reachable"`
		Agent       model.AgentRuntime             `json:"agent"`
		Deployments []model.InstanceStatus         `json:"deployments"`
		Managed     *model.ManagedDeploymentStatus `json:"managed,omitempty"`
		UpdatedAt   time.Time                      `json:"updated_at"`
		Error       string                         `json:"error,omitempty"`
	}
	return json.Marshal(nodeStatusJSON{
		HostID:      s.HostID,
		Name:        s.Name,
		Reachable:   s.Reachable,
		Agent:       s.Agent,
		Deployments: deployments,
		Managed:     s.Managed,
		UpdatedAt:   s.UpdatedAt,
		Error:       s.Error,
	})
}

// NodeTransport 抽象“与某个节点通信”。
type NodeTransport interface {
	Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error)
	Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error)
	SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func())
	Covers() []string
}
