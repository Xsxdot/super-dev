// Package metrics 采集 deployment 实例的进程级运行指标。
//
// 职责：
//   - 通过系统自带命令读取 systemd、docker 和裸进程指标
//   - 将命令输出解析为 model.InstanceMetrics
//
// 边界：
//   - 不展示主机整机负载
//   - 不持久化指标快照
//   - 不访问 HTTP API 或项目配置
package metrics

import (
	"context"
	"os/exec"
	"strings"
)

// CommandExecutor 抽象系统命令执行，便于测试注入假输出。
type CommandExecutor interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecCommandExecutor 使用 os/exec 执行真实系统命令。
type ExecCommandExecutor struct{}

// Run 执行命令并返回 stdout/stderr 合并输出。
func (ExecCommandExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}
