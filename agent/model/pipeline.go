// Package model 中的 pipeline.go 定义部署流水线的插件化声明模型。
//
// 职责：
//   - 声明 Pipeline / Step / Template 等配置模型
//   - 声明 Run / StepRun / Task 等执行状态模型
//   - 保持模型纯净，不包含 YAML 解析、模板展开、DAG 调度或命令执行
//
// 边界：
//   - 不做 I/O，不访问配置文件或远程主机
//   - 插件私有参数统一放入 Step.With，由插件自行校验
package model

// PipelinePhase 标识流水线阶段。阶段间由引擎串行控制。
type PipelinePhase string

const (
	// PhaseBuild 是构建阶段。
	PhaseBuild PipelinePhase = "build"
	// PhaseDeploy 是部署阶段。
	PhaseDeploy PipelinePhase = "deploy"
	// PhaseFinally 是清理阶段，无论 build/deploy 成功失败都会执行。
	PhaseFinally PipelinePhase = "finally"
)

// Pipeline 描述 deployment 的插件化 DAG 流水线。
type Pipeline struct {
	Variables map[string]string   `json:"variables,omitempty" yaml:"variables,omitempty"`
	Roles     map[string][]string `json:"roles,omitempty" yaml:"roles,omitempty"`
	Build     []Step              `json:"build,omitempty" yaml:"build,omitempty"`
	Deploy    []Step              `json:"deploy,omitempty" yaml:"deploy,omitempty"`
	Finally   []Step              `json:"finally,omitempty" yaml:"finally,omitempty"`
}

// Step 是流水线中的插件化执行单元。
type Step struct {
	Name        string                 `json:"name" yaml:"name"`
	Type        string                 `json:"type" yaml:"type"`
	Needs       []string               `json:"needs,omitempty" yaml:"needs,omitempty"`
	Roles       []string               `json:"roles,omitempty" yaml:"roles,omitempty"`
	RunIf       string                 `json:"run_if,omitempty" yaml:"run_if,omitempty"`
	Concurrency string                 `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
	Retries     int                    `json:"retries,omitempty" yaml:"retries,omitempty"`
	RetryDelay  string                 `json:"retry_delay,omitempty" yaml:"retry_delay,omitempty"`
	With        map[string]interface{} `json:"with,omitempty" yaml:"with,omitempty"`
}

// RunStatus 通用执行状态，Run / StepRun / Task 共用。
type RunStatus string

const (
	// StatusPending 待执行。
	StatusPending RunStatus = "pending"
	// RunStatusRunning 执行中。
	RunStatusRunning RunStatus = "running"
	// StatusSuccess 执行成功。
	StatusSuccess RunStatus = "success"
	// RunStatusFailed 执行失败。
	RunStatusFailed RunStatus = "failed"
	// StatusSkipped 因条件或上游失败被跳过。
	StatusSkipped RunStatus = "skipped"
	// StatusCanceled 被取消。
	StatusCanceled RunStatus = "canceled"
)

// Run 一次流水线执行。
type Run struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id,omitempty"`
	PipelineID      string    `json:"pipeline_id,omitempty"`
	EnvName         string    `json:"env_name,omitempty"`
	DeploymentID    string    `json:"deployment_id"`
	ArtifactVersion string    `json:"artifact_version,omitempty"`
	Status          RunStatus `json:"status"`
	StepRuns        []StepRun `json:"step_runs"`
	StartedAt       int64     `json:"started_at"`
	FinishedAt      int64     `json:"finished_at,omitempty"`
}

// StepRun 是一个插件步骤在本次 Run 中的执行状态。
type StepRun struct {
	StepName string        `json:"step_name"`
	Type     string        `json:"type"`
	Phase    PipelinePhase `json:"phase"`
	Needs    []string      `json:"needs,omitempty"`
	Status   RunStatus     `json:"status"`
	Tasks    []Task        `json:"tasks"`
}

// Task 是某个 StepRun 在某个目标上的执行单元。
type Task struct {
	HostID      string    `json:"host_id,omitempty"`
	HostName    string    `json:"host_name,omitempty"`
	HostAddress string    `json:"host_address,omitempty"`
	Status      RunStatus `json:"status"`
	ExitCode    int       `json:"exit_code,omitempty"`
	StartedAt   int64     `json:"started_at,omitempty"`
	FinishedAt  int64     `json:"finished_at,omitempty"`
}

// HostRef 是展开 roles 时所需的目标主机最小信息。
type HostRef struct {
	ID      string
	Name    string
	Address string
}
