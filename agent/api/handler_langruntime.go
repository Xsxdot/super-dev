// handler_langruntime.go 暴露 Language Runtime Provider 的 HTTP 只读契约。
//
// 职责：
//   - 返回已注册的语言运行 provider 列表
//   - 返回指定语言的 runtime schema，供前端表单和 MCP 工具消费
//
// 边界：
//   - 不持久化配置
//   - 不启动或停止进程
//   - 不直接编排调试会话
package api

import (
	"net/http"

	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

// listLanguageRuntimeProviders 处理 GET /api/language-runtime/providers。
//
// 返回：
//   - languages: 当前进程内已注册的语言 provider 标识列表
func (a *App) listLanguageRuntimeProviders(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"languages": langruntime.Core().Languages()})
}

// describeLanguageRuntimeSchema 处理 GET /api/language-runtime/{language}/schema。
//
// 参数：
//   - language: 服务语言标识，例如 go
//
// 返回：
//   - RuntimeSchema: provider 对外暴露的 schema 契约
//
// 注意：
//   - 未知语言返回 404，避免前端或 AI 静默按错误 schema 填配置。
func (a *App) describeLanguageRuntimeSchema(w http.ResponseWriter, r *http.Request) {
	language := model.ServiceLanguage(r.PathValue("language"))
	provider, ok := langruntime.Core().Provider(language)
	if !ok {
		jsonError(w, http.StatusNotFound, "unknown language runtime provider")
		return
	}
	jsonOK(w, provider.RuntimeSchema(r.Context()))
}

// suggestServiceRuntime 处理 POST /api/language-runtime/{language}/suggest。
//
// 参数：
//   - project_root: 项目根目录
//   - cwd: 服务运行工作目录，可为空表示项目根
//
// 返回：
//   - suggestions: provider 根据项目结构给出的候选 runtime 配置
func (a *App) suggestServiceRuntime(w http.ResponseWriter, r *http.Request) {
	provider, ok := languageRuntimeProviderFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectRoot string `json:"project_root"`
		CWD         string `json:"cwd"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	suggestions, err := provider.SuggestConfig(r.Context(), langruntime.RuntimeConfigInput{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{"suggestions": suggestions})
}

// validateServiceRuntime 处理 POST /api/language-runtime/{language}/validate。
//
// 参数：
//   - project_root/cwd/env/config: 语言运行配置原始输入
//
// 返回：
//   - valid: 是否不存在 error 级 diagnostics
//   - diagnostics: provider 返回的配置诊断
func (a *App) validateServiceRuntime(w http.ResponseWriter, r *http.Request) {
	provider, ok := languageRuntimeProviderFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectRoot string            `json:"project_root"`
		CWD         string            `json:"cwd"`
		Env         map[string]string `json:"env"`
		Config      map[string]any    `json:"config"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	_, diagnostics, err := provider.Normalize(r.Context(), langruntime.RuntimeConfigInput{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		Env:         req.Env,
		Config:      req.Config,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{
		"valid":       !langruntime.HasErrorDiagnostic(diagnostics),
		"diagnostics": diagnostics,
	})
}

// previewServiceExecution 处理 POST /api/language-runtime/{language}/preview。
//
// 参数：
//   - project_root/cwd/env/config: 语言运行配置原始输入
//   - intent: 执行意图，空值默认 start_dev
//   - artifact_dir: start_dev/start_normal 需要的预览产物目录
//
// 返回：
//   - preview: 可读命令预览
//   - diagnostics: provider 返回的配置或计划诊断
func (a *App) previewServiceExecution(w http.ResponseWriter, r *http.Request) {
	provider, ok := languageRuntimeProviderFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectRoot string            `json:"project_root"`
		CWD         string            `json:"cwd"`
		Env         map[string]string `json:"env"`
		Config      map[string]any    `json:"config"`
		Intent      string            `json:"intent"`
		ArtifactDir string            `json:"artifact_dir"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normalized, diagnostics, err := provider.Normalize(r.Context(), langruntime.RuntimeConfigInput{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		Env:         req.Env,
		Config:      req.Config,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if langruntime.HasErrorDiagnostic(diagnostics) {
		jsonOK(w, map[string]any{"diagnostics": diagnostics})
		return
	}
	intent := langruntime.BuildIntent(req.Intent)
	if intent == "" {
		intent = langruntime.IntentStartDev
	}
	plan, diagnostics, err := provider.BuildPlan(r.Context(), langruntime.BuildPlanInput{
		Intent:      intent,
		Config:      normalized,
		ArtifactDir: req.ArtifactDir,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{"preview": plan.Preview, "diagnostics": diagnostics})
}

func languageRuntimeProviderFromRequest(w http.ResponseWriter, r *http.Request) (langruntime.Provider, bool) {
	language := model.ServiceLanguage(r.PathValue("language"))
	provider, ok := langruntime.Core().Provider(language)
	if !ok {
		jsonError(w, http.StatusNotFound, "unknown language runtime provider")
		return nil, false
	}
	return provider, true
}
