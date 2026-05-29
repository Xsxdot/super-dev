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
