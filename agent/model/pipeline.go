// Package model 中的 pipeline.go 定义部署流水线的声明模型。
//
// 职责：
//   - 声明模型：Pipeline / Step / StepScope / StepAction，描述「同步→构建→启停」流程
//
// 边界：
//   - 仅数据结构定义，不含任何 I/O 或命令执行（执行在 pipeline 包）
//   - StepScope/StepAction 为开放枚举，未来新增 rolling 作用域不破坏结构
package model

// StepScope 步骤的执行作用域。开放枚举，未来加 rolling 不动引擎。
type StepScope string

const (
	// ScopeLocal 在本机执行一次（如 build、本地打包）。
	ScopeLocal StepScope = "local"
	// ScopeFanOut 对 deployment 的每台 host 并行执行（如 sync 产物、restart）。
	ScopeFanOut StepScope = "fan-out"
)

// StepAction 步骤动作类型。开放枚举。
type StepAction string

const (
	// ActionRun 执行命令（local 在本机，fan-out 在各 host）。
	ActionRun StepAction = "run"
	// ActionSync 把本地文件同步到各 host（仅 fan-out 有意义）。
	ActionSync StepAction = "sync"
)

// Pipeline 描述一个 deployment 的同步/构建/启停流程：一串有序 Step。
// 为空（nil）时 deployment 退回单命令模式，向后兼容。
type Pipeline struct {
	Steps []Step `json:"steps" yaml:"steps"`
}

// Step 流程中的一步。每步声明在哪执行（Scope）和执行什么（Action）。
//
// 字段互斥约束：
//   - Command / WorkDir 仅 ActionRun 有意义，其他 Action 下应保持零值
//   - SyncFrom / SyncTo 仅 ActionSync 有意义，其他 Action 下应保持零值
type Step struct {
	// ID 步骤唯一标识符，必须非空且在 Pipeline 内唯一。
	ID     string     `json:"id"     yaml:"id"`
	Name   string     `json:"name"   yaml:"name"`
	Scope  StepScope  `json:"scope"  yaml:"scope"`
	Action StepAction `json:"action" yaml:"action"`

	// Action=run 时使用
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
	WorkDir string `json:"work_dir,omitempty" yaml:"work_dir,omitempty"`

	// Action=sync 时使用
	SyncFrom string `json:"sync_from,omitempty" yaml:"sync_from,omitempty"`
	SyncTo   string `json:"sync_to,omitempty"   yaml:"sync_to,omitempty"`
}

// RunStatus 通用执行状态，Run / StepRun / Task 共用。
type RunStatus string

const (
	// StatusPending 待执行。
	StatusPending RunStatus = "pending"
	// RunStatusRunning 执行中（区别于 ServiceStatus.StatusRunning，避免同包常量冲突）。
	RunStatusRunning RunStatus = "running"
	// StatusSuccess 执行成功。
	StatusSuccess RunStatus = "success"
	// RunStatusFailed 执行失败（区别于 ServiceStatus.StatusFailed，避免同包常量冲突）。
	RunStatusFailed RunStatus = "failed"
	// StatusCanceled 被取消。
	StatusCanceled RunStatus = "canceled"
)

// Run 一次流水线执行。一个 deployment 同时只允许一个活跃 Run（约束由引擎/store 保证）。
type Run struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	Status       RunStatus `json:"status"`
	StepRuns     []StepRun `json:"step_runs"`
	StartedAt    int64     `json:"started_at"`
	FinishedAt   int64     `json:"finished_at,omitempty"`
}

// StepRun 一个 Step 在本次 Run 中的执行状态。
// local 步骤只有 1 个 Task；fan-out 步骤每台 host 一个 Task。
type StepRun struct {
	StepID string    `json:"step_id"`
	Name   string    `json:"name"`
	Scope  StepScope `json:"scope"`
	Status RunStatus `json:"status"`
	Tasks  []Task    `json:"tasks"`
}

// Task 最小执行单元 = 某步骤在某个位置的一次执行。GUI 的「格子」。
// HostID 为空表示本机（local 步骤）；非空表示远程 host（fan-out 步骤）。
type Task struct {
	HostID     string    `json:"host_id,omitempty"`
	HostName   string    `json:"host_name,omitempty"`
	Status     RunStatus `json:"status"`
	ExitCode   int       `json:"exit_code,omitempty"`
	StartedAt  int64     `json:"started_at,omitempty"`
	FinishedAt int64     `json:"finished_at,omitempty"`
}

// HostRef 是展开 fan-out 步骤所需的目标主机最小信息。
// 由上层（持有 deployment.HostIDs + Host 列表）解析后传入，
// 使 Expand 保持纯函数、不依赖 store。
type HostRef struct {
	ID   string
	Name string
}

// Expand 把声明的 Pipeline 按作用域展开成一个待执行的 Run 骨架。
//
// 参数：
//   - deploymentID: 关联的 deployment ID
//   - hosts: fan-out 步骤要扇出的目标主机；local 步骤忽略此参数
//
// 返回：
//   - 一个所有 Status 均为 pending 的 Run。ID、StartedAt 由引擎在执行时填充。
//
// 注意：
//   - local 步骤恒展开为 1 个无 HostID 的 Task
//   - fan-out 步骤为每台 host 展开 1 个 Task；hosts 为空则该步骤 0 个 Task
func (p Pipeline) Expand(deploymentID string, hosts []HostRef) Run {
	run := Run{DeploymentID: deploymentID, Status: StatusPending}
	for _, step := range p.Steps {
		sr := StepRun{StepID: step.ID, Name: step.Name, Scope: step.Scope, Status: StatusPending}
		switch step.Scope {
		case ScopeLocal:
			sr.Tasks = []Task{{Status: StatusPending}}
		case ScopeFanOut:
			for _, h := range hosts {
				sr.Tasks = append(sr.Tasks, Task{HostID: h.ID, HostName: h.Name, Status: StatusPending})
			}
		}
		run.StepRuns = append(run.StepRuns, sr)
	}
	return run
}
