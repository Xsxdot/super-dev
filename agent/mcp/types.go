// types.go 定义 MCP 包内部使用的 agent DTO。
//
// 职责：
//   - 补充 agent API 中未导出的响应类型
//   - 保持 MCP 输出与 HTTP API 解码解耦
//
// 边界：
//   - 已存在于 model 包的领域结构不重复定义
package mcp

import (
	"time"

	"github.com/superdev/agent/model"
	pipelinetemplate "github.com/superdev/agent/template"
)

// LogsResponse 是 deployment 日志分页接口响应。
type LogsResponse struct {
	Items []model.LogEntry `json:"items"`
	Next  struct {
		Time string `json:"time,omitempty"`
		ID   int64  `json:"id,omitempty"`
	} `json:"next"`
}

// LogSearchResponse 是项目/服务范围日志搜索接口响应。
type LogSearchResponse struct {
	Query            string           `json:"query"`
	Total            int              `json:"total"`
	Items            []model.LogEntry `json:"items"`
	DeploymentCounts map[string]int   `json:"deployment_counts"`
	HasMore          bool             `json:"has_more"`
}

// LogContextResponse 是围绕某条日志拉取跨服务上下文的接口响应。
type LogContextResponse struct {
	TargetID          int64                       `json:"target_id"`
	AnchorTime        time.Time                   `json:"anchor_time"`
	ItemsByDeployment map[string][]model.LogEntry `json:"items_by_deployment"`
}

// PipelineTemplateSummary 是模板导入接口返回的模板摘要。
type PipelineTemplateSummary struct {
	Source      string                            `json:"source"`
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	Version     string                            `json:"version"`
	Digest      string                            `json:"digest"`
	Description string                            `json:"description,omitempty"`
	Inputs      map[string]pipelinetemplate.Input `json:"inputs,omitempty"`
}

// PipelineTemplatePreview 是模板 dry-run preview 结果。
type PipelineTemplatePreview = pipelinetemplate.PreviewResult

// OperationRequest 描述 MCP 请求 agent 生成 operation plan 的参数。
type OperationRequest struct {
	Kind         string `json:"kind"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	EnvName      string `json:"env_name,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	TemplatePath string `json:"template_path,omitempty"`
}

// OperationTarget 描述 operation plan 中绑定的稳定目标。
type OperationTarget struct {
	ProjectID      string `json:"project_id,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	EnvName        string `json:"env_name,omitempty"`
	ServiceID      string `json:"service_id,omitempty"`
	ServiceName    string `json:"service_name,omitempty"`
	DeploymentID   string `json:"deployment_id,omitempty"`
	TemplatePath   string `json:"template_path,omitempty"`
	TemplateDigest string `json:"template_digest,omitempty"`
	PipelineID     string `json:"pipeline_id,omitempty"`
}

// OperationCheck 描述 operation preflight 中的一条检查项。
type OperationCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// OperationPlan 是 agent 返回给 MCP 的写操作预检结果。
type OperationPlan struct {
	ID               string           `json:"id"`
	Kind             string           `json:"kind"`
	Target           OperationTarget  `json:"target"`
	TargetSummary    string           `json:"target_summary"`
	RiskLevel        string           `json:"risk_level"`
	RequiresApproval bool             `json:"requires_approval"`
	Denied           bool             `json:"denied"`
	Reasons          []string         `json:"reasons,omitempty"`
	ExpectedEffects  []string         `json:"expected_effects,omitempty"`
	Checks           []OperationCheck `json:"checks,omitempty"`
	Fingerprint      string           `json:"fingerprint"`
	CreatedAt        time.Time        `json:"created_at"`
	ExpiresAt        time.Time        `json:"expires_at"`
}

// OperationApproval 是 agent 返回给 MCP 的审批请求摘要。
type OperationApproval struct {
	ID             string        `json:"id"`
	Plan           OperationPlan `json:"plan"`
	Status         string        `json:"status"`
	RequestedBy    string        `json:"requested_by"`
	RequesterLabel string        `json:"requester_label"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	ExpiresAt      time.Time     `json:"expires_at"`
	DecidedBy      string        `json:"decided_by,omitempty"`
	DecisionNote   string        `json:"decision_note,omitempty"`
}

