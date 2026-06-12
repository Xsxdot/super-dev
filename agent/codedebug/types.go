// Package codedebug 提供本机代码调试会话管理能力。
//
// 职责：
//   - 解析可调试 deployment
//   - 管理 DAP adapter 和调试 session
//   - 为 HTTP API 和 MCP 提供稳定 DTO
//
// 边界：
//   - 不修改普通服务启停链路
//   - 不访问远端主机
//   - 不持久化 live debug session
package codedebug

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

var (
	ErrTargetNotFound     = errors.New("debug target not found")
	ErrTargetUnsupported  = errors.New("debug target unsupported")
	ErrConfigInvalid      = errors.New("debug config invalid")
	ErrAdapterUnavailable = errors.New("debug adapter unavailable")
	ErrPathOutsideProject = errors.New("path outside project root")
	ErrSessionNotFound    = errors.New("debug session not found")
	ErrSessionClosed      = errors.New("debug session closed")
	ErrEvaluateDenied     = errors.New("debug evaluate denied")
)

const (
	CodeTargetUnsupported   = "debug_target_unsupported"
	CodeAdapterUnavailable  = "adapter_unavailable"
	CodeAdapterStartFailed  = "adapter_start_failed"
	CodeDAPConnectionFailed = "dap_connection_failed"
)

// AdapterErrorInfo 描述 adapter 启动/连接失败时暴露给调用方的稳定错误信息。
type AdapterErrorInfo struct {
	Code     string                  `json:"code"`
	Provider model.CodeDebugProvider `json:"provider,omitempty"`
	Command  string                  `json:"command,omitempty"`
	Hint     string                  `json:"remediation_hint,omitempty"`
}

// AdapterError 表示 adapter 启动或 DAP 连接阶段的结构化错误。
type AdapterError struct {
	AdapterErrorInfo
	cause error
}

// NewAdapterError 创建一个带稳定 code 和修复提示的 adapter 错误。
func NewAdapterError(code string, cmd AdapterCommand, cause error) error {
	if cause == nil {
		cause = errors.New(code)
	}
	return &AdapterError{
		AdapterErrorInfo: AdapterErrorInfo{
			Code:     strings.TrimSpace(code),
			Provider: cmd.Provider,
			Command:  cmd.Summary(),
			Hint:     adapterRemediationHint(code, cmd.Provider),
		},
		cause: cause,
	}
}

// Error 返回包含稳定 code 的错误摘要。
func (e *AdapterError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.cause)
}

