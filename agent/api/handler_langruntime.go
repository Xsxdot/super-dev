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
