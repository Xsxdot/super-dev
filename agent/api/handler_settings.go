// handler_settings.go 实现 agent 级设置 HTTP 接口。
//
// 职责：
//   - 返回当前 agent 设置
//   - 校验并持久化设置更新
//
// 边界：
//   - 不处理项目级配置
//   - 不直接渲染客户端设置页
package api

import (
	"encoding/json"
	"net/http"

	"github.com/superdev/agent/config"
)

// getSettings 处理 GET /api/settings。
func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.settings.Load()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load settings: "+err.Error())
		return
	}
	jsonOK(w, settings)
}

// putSettings 处理 PUT /api/settings。
func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.AgentSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := a.settings.Save(settings); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, settings)
}
