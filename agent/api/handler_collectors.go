// handler_collectors.go 实现远端 collector 的 HTTP 接口。
//
// 职责：
//   - POST   /api/collectors:按 (name, type) 启动采集
//   - DELETE /api/collectors/{id}:停止采集
//   - GET    /api/collectors:列出当前活跃 collector
//
// 边界：
//   - 不做命令校验,业务逻辑在 collector.Manager 内
//   - 错误码:400 = 参数非法;404 = 目标不存在;500 = 内部错误
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

// startCollector 处理 POST /api/collectors,body: {name, type}。
func (a *App) startCollector(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string              `json:"name"`
		Type model.LogSourceType `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := a.collector.Start(req.Name, req.Type)
	switch {
	case errors.Is(err, collector.ErrInvalidName), errors.Is(err, collector.ErrUnsupportedType):
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, collector.ErrTargetNotFound):
		jsonError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	col, _ := a.collector.Get(id)
	jsonOK(w, col)
}

// stopCollector 处理 DELETE /api/collectors/{id}。
func (a *App) stopCollector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.collector.Stop(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "stopped"})
}

// listCollectors 处理 GET /api/collectors。
func (a *App) listCollectors(w http.ResponseWriter, r *http.Request) {
	list := a.collector.List()
	if list == nil {
		list = []model.Collector{}
	}
	jsonOK(w, list)
}
