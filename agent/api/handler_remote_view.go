// handler_remote_view.go 实现 GET /api/remote/view:
// 聚合 Host 和 LogSource 数据为前端 Sidebar 友好的形态。
//
// 职责：
//   - 列出所有 Host(含 tags)
//   - 列出所有 LogSource,对每个 LogSource 计算 tag 分组("all" + 关联 Host 的 tags 并集)
//
// 边界：
//   - 不返回日志数据
//   - 不返回隧道端口(由 /api/tunnels 提供);前端组合两个接口数据使用
package api

import (
	"net/http"
	"sort"

	"github.com/superdev/agent/model"
)

type remoteViewGroup struct {
	Tag     string   `json:"tag"`
	HostIDs []string `json:"host_ids"`
}

type remoteViewLogSource struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Type   model.LogSourceType `json:"type"`
	Groups []remoteViewGroup   `json:"groups"`
}

type remoteViewResponse struct {
	LogSources []remoteViewLogSource `json:"log_sources"`
	Hosts      []model.Host          `json:"hosts"`
}

// remoteView 处理 GET /api/remote/view。
func (a *App) remoteView(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hosts == nil {
		hosts = []model.Host{}
	}
	if logSources == nil {
		logSources = []model.LogSource{}
	}

	hostByID := make(map[string]model.Host, len(hosts))
	for _, h := range hosts {
		hostByID[h.ID] = h
	}

	out := make([]remoteViewLogSource, 0, len(logSources))
	for _, ls := range logSources {
		out = append(out, remoteViewLogSource{
			ID:     ls.ID,
			Name:   ls.Name,
			Type:   ls.Type,
			Groups: buildGroups(ls.HostIDs, hostByID),
		})
	}

	jsonOK(w, remoteViewResponse{LogSources: out, Hosts: hosts})
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

	groups := []remoteViewGroup{{Tag: "all", HostIDs: allHosts}}
	for _, tag := range tagNames {
		groups = append(groups, remoteViewGroup{Tag: tag, HostIDs: tagToHosts[tag]})
	}
	return groups
}
