// runtime_remote_controller.go 编排 remote managed deployment 的生命周期命令。
//
// 职责：
//   - 根据 deployment 的 host_ids 解析远程主机
//   - 通过 pipeline.RoutingRunner 执行 start_command / stop_command
//   - 将远端命令输出归属到 deployment 日志
//
// 边界：
//   - 不管理本地子进程，local/launchd 仍由 process.Manager 负责
//   - 不修改项目配置或 pipeline 配置
//   - 不绕过 operation 安全门禁，调用方必须先完成授权
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/logparse"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

type remoteRuntimeController struct {
	runner pipelineRemoteTransport
	hosts  func() ([]model.Host, error)
	emit   func(model.LogEntry)
}

func (a *App) newRemoteRuntimeController() *remoteRuntimeController {
	sshExecutor := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		hosts, err := a.remoteStore.ListHosts()
		if err != nil {
			return model.Host{}, false
		}
		for _, host := range hosts {
			if host.ID == hostID {
				return host, true
			}
		}
		return model.Host{}, false
	})
	agentRunner := a.pipelineAgentRunner
	if agentRunner == nil {
		agentRunner = pipeline.NewAgentRunner(a.tunnelResolver)
	}
	return &remoteRuntimeController{
		runner: pipeline.NewRoutingRunner(a.agentHealth, agentRunner, sshExecutor),
		hosts:  a.remoteStore.ListHosts,
		emit:   a.buf.Append,
	}
}

func (c *remoteRuntimeController) Start(ctx context.Context, dep model.Deployment) error {
	return c.run(ctx, dep, "start", dep.StartCommand)
}

func (c *remoteRuntimeController) Stop(ctx context.Context, dep model.Deployment) error {
	return c.run(ctx, dep, "stop", dep.StopCommand)
}

func (c *remoteRuntimeController) Restart(ctx context.Context, dep model.Deployment) error {
	if err := c.Stop(ctx, dep); err != nil {
		return err
	}
	return c.Start(ctx, dep)
}

func (c *remoteRuntimeController) run(ctx context.Context, dep model.Deployment, action string, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("remote %s command is required", action)
	}
	targets, err := c.targets(dep)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := c.runner.RunRemote(ctx, target, command, "", func(line, stream string) {
			c.emit(model.LogEntry{
				DeploymentID: dep.ID,
				Timestamp:    time.Now().UTC(),
				Level:        logparse.DetectLevel(line),
				Message:      line,
				Stream:       stream,
				SourceID:     target.HostID,
			})
		}); err != nil {
			return fmt.Errorf("remote %s command failed on host %s: %w", action, target.HostID, err)
		}
	}
	return nil
}

func (c *remoteRuntimeController) targets(dep model.Deployment) ([]pipeline.Target, error) {
	if len(dep.HostIDs) == 0 {
		return nil, fmt.Errorf("remote deployment %s host_ids are required", dep.ID)
	}
	hosts, err := c.hosts()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.Host, len(hosts))
	for _, host := range hosts {
		byID[host.ID] = host
	}
	targets := make([]pipeline.Target, 0, len(dep.HostIDs))
	for _, hostID := range dep.HostIDs {
		hostID = strings.TrimSpace(hostID)
		if hostID == "" {
			return nil, fmt.Errorf("remote deployment %s host_ids contain empty host", dep.ID)
		}
		host, ok := byID[hostID]
		if !ok {
			return nil, fmt.Errorf("unknown remote host %s", hostID)
		}
		targets = append(targets, pipeline.Target{HostID: host.ID, HostName: host.Name})
	}
	return targets, nil
}
