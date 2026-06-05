// Package configchange 提供 MCP 配置 upsert 的纯业务模型。
//
// 职责：
//   - 定义配置变更请求、预览响应、diff 和校验结果
//   - 约束 MCP 只能表达新增/编辑，不能表达删除
//
// 边界：
//   - 不读写 .superdev/config.yaml
//   - 不访问 agent registry、进程管理器或审批存储
package configchange

import (
	"errors"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

const (
	// KindProjectUpsert 表示新增或编辑项目基础配置。
	KindProjectUpsert = "config.project.upsert"
	// KindServiceUpsert 表示新增或编辑一个 service 及其 deployments。
	KindServiceUpsert = "config.service.upsert"
	// KindPipelineUpsert 表示新增或编辑一条项目级流水线。
	KindPipelineUpsert = "config.pipeline.upsert"
)

var (
	// ErrInvalidChange 表示配置变更 kind 或目标字段不合法。
	ErrInvalidChange = errors.New("invalid config change")
	// ErrUnsupportedOperation 表示请求包含删除等本期不支持的能力。
	ErrUnsupportedOperation = errors.New("unsupported config operation")
)

// ChangeRequest 描述一次 MCP 配置变更。
type ChangeRequest struct {
	Kind           string                `json:"kind"`
	ProjectID      string                `json:"project_id,omitempty"`
	ProjectName    string                `json:"project_name,omitempty"`
	RootPath       string                `json:"root_path,omitempty"`
	ApprovalToken  string                `json:"approval_token,omitempty"`
	DebugSessionID string                `json:"debug_session_id,omitempty"`
	Project        *ProjectPatch         `json:"project,omitempty"`
	Service        *ServicePatch         `json:"service,omitempty"`
	Pipeline       *ProjectPipelinePatch `json:"pipeline,omitempty"`
	Delete         bool                  `json:"delete,omitempty"`
	Remove         bool                  `json:"remove,omitempty"`
}

// ProjectPatch 描述项目基础配置的局部 upsert。
type ProjectPatch struct {
	Name         string              `json:"name,omitempty"`
	Variables    map[string]string   `json:"variables,omitempty"`
	Environments []model.Environment `json:"environments,omitempty"`
}

// ServicePatch 描述 service 的局部 upsert。
type ServicePatch struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Required    *bool             `json:"required,omitempty"`
	Order       *int              `json:"order,omitempty"`
	Deployments []DeploymentPatch `json:"deployments,omitempty"`
}

// DeploymentPatch 描述 deployment 的局部 upsert。
type DeploymentPatch struct {
	ID           string               `json:"id,omitempty"`
	EnvName      string               `json:"env_name,omitempty"`
	Location     model.DeployLocation `json:"location,omitempty"`
	ControlMode  model.ControlMode    `json:"control_mode,omitempty"`
	Runtime      *model.RuntimeConfig `json:"runtime,omitempty"`
	Logs         *model.LogConfig     `json:"logs,omitempty"`
	Command      string               `json:"command,omitempty"`
	WorkDir      string               `json:"work_dir,omitempty"`
	EnvFile      string               `json:"env_file,omitempty"`
	Env          map[string]string    `json:"env,omitempty"`
	HostIDs      []string             `json:"host_ids,omitempty"`
	LogType      model.LogSourceType  `json:"log_type,omitempty"`
	LogTarget    string               `json:"log_target,omitempty"`
	ExtraArgs    []string             `json:"extra_args,omitempty"`
	ReadOnly     *bool                `json:"read_only,omitempty"`
	StartCommand string               `json:"start_command,omitempty"`
	StopCommand  string               `json:"stop_command,omitempty"`
}

// ProjectPipelinePatch 描述项目级流水线的 upsert。
type ProjectPipelinePatch = model.ProjectPipeline

// ValidationResult 描述 config change 校验结果。
type ValidationResult struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// DiffEntry 描述配置变更中的一处结构化差异。
type DiffEntry struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// PreviewResult 是 agent 返回给 MCP 的配置变更预览。
type PreviewResult struct {
	ChangeID      string           `json:"change_id"`
	Kind          string           `json:"kind"`
	TargetSummary string           `json:"target_summary"`
	Diff          []DiffEntry      `json:"diff"`
	Validation    ValidationResult `json:"validation"`
	Plan          operation.Plan   `json:"plan"`
	Project       model.Project    `json:"project,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
}
