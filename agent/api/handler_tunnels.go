// handler_tunnels.go 实现隧道状态查询、主动连接/断开,以及状态变化 WebSocket 推送。
//
// 职责：
//   - GET /api/tunnels:返回所有 Host 的隧道状态快照(含本地端口)
//   - POST /api/tunnels/{host_id}:按 host 凭据建立隧道
//   - DELETE /api/tunnels/{host_id}:主动断开
//   - GET /ws/tunnels:订阅状态变化事件流
//
// 边界：
//   - 不修改 Host 凭据等元数据;仅在首次随机端口成功后写回 LocalTunnelPort 便于复用
//   - 隧道空闲超时暂未实现;断开依赖前端 disconnect 或 agent 退出
package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/superdev/agent/tunnel"
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
	HostID    string `json:"host_id"`
	State     string `json:"state"`
	LocalPort int    `json:"local_port"`
	Error     string `json:"error,omitempty"`
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
		out = append(out, tunnelStatusDTO{
			HostID:    h.ID,
			State:     tunnelStateLabel(st),
			LocalPort: a.tunnels.LocalPort(h.ID),
			Error:     a.tunnels.ErrorOf(h.ID),
		})
	}
	jsonOK(w, out)
}

// connectTunnel 处理 POST /api/tunnels/{host_id}。
func (a *App) connectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, h := range hosts {
		if h.ID != hostID {
			continue
		}
		port, err := a.tunnels.EnsureConnected(h)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		if h.LocalTunnelPort == 0 && port != 0 {
			h.LocalTunnelPort = port
			_ = a.remoteStore.UpdateHost(h)
		}
		jsonOK(w, tunnelStatusDTO{HostID: hostID, State: "open", LocalPort: port})
		return
	}
	jsonError(w, http.StatusNotFound, "host not found")
}

// disconnectTunnel 处理 DELETE /api/tunnels/{host_id}。
func (a *App) disconnectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	a.tunnels.Disconnect(hostID)
	jsonOK(w, map[string]string{"status": "disconnected"})
}

// wsTunnels 处理 GET /ws/tunnels,推送状态变化事件。
func (a *App) wsTunnels(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	subID := uuid.NewString()
	ch := a.tunnels.Subscribe(subID)
	defer a.tunnels.Unsubscribe(subID)
	ctx := r.Context()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			dto := tunnelStatusDTO{
				HostID:    ev.HostID,
				State:     tunnelStateLabel(ev.Status),
				LocalPort: a.tunnels.LocalPort(ev.HostID),
				Error:     ev.Err,
			}
			if err := conn.WriteJSON(dto); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
