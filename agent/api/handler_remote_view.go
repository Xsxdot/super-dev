// handler_remote_view.go 实现 GET /api/remote/view:
// 按 log_source_id 聚合单个 LogSource 的分组信息和关联 Host 列表。
//
// 职责：
//   - 接受 ?log_source_id 参数,返回指定 LogSource
//   - 计算 tag 分组("all" + 关联 Host 的 tags 并集)
//   - 返回关联 Host 列表
//
// 边界：
//   - 不返回日志数据
//   - 不返回隧道端口(由 /api/tunnels 提供)
package api

import (
	"net/http"
	"sort"

	"github.com/xsxdot/super-dev/agent/model"
)

// hostDTO 是 Host 的对外视图。
//
// 本应用完全运行在本机，Host 设置页需要完整凭据来回填编辑表单；
// 具体展示层负责不把密码和私钥明文渲染到列表中。
type hostDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	SSHPassword     string   `json:"ssh_password,omitempty"`
	SSHKeyPath      string   `json:"ssh_key_path,omitempty"`
	SSHPrivateKey   string   `json:"ssh_private_key,omitempty"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	PublicIP        string   `json:"public_ip,omitempty"`
	PrivateIP       string   `json:"private_ip,omitempty"`
	Tags            []string `json:"tags"`
	// IsSelf 为 true 表示该条目代表本机，不可删除。远端 host 为 false。
	IsSelf bool `json:"is_self"`
	// NodeID 仅本机节点携带，是 identity 的 node_id。
	NodeID string `json:"node_id,omitempty"`
}

func toHostDTO(h model.Host) hostDTO {
	dto := hostDTO{
		ID:        h.ID,
		Name:      h.Name,
		PublicIP:  h.PublicIP,
		PrivateIP: h.PrivateIP,
		Tags:      h.Tags,
	}
	if tunnelParams, ok := h.TunnelParams(); ok {
		dto.SSHHost = tunnelParams.SSHHost
		dto.SSHPort = tunnelParams.SSHPort
		dto.SSHUser = tunnelParams.SSHUser
		dto.SSHPassword = tunnelParams.SSHPassword
		dto.SSHKeyPath = tunnelParams.SSHKeyPath
		dto.SSHPrivateKey = tunnelParams.SSHPrivateKey
		dto.RemoteAgentPort = tunnelParams.RemoteAgentPort
	}
	dto.LocalTunnelPort = h.RuntimeLocalPort()
	return dto
}

func hostFromDTO(dto hostDTO) model.Host {
	h := model.Host{
		ID:        dto.ID,
		Name:      dto.Name,
		PublicIP:  dto.PublicIP,
		PrivateIP: dto.PrivateIP,
		Tags:      dto.Tags,
	}
	tunnelParams := h.EnsureTunnelAgent()
	tunnelParams.SSHHost = dto.SSHHost
	tunnelParams.SSHPort = dto.SSHPort
	tunnelParams.SSHUser = dto.SSHUser
	tunnelParams.SSHPassword = dto.SSHPassword
	tunnelParams.SSHKeyPath = dto.SSHKeyPath
	tunnelParams.SSHPrivateKey = dto.SSHPrivateKey
	tunnelParams.RemoteAgentPort = dto.RemoteAgentPort
	h.SetRuntimeLocalPort(dto.LocalTunnelPort)
	return h
}

type remoteViewGroup struct {
	GroupKey string   `json:"group_key"`
	HostIDs  []string `json:"host_ids"`
}

type logSourceDTO struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Type      model.LogSourceType `json:"type"`
	HostIDs   []string            `json:"host_ids"`
	ProjectID string              `json:"project_id,omitempty"`
	ServiceID string              `json:"service_id,omitempty"`
}

type remoteViewResponse struct {
	LogSource logSourceDTO      `json:"log_source"`
	Groups    []remoteViewGroup `json:"groups"`
	Hosts     []hostDTO         `json:"hosts"`
}

// remoteView 处理 GET /api/remote/view?log_source_id=xxx。
func (a *App) remoteView(w http.ResponseWriter, r *http.Request) {
	logSourceID := r.URL.Query().Get("log_source_id")
	if logSourceID == "" {
		jsonError(w, http.StatusBadRequest, "log_source_id is required")
		return
	}

	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var ls *model.LogSource
	for i := range logSources {
		if logSources[i].ID == logSourceID {
			ls = &logSources[i]
			break
		}
	}
	if ls == nil {
		jsonError(w, http.StatusNotFound, "log source not found")
		return
	}

	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hosts == nil {
		hosts = []model.Host{}
	}

	hostByID := make(map[string]model.Host, len(hosts))
	for _, h := range hosts {
		hostByID[h.ID] = h
	}

	// 只返回 LogSource 关联的 Host
	relatedHosts := make([]hostDTO, 0, len(ls.HostIDs))
	for _, hid := range ls.HostIDs {
		if h, ok := hostByID[hid]; ok {
			relatedHosts = append(relatedHosts, toHostDTO(h))
		}
	}

	jsonOK(w, remoteViewResponse{
		LogSource: logSourceDTO{
			ID:        ls.ID,
			Name:      ls.Name,
			Type:      ls.Type,
			HostIDs:   ls.HostIDs,
			ProjectID: ls.ProjectID,
			ServiceID: ls.ServiceID,
		},
		Groups: buildGroups(ls.HostIDs, hostByID, ls.Tags),
		Hosts:  relatedHosts,
	})
}

// buildGroups 根据 LogSource 自身的 tags 生成分组列表。
//
// "all" 组始终存在且包含所有关联 Host;
// 其余分组按 LogSource.Tags 生成,每个 tag 对应一个分组,包含全部关联 Host。
// LogSource.Tags 与 Host.Tags 无关,仅作为监听任务的子视图分类。
func buildGroups(hostIDs []string, hostByID map[string]model.Host, logSourceTags []string) []remoteViewGroup {
	allHosts := make([]string, 0, len(hostIDs))
	for _, hid := range hostIDs {
		if _, ok := hostByID[hid]; ok {
			allHosts = append(allHosts, hid)
		}
	}

	tagNames := make([]string, len(logSourceTags))
	copy(tagNames, logSourceTags)
	sort.Strings(tagNames)

	groups := []remoteViewGroup{{GroupKey: "all", HostIDs: allHosts}}
	for _, tag := range tagNames {
		groups = append(groups, remoteViewGroup{GroupKey: tag, HostIDs: allHosts})
	}
	return groups
}
