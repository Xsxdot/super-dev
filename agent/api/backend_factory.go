// backend_factory.go 根据 Deployment 配置构造对应的 LogBackend 实例。
//
// 职责：
//   - location=local 且项目归属未转移（或非 dev 部署）→ SQLiteBackend
//     （读本机 SQLite + logbuf）
//   - location=local 且 dev 部署所属项目归属已转移到另一节点 → 归属路由：
//     RemoteAgentBackend 指向归属机（见下方「为什么复用 RemoteAgentBackend」）
//   - location=remote, 1 host → RemoteAgentBackend（SSH 隧道转发）
//   - location=remote, N host → FederatedBackend([RemoteAgentBackend × N])
//
// 边界：
//   - 不持有 backend 生命周期，调用方（App）负责存储和关闭
//   - deploymentID 仅用于 remote backend 的 collector 虚拟部署查询
//   - 不自己判定归属/dev 归属：isDevEnv、homeHostID 均由调用方
//     （registerProjectBackendsLocked）算好传入，本函数只负责按值分支，
//     保持纯函数、便于单测覆盖每条分支
package api

import (
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/store"
)

// buildBackend 根据 deployment 配置返回对应的 LogBackend。
//
// 参数：
//   - dep: 目标 deployment
//   - localDeploymentID: 本地 deployment ID（仅 local deployment 使用，用于 SQLiteBackend 过滤）
//   - s: 本地 SQLite store（local deployment 使用）
//   - buf: 本地 logbuf（local deployment 使用）
//   - transport: 节点传输（remote deployment 使用，归属路由分支同样复用）
//   - isDevEnv: dep 所属 EnvName 是否为其项目声明的 dev 环境（调用方按
//     devEnvSet(project) 算好传入）——归属只描述"dev 环境在哪个节点上跑"，
//     prod 部署的 host 由自身 Location/HostIDs 钉死，不受归属影响，与
//     project_home_routing.go 的 homeRouteTargetForDeployment 判断口径一致
//   - homeHostID: 该 deployment 所属项目的归属主机 ID；空串表示未设归属
//     或归属恰好是本机（调用方用 projectHomeOf 已做过自身 ID 折叠）
func buildBackend(dep model.Deployment, localDeploymentID string, s *store.Store, buf *logbuf.Buffer, transport nodetransport.NodeTransport, isDevEnv bool, homeHostID string) logbackend.LogBackend {
	if dep.Location == model.LocationLocal {
		// 归属路由：dev 部署且项目归属已转移到另一节点时，日志读取必须转向
		// 归属机，否则本机 SQLite 里这条 deployment 早已没有新日志写入。
		//
		// 为什么复用 RemoteAgentBackend：归属机上它是 local deployment，
		// 日志天然在其 SQLite，这正是 remote 日志读取协议的服务对象。
		//
		// dep.ID 直接作为 deploymentID（而非 remoteCollectorIDForDeployment）：
		// 这不是一个虚拟 collector，是随 project.yaml 流动、跨机保持一致的
		// 真实 deployment ID，归属机上按同一个 ID 注册了它自己的 SQLiteBackend，
		// GET /api/logs?deployment=<dep.ID> 天然能查到。
		if isDevEnv && homeHostID != "" {
			return logbackend.NewRemoteAgentBackend(homeHostID, dep.ID, transport)
		}
		return logbackend.NewSQLiteBackend(s, buf)
	}

	// remote deployment：按 host 数量决定单节点还是联邦
	if len(dep.HostIDs) == 1 {
		remoteDeploymentID := remoteCollectorIDForDeployment(dep)
		return logbackend.NewRemoteAgentBackend(dep.HostIDs[0], remoteDeploymentID, transport)
	}

	children := make([]logbackend.LogBackend, 0, len(dep.HostIDs))
	for _, hostID := range dep.HostIDs {
		remoteDeploymentID := remoteCollectorIDForDeployment(dep)
		children = append(children, logbackend.NewRemoteAgentBackend(hostID, remoteDeploymentID, transport))
	}
	return logbackend.NewFederatedBackend(children)
}

func remoteCollectorIDForDeployment(dep model.Deployment) string {
	if dep.ID != "" {
		return dep.ID
	}
	return collector.CollectorID(deploymentCollectorName(dep), deploymentCollectorType(dep))
}

// deploymentCollectorType 返回远程日志采集任务类型，优先使用新 logs 配置。
func deploymentCollectorType(dep model.Deployment) model.LogSourceType {
	if dep.Logs != nil && dep.Logs.Type != "" {
		return model.LogSourceType(dep.Logs.Type)
	}
	return dep.LogType
}

// deploymentCollectorName 返回远程日志采集任务的目标名、路径或命令，优先使用新 logs 配置。
func deploymentCollectorName(dep model.Deployment) string {
	if dep.Logs == nil {
		return dep.LogTarget
	}
	switch dep.Logs.Type {
	case model.LogKindFileTail:
		return dep.Logs.Path
	case model.LogKindCommand:
		return dep.Logs.Command
	default:
		if dep.Logs.Target != "" {
			return dep.Logs.Target
		}
		return dep.LogTarget
	}
}
