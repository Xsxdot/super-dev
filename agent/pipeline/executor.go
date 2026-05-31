// Package pipeline 提供部署流水线的执行引擎。
//
// 职责：
//   - Engine：按 model.Run 骨架执行流水线
//   - StepPlugin 接口：抽象插件校验与执行
//   - RunContext：为插件提供上下文与日志回调
//
// 边界：
//   - 引擎不直接拼接命令或连接 SSH，全部通过 StepPlugin 接口
//   - 不持久化 Run、不推送事件——状态变更通过回调上报，由上层（API/store）承接
//   - 不解析 YAML、不查 store
package pipeline

import (
	"context"

	"github.com/superdev/agent/model"
)

// StepPlugin is the extension point for concrete step actions.
type StepPlugin interface {
	Name() string
	Validate(step model.Step) error
	Execute(ctx *RunContext, step model.Step, targets []Target) error
}

// RunContextOptions configures a RunContext.
type RunContextOptions struct {
	ProjectRoot string
	RunTempDir  string
	Vars        map[string]string
	LogLine     func(line, stream string)
}

// RunContext is passed to plugins during execution.
type RunContext struct {
	Context     context.Context
	ProjectRoot string
	RunTempDir  string
	Vars        map[string]string
	logLine     func(line, stream string)
}

// NewRunContext creates plugin execution context.
//
// 参数：
//   - ctx: 上下文，用于取消插件执行
//   - opts: 运行上下文配置
//
// 返回：
//   - 可传给 StepPlugin.Execute 的 RunContext
func NewRunContext(ctx context.Context, opts RunContextOptions) *RunContext {
	return &RunContext{Context: ctx, ProjectRoot: opts.ProjectRoot, RunTempDir: opts.RunTempDir, Vars: opts.Vars, logLine: opts.LogLine}
}

// LogLine records one plugin output line.
//
// 参数：
//   - line: 单行日志内容
//   - stream: 日志流名，如 stdout/stderr
//
// 注意：
//   - 未配置 LogLine 回调时该方法为空操作
func (c *RunContext) LogLine(line, stream string) {
	if c.logLine != nil {
		c.logLine(line, stream)
	}
}
