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

	"github.com/xsxdot/super-dev/agent/config"
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
	if req.Approval != nil {
		current.Approval = mergeApprovalPolicyPatch(current.Approval, *req.Approval)
	}
	if req.DebugBrowser != nil {
		current.DebugBrowser = mergeDebugBrowserSettingsPatch(current.DebugBrowser, *req.DebugBrowser)
	}
	if err := a.settings.Save(current); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, current)
}

type settingsPatchRequest struct {
	LogRetentionDays          *int                       `json:"log_retention_days"`
	LogMaxBytes               *int64                     `json:"log_max_bytes"`
	LogCleanupIntervalSeconds *int                       `json:"log_cleanup_interval_seconds"`
	OnboardingCompleted       *bool                      `json:"onboarding_completed"`
	Approval                  *approvalPolicyPatch       `json:"approval"`
	DebugBrowser              *debugBrowserSettingsPatch `json:"debug_browser"`
}

type approvalPolicyPatch struct {
	ConfigUpsert      *bool `json:"config_upsert"`
	PipelineUpsert    *bool `json:"pipeline_upsert"`
	PipelineRun       *bool `json:"pipeline_run"`
	TemplateImport    *bool `json:"template_import"`
	BrowserDebugOpen  *bool `json:"browser_debug_open"`
	CodeDebugOpen     *bool `json:"code_debug_open"`
	CodeDebugEvaluate *bool `json:"code_debug_evaluate"`
	GraceMinutes      *int  `json:"grace_minutes"`
}

type debugBrowserSettingsPatch struct {
	DefaultBrowserID  *string                      `json:"default_browser_id"`
	ProfileMode       *string                      `json:"profile_mode"`
	AllowEvaluate     *bool                        `json:"allow_evaluate"`
	SessionTTLMinutes *int                         `json:"session_ttl_minutes"`
	Browsers          *[]config.DebugBrowserConfig `json:"browsers"`
}

func mergeApprovalPolicyPatch(current config.ApprovalPolicy, patch approvalPolicyPatch) config.ApprovalPolicy {
	if patch.ConfigUpsert != nil {
		current.ConfigUpsert = *patch.ConfigUpsert
	}
	if patch.PipelineUpsert != nil {
		current.PipelineUpsert = *patch.PipelineUpsert
	}
	if patch.PipelineRun != nil {
		current.PipelineRun = *patch.PipelineRun
	}
	if patch.TemplateImport != nil {
		current.TemplateImport = *patch.TemplateImport
	}
	if patch.BrowserDebugOpen != nil {
		current.BrowserDebugOpen = *patch.BrowserDebugOpen
	}
	if patch.CodeDebugOpen != nil {
		current.CodeDebugOpen = *patch.CodeDebugOpen
	}
	if patch.CodeDebugEvaluate != nil {
		current.CodeDebugEvaluate = *patch.CodeDebugEvaluate
	}
	if patch.GraceMinutes != nil {
		current.GraceMinutes = *patch.GraceMinutes
	}
	return current
}

func mergeDebugBrowserSettingsPatch(current config.DebugBrowserSettings, patch debugBrowserSettingsPatch) config.DebugBrowserSettings {
	if patch.DefaultBrowserID != nil {
		current.DefaultBrowserID = *patch.DefaultBrowserID
	}
	if patch.ProfileMode != nil {
		current.ProfileMode = *patch.ProfileMode
	}
	if patch.AllowEvaluate != nil {
		current.AllowEvaluate = *patch.AllowEvaluate
	}
	if patch.SessionTTLMinutes != nil {
		current.SessionTTLMinutes = *patch.SessionTTLMinutes
	}
	if patch.Browsers != nil {
		current.Browsers = *patch.Browsers
	}
	return current
}
