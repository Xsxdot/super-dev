// Package plugins 中的 transfer.go 实现远程文件传输插件。
//
// 职责：
//   - 校验 transfer 参数
//   - 将文件传输请求分发给注入的 FileTransfer
//
// 边界：
//   - 不直接实现 SCP/SSH 协议
//   - 不递归打包目录，目录处理由 archive 插件负责
package plugins

import (
	"context"
	"errors"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

// FileTransfer 是 transfer 插件依赖的远程文件传输能力。
type FileTransfer interface {
	Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error
}

// Transfer copies one local file to remote targets.
type Transfer struct {
	transfer FileTransfer
}

// NewTransfer creates Transfer.
//
// 参数：
//   - transfer: 文件传输能力，可由 SSHExecutor 提供
//
// 返回：
//   - transfer 插件实例
func NewTransfer(transfer FileTransfer) *Transfer {
	return &Transfer{transfer: transfer}
}

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `transfer`
func (p *Transfer) Name() string { return "transfer" }

// Validate checks transfer step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - with.source 或 with.target 缺失时返回错误
func (p *Transfer) Validate(step model.Step) error {
	if withString(step.With, "source", "from", "src") == "" {
		return errors.New("transfer requires with.source")
	}
	if withString(step.With, "target", "to", "dest") == "" {
		return errors.New("transfer requires with.target")
	}
	return nil
}

// Execute transfers the configured file to all targets.
//
// 参数：
//   - ctx: 插件运行上下文
//   - step: transfer 步骤
//   - targets: 已解析出的远程目标列表
//
// 返回：
//   - 无 target、transfer 能力缺失或任一传输失败时返回错误
func (p *Transfer) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("transfer requires targets")
	}
	if p.transfer == nil {
		return errors.New("file transfer is required")
	}
	source := withString(step.With, "source", "from", "src")
	targetPath := withString(step.With, "target", "to", "dest")
	for _, target := range targets {
		if err := p.transfer.Transfer(ctx.Context, target, source, targetPath, ctx.LogLine); err != nil {
			return err
		}
	}
	return nil
}
