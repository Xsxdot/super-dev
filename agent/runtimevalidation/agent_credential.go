// agent_credential.go 为验证框架里未经 packaged MCP、直连 disposable Agent 的 HTTP
// 调用附加本机访问 token。
//
// 职责：
//   - 从 disposable Agent 的数据目录（campaign.go 里的 cloneRoot）读取
//     local-access-token（agent/security.ReadLocalToken）
//   - 给这些直连请求设置 Authorization: Bearer <token>
//
// 边界：
//   - 不生成或轮换 token——token 由 agent 自身的 security.RotateLocalToken 在启动时完成，
//     这里只读
//   - 不覆盖调用方已经显式设置的 Authorization 头（例如 credential.go 里 auth sidecar
//     登录用的是人工输入的调试凭据，跟 agent 的本机 token 是两套完全不同的身份）
//   - 不判定「要不要带」——那是每个直连调用点的职责（纯探活换 bypass 端点，
//     读受保护数据才带 token）；本文件只提供「带」这一步的机制
package runtimevalidation

import (
	"net/http"
	"strings"
)

// attachAgentToken 在 token 非空时给直连 disposable Agent 的请求设置 Authorization。
//
// 参数：
//   - request: 尚未发出的 HTTP 请求
//   - token: security.ReadLocalToken 读到的本机访问 token；空串表示调用方未解析到凭据
//
// 注意：token 为空时请求保持裸发——多见于单测里的假 server，不在这里静默兜底；
// 真实 disposable Agent 收到裸请求会用 401 自然暴露问题。调用方永远不应把 token
// 值写入日志或错误文本。
func attachAgentToken(request *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
}
