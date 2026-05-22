// handler_log_sources.go 实现 LogSource CRUD HTTP 接口。
//
// 职责：与 handler_hosts.go 镜像,负责 LogSource 资源。
// 边界：不直接启动远端采集任务,只持久化"我想监听什么"的元数据。
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// listLogSources 处理 GET /api/log-sources。
func (a *App) listLogSources(w http.ResponseWriter, r *http.Request) {
	list, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []model.LogSource{}
	}
	jsonOK(w, list)
}

// createLogSource 处理 POST /api/log-sources。
func (a *App) createLogSource(w http.ResponseWriter, r *http.Request) {
	var ls model.LogSource
	if err := json.NewDecoder(r.Body).Decode(&ls); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !ls.Type.IsValid() {
		jsonError(w, http.StatusBadRequest, "unsupported log source type")
		return
	}
	saved, err := a.remoteStore.AddLogSource(ls)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

// updateLogSource 处理 PUT /api/log-sources/{id}。
func (a *App) updateLogSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ls model.LogSource
	if err := json.NewDecoder(r.Body).Decode(&ls); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ls.ID = id
	if !ls.Type.IsValid() {
		jsonError(w, http.StatusBadRequest, "unsupported log source type")
		return
	}
	if err := a.remoteStore.UpdateLogSource(ls); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "log source not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, ls)
}

// deleteLogSource 处理 DELETE /api/log-sources/{id}。
func (a *App) deleteLogSource(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveLogSource(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
