// backend_factory.go 根据 Deployment 配置构造对应的 LogBackend 实例。
//
// 职责：
//   - location=local → SQLiteBackend（读本机 SQLite + logbuf）
//   - location=remote, 1 host → RemoteAgentBackend（SSH 隧道转发）
//   - location=remote, N host → FederatedBackend([RemoteAgentBackend × N])
//
// 边界：
//   - 不持有 backend 生命周期，调用方（App）负责存储和关闭
//   - deploymentID 仅用于 remote backend 的 collector 虚拟部署查询
package api

import (
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/logbuf"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

// buildBackend 根据 deployment 配置返回对应的 LogBackend。
//
// 参数：
//   - dep: 目标 deployment
//   - localDeploymentID: 本地 deployment ID（仅 local deployment 使用，用于 SQLiteBackend 过滤）
//   - s: 本地 SQLite store（local deployment 使用）
//   - buf: 本地 logbuf（local deployment 使用）
//   - resolver: 隧道地址解析器（remote deployment 使用）
func buildBackend(dep model.Deployment, localDeploymentID string, s *store.Store, buf *logbuf.Buffer, resolver logbackend.TunnelResolver) logbackend.LogBackend {
	if dep.Location == model.LocationLocal {
		return logbackend.NewSQLiteBackend(s, buf)
	}

	// remote deployment：按 host 数量决定单节点还是联邦
	if len(dep.HostIDs) == 1 {
		remoteDeploymentID := collector.CollectorID(dep.LogTarget, dep.LogType)
		return logbackend.NewRemoteAgentBackend(dep.HostIDs[0], remoteDeploymentID, resolver)
	}

	children := make([]logbackend.LogBackend, 0, len(dep.HostIDs))
	for _, hostID := range dep.HostIDs {
		remoteDeploymentID := collector.CollectorID(dep.LogTarget, dep.LogType)
		children = append(children, logbackend.NewRemoteAgentBackend(hostID, remoteDeploymentID, resolver))
	}
	return logbackend.NewFederatedBackend(children)
}
