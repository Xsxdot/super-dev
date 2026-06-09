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
	current, err := a.settings.Load()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load settings: "+err.Error())
		return
	}
	var req settingsPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.LogRetentionDays != nil {
		current.LogRetentionDays = *req.LogRetentionDays
	}
	if req.LogMaxBytes != nil {
		current.LogMaxBytes = *req.LogMaxBytes
	}
	if req.LogCleanupIntervalSeconds != nil {
		current.LogCleanupIntervalSeconds = *req.LogCleanupIntervalSeconds
	}
	if req.OnboardingCompleted != nil {
		current.OnboardingCompleted = *req.OnboardingCompleted
	}
	if err := a.settings.Save(current); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, current)
}

type settingsPatchRequest struct {
	LogRetentionDays          *int   `json:"log_retention_days"`
	LogMaxBytes               *int64 `json:"log_max_bytes"`
	LogCleanupIntervalSeconds *int   `json:"log_cleanup_interval_seconds"`
	OnboardingCompleted       *bool  `json:"onboarding_completed"`
}
