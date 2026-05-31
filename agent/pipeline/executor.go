// Package pipeline 提供部署流水线的执行引擎。
//
// 职责：
//   - Engine：按 model.Run 骨架执行流水线
//   - Executor 接口：抽象「在某个 Target 上执行命令 / 传输文件」
//   - 为旧本地/SSH 执行器提供 Step.With 参数读取工具
//
// 边界：
//   - 引擎不直接拼接命令或连接 SSH，全部通过 Executor 接口
//   - 不持久化 Run、不推送事件——状态变更通过回调上报，由上层（API/store）承接
//   - 不解析 YAML、不查 store
package pipeline

import (
	"context"
	"fmt"

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
	LogLine     func(line, stream string)
}

// RunContext is passed to plugins during execution.
type RunContext struct {
	Context     context.Context
	ProjectRoot string
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
	return &RunContext{Context: ctx, ProjectRoot: opts.ProjectRoot, logLine: opts.LogLine}
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

// Executor 抽象「在某个 Target 上执行一个插件步骤」。
//
// 实现需把命令/文件传输输出逐行通过 onLine 回调上报。
type Executor interface {
	// Run 在 target 上执行命令（step.With["cmd"] / step.With["workDir"]），返回退出码与错误。
	// onLine(line, stream) 逐行上报输出，stream 为 "stdout"/"stderr"。
	Run(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) (exitCode int, err error)
	// Sync 把 step.With["source"] 同步到 target 的 step.With["target"]。local target 应返回错误。
	Sync(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) error
}

func stepWithString(step model.Step, keys ...string) string {
	if step.With == nil {
		return ""
	}
	for _, key := range keys {
		if raw, ok := step.With[key]; ok && raw != nil {
			if s, ok := raw.(string); ok {
				return s
			}
			return fmt.Sprint(raw)
		}
	}
	return ""
}
