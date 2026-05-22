// handler_hosts.go 实现 Host CRUD HTTP 接口。
//
// 职责：
//   - 列出/创建/更新/删除 Host
//   - 所有响应使用 application/json
//
// 边界：
//   - 不直接管理隧道,只持久化元数据;隧道由 tunnel.Manager 在使用时按需建立
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// listHosts 处理 GET /api/hosts,返回不含 SSH 凭据的安全视图。
func (a *App) listHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]hostDTO, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, toHostDTO(h))
	}
	jsonOK(w, out)
}

// createHost 处理 POST /api/hosts,body 为 model.Host。
func (a *App) createHost(w http.ResponseWriter, r *http.Request) {
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, err := a.remoteStore.AddHost(h)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

// updateHost 处理 PUT /api/hosts/{id}。
func (a *App) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.ID = id
	if err := a.remoteStore.UpdateHost(h); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, h)
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveHost(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
