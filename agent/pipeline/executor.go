// Package pipeline 提供部署流水线的执行引擎。
//
// 职责：
//   - Engine：按 model.Run 骨架执行流水线（串行 step、并行 fan-out、fail-fast）
//   - Executor 接口：抽象「在某个 Target 上 run 命令 / sync 文件」，使引擎与具体执行方式解耦
//   - 实现：LocalExecutor（本机 sh -c）、SSHExecutor（远程 ssh/scp）
//
// 边界：
//   - 引擎不感知 local/remote 差异，全部通过 Executor 接口
//   - 不持久化 Run、不推送事件——状态变更通过回调上报，由上层（API/store）承接
//   - 不解析 YAML、不查 store；目标主机由上层解析为 model.HostRef 后传入
package pipeline

import (
	"context"

	"github.com/superdev/agent/model"
)

// Target 描述一个 step 在何处执行。HostID 为空表示本机。
type Target struct {
	HostID   string
	HostName string
}

// IsLocal 报告该目标是否为本机。
func (t Target) IsLocal() bool { return t.HostID == "" }

// Executor 抽象「在某个 Target 上执行一个 Step 动作」。
//
// 实现需把命令/文件传输的输出逐行通过 onLine 回调上报。
type Executor interface {
	// Run 在 target 上执行命令（step.Command/WorkDir），返回退出码与错误。
	// onLine(line, stream) 逐行上报输出，stream 为 "stdout"/"stderr"。
	Run(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) (exitCode int, err error)
	// Sync 把 step.SyncFrom 同步到 target 的 step.SyncTo。local target 应返回错误。
	Sync(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) error
}
