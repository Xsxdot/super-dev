// routing_runner.go 实现 pipeline remote step 的 per-host 通道路由。
//
// 职责：
//   - 按 agenthealth.Status 为每个 target 选择 agent 或 SSH
//   - 在 onLine 中记录本次通道选择
//
// 边界：
//   - 不主动探活，只读取注入的健康状态
//   - 不在 healthy agent 调用失败后自动 fallback
//   - 不感知 pipeline DAG 调度
package pipeline

import (
	"context"
	"fmt"

	"github.com/superdev/agent/agenthealth"
)

// AgentHealthLookup 查询 host 的 agent 健康状态。
type AgentHealthLookup interface {
	Status(hostID string) agenthealth.Status
}

// remoteTransport 是 RoutingRunner 内部使用的组合能力。
// 不导出该接口，避免与 plugins.RemoteRunner / plugins.FileTransfer 形成重复公共契约。
type remoteTransport interface {
	RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error
	Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error
}

// RoutingRunner 按 host agent 健康状态选择 AgentRunner 或 SSHExecutor。
type RoutingRunner struct {
	health AgentHealthLookup
	agent  remoteTransport
	ssh    remoteTransport
}

// NewRoutingRunner 创建 per-host 远程执行路由器。
//
// 参数：
//   - health: agent 健康状态查询器
//   - agent: healthy 时使用的 runner
//   - ssh: 非 healthy 时使用的 fallback runner
//
// 返回：
//   - 可分别满足 plugins.RemoteRunner 和 plugins.FileTransfer 的 RoutingRunner
func NewRoutingRunner(health AgentHealthLookup, agent remoteTransport, ssh remoteTransport) *RoutingRunner {
	return &RoutingRunner{health: health, agent: agent, ssh: ssh}
}

// RunRemote 按 target.HostID 的 agent 健康状态选择执行通道。
func (r *RoutingRunner) RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error {
	runner, channel, err := r.route(target)
	if err != nil {
		return err
	}
	logRoute(target, channel, onLine)
	return runner.RunRemote(ctx, target, cmd, workDir, onLine)
}

// Transfer 按 target.HostID 的 agent 健康状态选择传输通道。
func (r *RoutingRunner) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error {
	runner, channel, err := r.route(target)
	if err != nil {
		return err
	}
	logRoute(target, channel, onLine)
	return runner.Transfer(ctx, target, source, targetPath, onLine)
}

func (r *RoutingRunner) route(target Target) (remoteTransport, string, error) {
	if r.health != nil && r.health.Status(target.HostID) == agenthealth.StatusHealthy {
		if r.agent == nil {
			return nil, "", fmt.Errorf("agent runner is required")
		}
		return r.agent, "agent", nil
	}
	if r.ssh == nil {
		return nil, "", fmt.Errorf("ssh runner is required")
	}
	return r.ssh, "ssh", nil
}

func logRoute(target Target, channel string, onLine func(string, string)) {
	if onLine == nil {
		return
	}
	host := target.HostID
	if host == "" {
		host = target.HostName
	}
	onLine("remote route host "+host+" -> "+channel, "system")
}
