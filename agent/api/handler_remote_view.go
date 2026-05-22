// handler_remote_view.go 实现 GET /api/remote/view:
// 按 log_source_id 聚合单个 LogSource 的分组信息和关联 Host 列表。
//
// 职责：
//   - 接受 ?log_source_id 参数,返回指定 LogSource
//   - 计算 tag 分组("all" + 关联 Host 的 tags 并集)
//   - 返回关联 Host 列表(不含 SSH 密码等敏感字段)
//
// 边界：
//   - 不返回日志数据
//   - 不返回隧道端口(由 /api/tunnels 提供)
package api

import (
	"net/http"
	"sort"

	"github.com/superdev/agent/model"
)

// hostDTO 是 Host 的对外安全视图,不含 SSH 密码和密钥路径。
type hostDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	Tags            []string `json:"tags"`
}

func toHostDTO(h model.Host) hostDTO {
	return hostDTO{
		ID:              h.ID,
		Name:            h.Name,
		SSHHost:         h.SSHHost,
		SSHPort:         h.SSHPort,
		SSHUser:         h.SSHUser,
		RemoteAgentPort: h.RemoteAgentPort,
		LocalTunnelPort: h.LocalTunnelPort,
		Tags:            h.Tags,
	}
}

type remoteViewGroup struct {
	GroupKey string   `json:"group_key"`
	HostIDs  []string `json:"host_ids"`
}

type logSourceDTO struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Type    model.LogSourceType `json:"type"`
	HostIDs []string            `json:"host_ids"`
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
		LogSource: logSourceDTO{ID: ls.ID, Name: ls.Name, Type: ls.Type, HostIDs: ls.HostIDs},
		Groups:    buildGroups(ls.HostIDs, hostByID),
		Hosts:     relatedHosts,
	})
}

// buildGroups 根据 LogSource 关联的 Host 集合生成 tag 分组列表。
//
// "all" 组始终存在且包含所有关联 Host;
// 其余分组按 Host.Tags 并集生成,每个 tag 对应一个分组。
// 同一 Host 出现在它拥有的所有 tag 分组里。
func buildGroups(hostIDs []string, hostByID map[string]model.Host) []remoteViewGroup {
	allHosts := make([]string, 0, len(hostIDs))
	tagToHosts := map[string][]string{}
	for _, hid := range hostIDs {
		h, ok := hostByID[hid]
		if !ok {
			continue
		}
		allHosts = append(allHosts, hid)
		for _, tag := range h.Tags {
			tagToHosts[tag] = append(tagToHosts[tag], hid)
		}
	}
	tagNames := make([]string, 0, len(tagToHosts))
	for tag := range tagToHosts {
		tagNames = append(tagNames, tag)
	}
	sort.Strings(tagNames)

	groups := []remoteViewGroup{{GroupKey: "all", HostIDs: allHosts}}
	for _, tag := range tagNames {
		groups = append(groups, remoteViewGroup{GroupKey: tag, HostIDs: tagToHosts[tag]})
	}
	return groups
}
