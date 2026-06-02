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
