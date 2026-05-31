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