// Unwrap 返回底层错误，便于调用方继续检查原始 cause。
func (e *AdapterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is 保留 legacy sentinel 的 errors.Is 兼容性。
func (e *AdapterError) Is(target error) bool {
	if target == ErrAdapterUnavailable {
		return e != nil && e.Code == CodeAdapterUnavailable
	}
	return false
}

// AdapterErrorDetails 从错误链中提取 adapter 稳定错误信息。
func AdapterErrorDetails(err error) (AdapterErrorInfo, bool) {
	var adapterErr *AdapterError
	if errors.As(err, &adapterErr) && adapterErr != nil {
		return adapterErr.AdapterErrorInfo, true
	}
	return AdapterErrorInfo{}, false
}

func adapterRemediationHint(code string, provider model.CodeDebugProvider) string {
	switch code {
	case CodeDAPConnectionFailed:
		return "The adapter process did not accept DAP connections; check the adapter command, port, and startup logs."
	}
	switch provider {
	case model.CodeDebugProviderGo:
		return "Install Delve with `go install github.com/go-delve/delve/cmd/dlv@latest` and ensure `dlv` is on PATH."
	case model.CodeDebugProviderPython:
		return "Install debugpy with `python3 -m pip install debugpy` and ensure `python3` is on PATH."
	case model.CodeDebugProviderNode:
		return "Configure code_debug.adapter_command for the experimental Node provider and ensure it is executable."
	default:
		return "Install or configure the requested debug adapter and ensure it is on PATH."
	}
}

// Target 描述一个允许打开代码调试会话的本机 deployment。
type Target struct {
	ProjectID               string                   `json:"project_id"`
	ProjectName             string                   `json:"project_name"`
	RootPath                string                   `json:"root_path"`
	ServiceID               string                   `json:"service_id"`
	ServiceName             string                   `json:"service_name"`
	DeploymentID            string                   `json:"deployment_id"`
	EnvName                 string                   `json:"env_name"`
	Provider                model.CodeDebugProvider  `json:"provider"`
	Experimental            bool                     `json:"experimental,omitempty"`
	Command                 string                   `json:"command,omitempty"`
	WorkDir                 string                   `json:"work_dir,omitempty"`
	Enabled                 bool                     `json:"enabled"`
	StartMode               model.CodeDebugStartMode `json:"start_mode,omitempty"`
	KeepRuntimeOnLeaseClose bool                     `json:"keep_runtime_on_lease_close,omitempty"`
	RuntimeState            RuntimeState             `json:"runtime_state,omitempty"`
	LeaseActive             bool                     `json:"lease_active,omitempty"`
	CanOpen                 bool                     `json:"can_open"`
	UnavailableReason       string                   `json:"unavailable_reason,omitempty"`
}

// OpenRequest 描述创建代码调试会话的请求。
type OpenRequest struct {
	DeploymentID  string `json:"deployment_id"`
	Provider      string `json:"provider,omitempty"`
	StopOnEntry   *bool  `json:"stop_on_entry,omitempty"`
	ApprovalToken string `json:"-"`
}

// LaunchConfig 描述启动 DAP adapter 和目标程序所需的稳定参数。
type LaunchConfig struct {
	Target         Target
	Provider       model.CodeDebugProvider
	Program        string
	Args           []string
	WorkingDir     string
	Env            map[string]string
	AdapterCommand string
	AdapterArgs    []string
	AdapterPort    int
	StopOnEntry    bool
}

// RuntimeState 描述 deployment 级 Debug Runtime 的当前状态。
type RuntimeState string

const (
	// RuntimeStateDebugRunning 表示 deployment 由 Debug Runtime 接管运行。
	RuntimeStateDebugRunning RuntimeState = "debug-running"
)

// Runtime 描述可复用的 Debug Runtime。
type Runtime struct {
	ProjectID    string                  `json:"project_id"`
	DeploymentID string                  `json:"deployment_id"`
	Provider     model.CodeDebugProvider `json:"provider"`
	AdapterPort  int                     `json:"adapter_port"`
	ProcessID    int                     `json:"process_id,omitempty"`
	State        RuntimeState            `json:"state"`
	Alive        bool                    `json:"alive"`
	CreatedAt    time.Time               `json:"created_at"`
	LastUsedAt   time.Time               `json:"last_used_at"`
}

// CloseRequest 描述关闭 AI lease 时是否同时停止 Debug Runtime。
type CloseRequest struct {
	StopRuntime *bool `json:"stop_runtime,omitempty"`
}

// DAP 描述 manager 依赖的 Debug Adapter Protocol 操作集合。
type DAP interface {
	Initialize(context.Context) (map[string]any, error)
	Launch(context.Context, map[string]any) error
	ConfigurationDone(context.Context) error
	SetBreakpoints(context.Context, string, []int) (map[string]any, error)
	Continue(context.Context, int) error
	Pause(context.Context, int) error
	Next(context.Context, int) error
	StepIn(context.Context, int) error
	StepOut(context.Context, int) error
	StackTrace(context.Context, int) (map[string]any, error)
	Scopes(context.Context, int) (map[string]any, error)
	Variables(context.Context, int) (map[string]any, error)
	Evaluate(context.Context, string, int) (map[string]any, error)
	Disconnect(context.Context) error
	WaitForStopped(context.Context) (map[string]any, error)
	Close() error
}

// Session 描述一个短生命周期代码调试会话。
type Session struct {
	ID           string                  `json:"session_id"`
	ProjectID    string                  `json:"project_id"`
	DeploymentID string                  `json:"deployment_id"`
	Provider     model.CodeDebugProvider `json:"provider"`
	AdapterPort  int                     `json:"adapter_port"`
	ProcessID    int                     `json:"process_id,omitempty"`
	RuntimeState RuntimeState            `json:"runtime_state,omitempty"`
	CreatedAt    time.Time               `json:"created_at"`
	LastUsedAt   time.Time               `json:"last_used_at"`
	Alive        bool                    `json:"alive"`
	Closed       bool                    `json:"closed,omitempty"`
	Error        string                  `json:"error,omitempty"`
}
