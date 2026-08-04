// handler_agent_integrations_proxy.go 把桌面端 connector 远端安装场景的全部
// integrations 请求经本机 agent 代理到目标机。
//
// 职责：
//   - ANY /api/agents/{host_id}/integrations/{rest...} → 目标机同前缀端点
//     ANY /api/integrations/{rest}：detect（Task 3）与受限文件读写七端点
//     （Task 4）统一走这一条通用转发，不为每个端点各写一个 handler
//   - RawQuery 原样透传：stat/read/list/delete 四个端点全靠 ?path= 查询参数
//     寻址目标文件，代理层对查询字符串不做任何解析或重新编码
//   - 响应原样透传（状态码 + body）：目标机的白名单拒绝（403 path_not_allowed）、
//     内容超限（413）等业务错误对桌面端有语义，代理不做二次解释
//
// 为什么必须走代理而不是桌面 webview 直连目标机：
//   - 与 handler_agent_adoption_proxy.go 同一理由——目标机默认（tls_mode=auto）
//     被 provision 成自签 HTTPS，webview 裸 fetch 既不能跳过证书校验也没有对方
//     CA；本机 agent 经 nodetransport 按 host_id 寻址，天然复用用户已配置的
//     连接链（direct 与 SSH tunnel 皆可），并用 doAgentRequestSchemeAware 吸收
//     目标 TLS 姿态未知的问题
//
// 边界：
//   - 本代理路径在 withSecurity 之后（调用方是已认证的本机桌面），绝不进
//     bypass 白名单；目标机侧对应端点是否匿名可达是目标机自己的判定
//   - 本代理是哑管道：不解析、不校验被转发的 rest 段或请求体，白名单校验
//     （integrationPathAllowed / integrationDeleteAllowed）只在目标机 Task 4
//     handler 里执行一次，代理层重复校验只会造成两地维护、逻辑漂移
//   - 桌面侧的 Authorization 头（本机 token）绝不透传给目标机：转发请求头
//     由本 handler 从空白重新构造，不拷贝调用方任何请求头；目标机凭据由
//     nodetransport 按其自身 Agent Secret 独立注入（与纳管代理同一纪律）
//   - 转发失败（传输层：拨号/握手/超时）与目标机自身业务错误（4xx/5xx）是
//     两个不同故障域，绝不能混淆：前者返回 502 integration_target_unreachable，
//     后者原样透传目标机的状态码与响应体
package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"
)

// integrationsProxyTimeout 是单次 integrations 代理转发的总预算。
//
// 取 15 秒：write-batch 端点单批次内容上限 4MB（见 handler_integrations_fs.go
// integrationsFsWriteBatchMaxBytes），经 tunnel 链路的冷启动握手 + 传输耗时可能
// 明显长于纳管代理的小 JSON 请求（10 秒），故预算比纳管代理更宽。
const integrationsProxyTimeout = 15 * time.Second

// maxIntegrationsProxyRequestBytes 限制读取调用方请求体的上限。
//
// 与响应上限对称取 8MB：write-batch 目标端限制原始文件内容之和为 4MB，但请求体
// 是「JSON 元数据 + base64 编码内容」，base64 本身即带来约 1.33 倍膨胀，8MB 留出
// 充分余量；代理不做业务校验，这里的上限只防止把无关大流量灌入内存，真正的
// 4MB 业务上限由目标机 Task 4 handler 自己校验并原样透传 413。
const maxIntegrationsProxyRequestBytes = 8 << 20

// maxIntegrationsProxyResponseBytes 限制透传响应体的读取上限。
const maxIntegrationsProxyResponseBytes = 8 << 20

// proxyAgentIntegrations 处理 ANY /api/agents/{host_id}/integrations/{rest...}。
//
// 参数：
//   - r.PathValue("host_id"): 目标机在本机 agents.json 中的 host ID
//   - r.PathValue("rest"): 去掉 "/api/agents/{host_id}/integrations/" 前缀后的
//     剩余路径段（如 "detect"、"fs/stat"、"fs/write-batch"）
//
// 注意：
//   - 目标路径为 "/api/integrations/" + rest，RawQuery 非空时原样拼接在后面——
//     不经过 url.Values 解析再编码，避免百分号编码被规范化成不同但语义等价的
//     形式（虽然语义不变，但没必要引入这一步不确定性）
//   - method 与调用方请求一致地转发，不区分 GET/PUT/POST/DELETE
func (a *App) proxyAgentIntegrations(w http.ResponseWriter, r *http.Request) {
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

	targetPath := "/api/integrations/" + r.PathValue("rest")
	if q := r.URL.RawQuery; q != "" {
		targetPath += "?" + q
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIntegrationsProxyRequestBytes))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 空 body（GET/DELETE 等无请求体的方法）传 nil，而不是一个空 reader——
	// 与 doAgentRequestSchemeAware「nil 表示无 body」的既有约定一致，避免给
	// 无 body 的请求平白附上一个 Content-Length: 0 的 reader。
	var forwardBody []byte
	if len(body) > 0 {
		forwardBody = body
	}

	ctx, cancel := context.WithTimeout(r.Context(), integrationsProxyTimeout)
	defer cancel()
	// 从空白重新构造请求头：绝不拷贝调用方（桌面端）的 Authorization 等头部，
	// 见文件头注释「转发头纪律」。目标机的全部 integrations 端点都以 JSON
	// 交互，固定声明 Content-Type 与纳管代理一致。
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	resp, _, _, err := a.doAgentRequestSchemeAware(ctx, hostID, r.Method, targetPath, headers, forwardBody)
	if err != nil {
		// 传输层故障（拨号/握手/超时），与目标机业务错误是不同故障域，见文件头注释。
		// 错误信息只含传输层描述，不含请求体内容，不会泄漏被转发的文件路径/内容。
		log.Printf("[SuperDev] integrations: 代理转发失败 host=%s %s %s：%v", hostID, r.Method, targetPath, err)
		jsonCodeError(w, http.StatusBadGateway, "integration_target_unreachable", err.Error(), nil)
		return
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxIntegrationsProxyResponseBytes))
	if err != nil {
		log.Printf("[SuperDev] integrations: 代理读取目标响应失败 host=%s %s %s：%v", hostID, r.Method, targetPath, err)
		jsonCodeError(w, http.StatusBadGateway, "integration_target_unreachable", "failed to read target response", nil)
		return
	}
	// 正常转发不逐请求打日志：stat/list 会被桌面端 UI 状态刷新高频轮询，
	// 每请求一条日志会刷屏，故意不打印 scheme/status 等成功路径信息
	// （与 502 分支不同，那里的失败必须留痕）。
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}
