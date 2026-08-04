// handler_agent_adoption_proxy.go 把桌面端的纳管三请求经本机 agent 代理到目标机。
//
// 职责：
//   - POST /api/agents/{host_id}/adoption-requests → 目标机 POST /api/security/adoption-requests
//   - GET  /api/agents/{host_id}/adoption-requests/{id} → 目标机同路径 GET
//   - POST /api/agents/{host_id}/adoption-requests/{id}/exchange → 目标机同路径 POST
//
// 为什么必须走代理而不是桌面 webview 直连目标机：
//   - 目标机默认（tls_mode=auto）被 provision 成自签 HTTPS 监听，webview 的
//     裸 fetch 既不能跳过证书校验、也没有对方 CA，硬编码 http:// 更是必然
//     连不上——直连只对 tls_mode=off 的目标可用，而那不是默认姿态
//   - 本机 agent 经 nodetransport 按 host_id 寻址，天然复用用户已配置的连接链
//     （direct 与 SSH tunnel 皆可；裸 fetch 只支持 direct 且要靠猜地址），
//     并用 doAgentRequestSchemeAware 吸收目标 TLS 姿态未知的问题
//
// 边界：
//   - 三个端点都在 withSecurity 之后（调用方是已认证的本机桌面），绝不进
//     bypass 白名单；匿名的是目标机侧的对应端点，不是这里
//   - 响应原样透传（状态码 + body）：目标机的稳定错误码（429 限流、404 过期、
//     409 状态冲突等）对桌面端有语义，代理不做二次解释
//   - exchange 请求体里的一次性 adoption token 只在内存中转，绝不落日志
//   - 已知取舍：经代理后目标机 requestOriginLabel 记录的是本控制面机器的出口
//     地址（tunnel 链路下是目标机自己的回环地址），与「请求来自哪个控制面机器」
//     的语义一致；配对码不受影响（由请求 ID 派生）
package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// adoptionProxyTimeout 是单次纳管代理转发的总预算。
//
// 取 10 秒：纳管是交互式向导路径（发起/2s 轮询/兑换），预算需覆盖「SSH 隧道
// 冷启动 + HTTPS 握手（可能先失败一次再退明文）+ 目标机处理」的全价；比安装
// 守卫探测（5s）更宽，因为这里用户已明确处于等待流程中，宁可慢不可假失败。
const adoptionProxyTimeout = 10 * time.Second

// maxAdoptionProxyResponseBytes 限制透传响应体的读取上限。
// 目标机的合法响应都是小 JSON；上限防的是「目标机不是 agent 而是别的什么服务」
// 时把任意大响应灌进内存。
const maxAdoptionProxyResponseBytes = 64 << 10

// proxyAdoptionCreate 处理 POST /api/agents/{host_id}/adoption-requests。
func (a *App) proxyAdoptionCreate(w http.ResponseWriter, r *http.Request) {
	a.proxyAdoptionRequest(w, r, http.MethodPost, "/api/security/adoption-requests", true)
}

// proxyAdoptionStatus 处理 GET /api/agents/{host_id}/adoption-requests/{id}。
func (a *App) proxyAdoptionStatus(w http.ResponseWriter, r *http.Request) {
	a.proxyAdoptionRequest(w, r, http.MethodGet, "/api/security/adoption-requests/"+url.PathEscape(r.PathValue("id")), false)
}

// proxyAdoptionExchange 处理 POST /api/agents/{host_id}/adoption-requests/{id}/exchange。
func (a *App) proxyAdoptionExchange(w http.ResponseWriter, r *http.Request) {
	a.proxyAdoptionRequest(w, r, http.MethodPost, "/api/security/adoption-requests/"+url.PathEscape(r.PathValue("id"))+"/exchange", true)
}

// proxyAdoptionRequest 是三个纳管代理端点的公共转发实现。
//
// 参数：
//   - method/targetPath: 目标机匿名端点的请求要素
//   - withBody: true 时读取调用方请求体（上限 maxAdoptionRequestBytes，与目标机
//     侧同一常量——代理不该比目标更宽容）原样转发
//
// 注意：
//   - 转发失败返回 502 adoption_target_unreachable；错误消息来自传输层
//     （地址/超时类信息），不含请求体内容，adoption token 不会经错误路径泄漏
func (a *App) proxyAdoptionRequest(w http.ResponseWriter, r *http.Request, method, targetPath string, withBody bool) {
	hostID := r.PathValue("host_id")
	_, _, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	var body []byte
	if withBody {
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdoptionRequestBytes))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), adoptionProxyTimeout)
	defer cancel()
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	resp, scheme, _, err := a.doAgentRequestSchemeAware(ctx, hostID, method, targetPath, headers, body)
	if err != nil {
		// 错误里只有传输层信息（host/超时/握手），没有请求体内容。
		log.Printf("[SuperDev] 纳管代理转发失败 host=%s %s %s：%v", hostID, method, targetPath, err)
		jsonWrite(w, http.StatusBadGateway, map[string]string{
			"code":  "adoption_target_unreachable",
			"error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxAdoptionProxyResponseBytes))
	if err != nil {
		log.Printf("[SuperDev] 纳管代理读取目标响应失败 host=%s %s %s：%v", hostID, method, targetPath, err)
		jsonWrite(w, http.StatusBadGateway, map[string]string{
			"code":  "adoption_target_unreachable",
			"error": "failed to read target response",
		})
		return
	}
	log.Printf("[SuperDev] 纳管代理转发完成 host=%s %s %s scheme=%s status=%d", hostID, method, targetPath, scheme, resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}
