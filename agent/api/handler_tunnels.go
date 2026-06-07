// handler_tunnels.go 实现隧道状态查询、主动连接/断开,以及状态变化 WebSocket 推送。
//
// 职责：
//   - GET /api/tunnels:返回所有 Host 的隧道状态快照(含本地端口)
//   - POST /api/tunnels/{host_id}:按 host 凭据建立隧道
//   - DELETE /api/tunnels/{host_id}:主动断开
//   - GET /ws/tunnels:订阅状态变化事件流
//
// 边界：
//   - 不修改 Host 凭据等元数据;本地端口属于运行时状态,不写回 hosts.json
//   - 隧道空闲超时暂未实现;断开依赖前端 disconnect 或 agent 退出
package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// tunnelStateLabel 将内部 tunnel.Status 映射到前端 TunnelState 枚举。
// 前端枚举：idle | connecting | open | failed | closed
func tunnelStateLabel(s tunnel.Status) string {
	switch s {
	case tunnel.StatusConnected:
		return "open"
	case tunnel.StatusConnecting:
		return "connecting"
	case tunnel.StatusFailed:
		return "failed"
	default:
		return "idle"
	}
}

type tunnelStatusDTO struct {
	HostID         string `json:"host_id"`
	State          string `json:"state,omitempty"`
	LocalPort      int    `json:"local_port,omitempty"`
	Error          string `json:"error,omitempty"`
	Agent          string `json:"agent,omitempty"`
	AgentVersion   string `json:"agent_version,omitempty"`
	AgentCheckedAt string `json:"agent_checked_at,omitempty"`
}

func agentInfoDTO(info agenthealth.Info) (string, string, string) {
	checkedAt := ""
	if !info.CheckedAt.IsZero() {
		checkedAt = info.CheckedAt.Format(time.RFC3339)
	}
	return string(info.Status), info.Version, checkedAt
}

// listTunnels 处理 GET /api/tunnels。
func (a *App) listTunnels(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]tunnelStatusDTO, 0, len(hosts))
	for _, h := range hosts {
		st := a.tunnels.Status(h.ID)
		if st == tunnel.StatusDisconnected {
			continue
		}
		agentStatus, agentVersion, agentCheckedAt := agentInfoDTO(a.agentHealth.Info(h.ID))
		out = append(out, tunnelStatusDTO{
			HostID:         h.ID,
			State:          tunnelStateLabel(st),
			LocalPort:      a.tunnels.LocalPort(h.ID),
			Error:          a.tunnels.ErrorOf(h.ID),
			Agent:          agentStatus,
			AgentVersion:   agentVersion,
			AgentCheckedAt: agentCheckedAt,
		})
	}
	jsonOK(w, out)
}

// connectTunnel 处理 POST /api/tunnels/{host_id}。
func (a *App) connectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	port, err := a.tunnels.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(nodetransport.NodeTarget{Host: host, Agent: agent}))
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, tunnelStatusDTO{HostID: hostID, State: "open", LocalPort: port})
}

// disconnectTunnel 处理 DELETE /api/tunnels/{host_id}。
func (a *App) disconnectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	a.tunnels.Disconnect(hostID)
	jsonOK(w, map[string]string{"status": "disconnected"})
}

// wsTunnels 处理 GET /ws/tunnels,推送隧道状态与 agent 健康变化事件。
func (a *App) wsTunnels(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	subID := uuid.NewString()
	tunCh := a.tunnels.Subscribe(subID)
	defer a.tunnels.Unsubscribe(subID)
	agentCh := a.agentHealth.Subscribe(subID)
	defer a.agentHealth.Unsubscribe(subID)

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-tunCh:
			if !ok {
				return
			}
			agentStatus, agentVersion, agentCheckedAt := agentInfoDTO(a.agentHealth.Info(ev.HostID))
			dto := tunnelStatusDTO{
				HostID:         ev.HostID,
				State:          tunnelStateLabel(ev.Status),
				LocalPort:      a.tunnels.LocalPort(ev.HostID),
				Error:          ev.Err,
				Agent:          agentStatus,
				AgentVersion:   agentVersion,
				AgentCheckedAt: agentCheckedAt,
			}
			if err := conn.WriteJSON(dto); err != nil {
				return
			}
		case ev, ok := <-agentCh:
			if !ok {
				return
			}
			// agent 部分更新：只带 host_id + agent 元信息，靠前端 merge 保留隧道字段。
			dto := tunnelStatusDTO{
				HostID:         ev.HostID,
				Agent:          string(ev.Status),
				AgentVersion:   ev.Version,
				AgentCheckedAt: ev.CheckedAt,
			}
			if err := conn.WriteJSON(dto); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
