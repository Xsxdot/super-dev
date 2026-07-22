// handler_node_status.go 暴露节点状态中心和远端 agent 状态上报 WebSocket。
//
// 职责：
//   - /ws/node-status：远端 agent 周期上报本机 managed runtime/collector 状态
//   - /api/nodes：桌面端读取 NodeRegistry 当前快照
//   - /ws/nodes：桌面端订阅 NodeRegistry 快照变化
//
// 边界：
//   - 不建立 SSH 隧道，传输生命周期由 NodeTransport/TunnelTransport 管理
//   - 不持久化节点状态
//   - 不替代 deployment 日志 WebSocket
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

const nodeStatusReportInterval = 5 * time.Second

// listNodes 处理 GET /api/nodes。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - Registry 尚未装配时返回空数组，保持桌面端启动兼容
func (a *App) listNodes(w http.ResponseWriter, r *http.Request) {
	if a.nodeRegistry == nil {
		jsonOK(w, []nodetransport.NodeStatus{})
		return
	}
	jsonOK(w, a.nodeRegistry.Snapshot())
}

// wsNodes 处理 GET /ws/nodes，向桌面端推送 NodeRegistry 全量快照。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - 每次 Registry 变化都发送全量快照，前端只需要按 host_id 覆盖缓存
func (a *App) wsNodes(w http.ResponseWriter, r *http.Request) {
	if a.nodeRegistry == nil {
		jsonError(w, http.StatusServiceUnavailable, "node registry unavailable")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch, unsubscribe := a.nodeRegistry.Subscribe()
	defer unsubscribe()
	for {
		select {
		case snapshot, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(snapshot); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// wsNodeStatus 处理 GET /ws/node-status，供远端 agent 周期上报自身状态。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - host_id/host_name 来自 TunnelTransport 查询参数，用于绑定桌面端 host
func (a *App) wsNodeStatus(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	hostID := r.URL.Query().Get("host_id")
	hostName := r.URL.Query().Get("host_name")
	if hostID == "" {
		hostID = a.identity.NodeID
	}
	if hostID == "" {
		hostID = uuid.NewString()
	}
	if hostName == "" {
		// identity.DisplayName 可能来自操作系统 hostname，不能作为安全系统身份的隐式退化。
		hostName = hostID
	}

	publisher := newNodeStatusPublisher(a, hostID, hostName, nodeStatusReportInterval)
	unregister := a.registerNodeStatusPublisher(publisher)
	defer unregister()
	ch := publisher.Subscribe(r.Context())
	for batch := range ch {
		if err := conn.WriteJSON(batch); err != nil {
			return
		}
	}
}

func (a *App) nodeStatusSnapshot(ctx context.Context, hostID, hostName string) nodetransport.NodeStatus {
	now := time.Now().UTC()
	var systemFacts *remoteobservation.SystemFacts
	if a.remoteObservation != nil {
		facts := a.remoteObservation.LocalSystemFacts(ctx)
		systemFacts = &facts
	}
	return nodetransport.NodeStatus{
		HostID:    hostID,
		Name:      hostName,
		Reachable: true,
		Agent: model.AgentRuntime{
			Installed: true,
			Version:   agentAPIVersion,
			Health:    model.AgentHealthHealthy,
			Reachable: true,
		},
		Deployments: a.managedRuntimeInstances(ctx, hostID, hostName),
		Managed:     a.managedDeploymentStatusSnapshot(),
		System:      systemFacts,
		UpdatedAt:   now,
	}
}

func (a *App) managedRuntimeInstances(ctx context.Context, hostID, hostName string) []model.InstanceStatus {
	projects := a.managedProjectsSnapshot()
	out := []model.InstanceStatus{}
	service := a.runtimeStatusService()
	for _, project := range projects {
		resp := service.Snapshot(ctx, project)
		for _, env := range resp.Environments {
			for _, inst := range env.Instances {
				inst.NodeID = hostID
				inst.NodeName = hostName
				inst.IsLocal = false
				out = append(out, inst)
			}
		}
	}
	return out
}

func (a *App) managedProjectsSnapshot() []model.Project {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := []model.Project{}
	for _, project := range a.projects {
		if _, ok := a.managedProjectIDs[project.ID]; !ok {
			continue
		}
		out = append(out, project)
	}
	return out
}

func (a *App) managedDeploymentStatusSnapshot() *model.ManagedDeploymentStatus {
	a.mu.RLock()
	status := a.managedStatus
	a.mu.RUnlock()
	status.ActiveCollectorCount = remoteobservation.CountActiveCollectors(a.collector.List())
	if status.Collectors == nil {
		status.Collectors = []model.ManagedCollectorStatus{}
	}
	return &status
}