// OperationApprovalDetail 是读取单条审批时的详情和一次性 token。
type OperationApprovalDetail struct {
	Approval      OperationApproval `json:"approval"`
	ApprovalToken string            `json:"approval_token,omitempty"`
}

// OperationAuditEvent 是 operation 安全链路中的一条审计事实。
type OperationAuditEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Action     string         `json:"action"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Plan       OperationPlan  `json:"plan"`
	Summary    string         `json:"summary"`
	Data       map[string]any `json:"data,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// OperationAuditList 是 operation 审计查询响应。
type OperationAuditList struct {
	Events []OperationAuditEvent `json:"events"`
	Count  int                   `json:"count"`
}

// ConfigChangeRequest 描述 MCP 侧配置 upsert 请求。
type ConfigChangeRequest struct {
	Kind           string         `json:"kind"`
	ProjectID      string         `json:"project_id,omitempty"`
	ProjectName    string         `json:"project_name,omitempty"`
	RootPath       string         `json:"root_path,omitempty"`
	ApprovalToken  string         `json:"approval_token,omitempty"`
	DebugSessionID string         `json:"debug_session_id,omitempty"`
	Project        map[string]any `json:"project,omitempty"`
	Service        map[string]any `json:"service,omitempty"`
	Pipeline       map[string]any `json:"pipeline,omitempty"`
	Delete         bool           `json:"delete,omitempty"`
	Remove         bool           `json:"remove,omitempty"`
}

// ConfigChangeValidation 描述 config change preview 的校验结果。
type ConfigChangeValidation struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ConfigChangeDiffEntry 描述配置变更中的一处差异。
type ConfigChangeDiffEntry struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// ConfigChangePreview 是 agent 返回给 MCP 的配置变更预览。
type ConfigChangePreview struct {
	ChangeID      string                  `json:"change_id"`
	Kind          string                  `json:"kind"`
	TargetSummary string                  `json:"target_summary"`
	Diff          []ConfigChangeDiffEntry `json:"diff"`
	Validation    ConfigChangeValidation  `json:"validation"`
	Plan          OperationPlan           `json:"plan"`
	Project       model.Project           `json:"project,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
}

// DebugSession 是 MCP 侧使用的本机排障会话 DTO。
type DebugSession struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	ProjectName  string     `json:"project_name"`
	EnvName      string     `json:"env_name,omitempty"`
	ServiceID    string     `json:"service_id,omitempty"`
	ServiceName  string     `json:"service_name,omitempty"`
	DeploymentID string     `json:"deployment_id,omitempty"`
	Title        string     `json:"title"`
	Question     string     `json:"question"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// DebugSessionEvent 是 MCP 侧使用的排障会话事件 DTO。
type DebugSessionEvent struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Summary   string         `json:"summary"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// DebugSessionCreateRequest 描述 MCP 创建排障会话的请求。
type DebugSessionCreateRequest struct {
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	EnvName      string `json:"env_name,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Title        string `json:"title"`
	Question     string `json:"question"`
}

// DebugSessionAppendEventRequest 描述 MCP 追加排障会话事件的请求。
type DebugSessionAppendEventRequest struct {
	Type    string         `json:"type"`
	Actor   string         `json:"actor"`
	Summary string         `json:"summary"`
	Data    map[string]any `json:"data,omitempty"`
}

// DebugSessionCreateResponse 是创建排障会话后的响应。
type DebugSessionCreateResponse struct {
	Session DebugSession      `json:"session"`
	Event   DebugSessionEvent `json:"event"`
}

// DebugSessionDetailResponse 是读取排障会话详情后的响应。
type DebugSessionDetailResponse struct {
	Session   DebugSession        `json:"session"`
	Events    []DebugSessionEvent `json:"events"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
}
