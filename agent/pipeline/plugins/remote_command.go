// Package plugins 中的 remote_command.go 实现远程命令插件。
//
// 职责：
//   - 校验 remote_command 参数
//   - 将远程命令分发给注入的 RemoteRunner
//
// 边界：
//   - 不直接建立 SSH 连接
//   - 不做 DAG 调度或重试
package plugins

import (
	"context"
	"errors"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

// RemoteRunner 是 remote_command 依赖的远程命令执行能力。
type RemoteRunner interface {
	RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error
}

// RemoteCommand runs a shell command on remote targets.
type RemoteCommand struct {
	runner RemoteRunner
}

// NewRemoteCommand creates RemoteCommand.
//
// 参数：
//   - runner: 远程命令执行能力，可由 SSHExecutor 提供
//
// 返回：
//   - remote_command 插件实例
func NewRemoteCommand(runner RemoteRunner) *RemoteCommand {
	return &RemoteCommand{runner: runner}
}

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `remote_command`
func (p *RemoteCommand) Name() string { return "remote_command" }

// Validate checks remote_command step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - with.cmd 缺失时返回错误
func (p *RemoteCommand) Validate(step model.Step) error {
	if withString(step.With, "cmd", "command") == "" {
		return errors.New("remote_command requires with.cmd")
	}
	return nil
}

// ValidateTargets checks remote_command resolved target requirements.
//
// 参数：
//   - step: 待校验步骤，当前仅用于满足 pipeline.TargetValidator 接口
//   - targets: 已解析出的远程目标列表
//
// 返回：
//   - targets 为空时返回错误
//
// 注意：
//   - 不检查 runner，因为 runner 是运行时依赖，不属于 pipeline 配置
func (p *RemoteCommand) ValidateTargets(_ model.Step, targets []pipeline.Target) error {
	if len(targets) == 0 {
		return errors.New("remote_command requires targets")
	}
	return nil
}

// Execute runs the configured command on all targets.
//
// 参数：
//   - ctx: 插件运行上下文
//   - step: remote_command 步骤
//   - targets: 已解析出的远程目标列表
//
// 返回：
//   - 无 target、runner 缺失或任一远程命令失败时返回错误
func (p *RemoteCommand) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	if err := p.ValidateTargets(step, targets); err != nil {
		return err
	}
	if p.runner == nil {
		return errors.New("remote_command runner is required")
	}
	cmd := withString(step.With, "cmd", "command")
	workDir := withString(step.With, "workDir", "work_dir", "workdir")
	for _, target := range targets {
		if err := p.runner.RunRemote(ctx.Context, target, cmd, workDir, ctx.LogLine); err != nil {
			return err
		}
	}
	return nil
}
