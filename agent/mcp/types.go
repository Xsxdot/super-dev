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
