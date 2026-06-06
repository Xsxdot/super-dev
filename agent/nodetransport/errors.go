// errors.go 定义节点传输层的结构化错误。
//
// 职责：
//   - 为不同 transport 的失败提供稳定 error code
//   - 保留底层 cause，兼容 errors.Is / errors.As
//   - 给 NodeRegistry 和 UI 提供可分类的故障信息
//
// 边界：
//   - 不决定 HTTP 状态码
//   - 不执行重试或降级
//   - 不格式化前端展示文案
package nodetransport

import (
	"errors"
	"fmt"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	CodeAgentNotConfigured   = "agent_not_configured"
	CodeUnsupportedTransport = "unsupported_transport"
	CodeTransportUnreachable = "transport_unreachable"
	CodeAgentUnreachable     = "agent_unreachable"
	CodeAgentVersionMismatch = "agent_version_mismatch"
	CodeAgentAPIMissing      = "agent_api_missing"
	CodeRequestTimeout       = "request_timeout"
	CodeAuthFailed           = "auth_failed"
)

// NodeError 表示一次按节点通信操作失败。
type NodeError struct {
	Code          string
	HostID        string
	TransportType model.TransportType
	Operation     string
	Message       string
	Cause         error
}

// Error 返回面向日志和 API 的稳定错误摘要。
func (e *NodeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s %s failed for host %s", e.TransportType, e.Operation, e.HostID)
}

// Unwrap 返回底层 cause，供 errors.Is / errors.As 使用。
func (e *NodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ErrorCode 返回结构化错误 code；非 NodeError 返回空字符串。
func ErrorCode(err error) string {
	var nodeErr *NodeError
	if errors.As(err, &nodeErr) {
		return nodeErr.Code
	}
	return ""
}
