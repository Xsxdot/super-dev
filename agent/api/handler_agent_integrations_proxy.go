// handler_agent_integrations_proxy.go 把桌面端 connector 远端安装场景的全部
// integrations 请求经本机 agent 代理到目标机。
//
// 职责：
//   - ANY /api/agents/{host_id}/integrations/{rest...} → 目标机同前缀端点
//     ANY /api/integrations/{rest}：detect、受限文件读写七端点与受限命令执行
//     端点 exec 统一走这一条通用转发，不为每个端点各写一个 handler
//   - 按目标路径给出转发预算（integrationsProxyBudget）：exec 的预算必须大于
//     目标机命令自身的时限上限，否则内层超时语义整个失效——详见该常量注释
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
//   - 本代理是哑管道：不解析、不校验被转发的 rest 段或请求体的 integrations
//     业务语义，白名单校验（integrationPathAllowed / integrationDeleteAllowed）
//     只在目标机 Task 4 handler 里执行一次，代理层重复业务校验只会造成两地
//     维护、逻辑漂移。但路由完整性属于代理自己的职责——{rest...} 通配段经
//     mux 解码后可能含形如 %2E%2E 的百分号编码 dot segment，拼接后必须收敛
//     到 /api/integrations/ 前缀之内，否则调用方能借这条通配打到目标机
//     /api/integrations/ 之外的任意端点，还携带 nodetransport 为该 host 注入
//     的凭据；这不是「加 integrations 语义校验」，是代理不能被越权当跳板
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
	"path"
	"strings"
	"time"
)

// integrationsProxyBasePath 是本代理允许转发到的目标机路径前缀。
const integrationsProxyBasePath = "/api/integrations"

// integrationsProxyTimeout 是【文件类】单次 integrations 代理转发的总预算。
//
// 取 15 秒：write-batch 端点单批次内容上限 4MB（见 handler_integrations_fs.go
// integrationsFsWriteBatchMaxBytes），经 tunnel 链路的冷启动握手 + 传输耗时可能
// 明显长于纳管代理的小 JSON 请求（10 秒），故预算比纳管代理更宽。
//
// 文件类端点的耗时由**本代理这一跳**决定（读写多大内容、链路多慢），目标机
// handler 侧没有自己的等待时限，因此一个统一预算就够。exec 不是这样，见下。
const integrationsProxyTimeout = 15 * time.Second

// integrationsProxyExecPath 是 exec 端点在目标机上的收敛后路径。
const integrationsProxyExecPath = integrationsProxyBasePath + "/exec"

// integrationsProxyExecTimeout 是 exec 端点的转发预算。
//
// **必须严格大于目标机命令自身的时限上限**（integrations_exec_allowlist.go 的
// integrationsExecMaxTimeout = 60s）。exec 与文件类端点的根本差别是：目标机
// handler 自己有一套超时语义——它会杀掉超时的子进程并以 timed_out=true 正常
// 返回，桌面端据此把「命令跑太久」与「网络断了」区分开。
//
// 如果代理预算比命令时限小，这套语义整个失效，而且失效方式是最坏的那种：
// 代理先超时 → 本机 agent 回 502 integration_target_unreachable → 桌面端报错，
// **而目标机上那条 CLI 仍在跑，并且会把配置写完**。用户看到「装失败」，机器
// 其实已经装好了；下次刷新状态又变成已装。exec 是有副作用的调用，这种
// 「报告失败但实际生效」比单纯的慢要坏得多。
//
// 90s = 60s 命令上限 + 30s 转发余量（tunnel 冷启动握手、杀进程回收、响应回程）。
// 桌面端 remote_command.rs 的 HTTP 超时必须再大于本值，三层严格递增。
const integrationsProxyExecTimeout = 90 * time.Second

// integrationsProxyBudget 按收敛后的目标路径给出这次转发的总预算。
//
// 参数：
//   - targetPath: path.Clean 收敛后、**尚未拼接 RawQuery** 的目标机路径
//
// 返回：
//   - 该端点的转发时限
//
// 注意：
//   - 只按路径分档，不看 method 或请求体：预算是「这条链路要等多久」的属性，
//     与调用方下发什么无关，也不能让调用方影响它
func integrationsProxyBudget(targetPath string) time.Duration {
	if targetPath == integrationsProxyExecPath {
		return integrationsProxyExecTimeout
	}
	return integrationsProxyTimeout
}

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
//   - 目标路径为 "/api/integrations/" + rest 经 path.Clean 收敛后的结果，
//     RawQuery 非空时原样拼接在后面——查询串不经过 url.Values 解析再编码，
//     避免百分号编码被规范化成不同但语义等价的形式（虽然语义不变，但没必要
//     引入这一步不确定性）；path.Clean 只作用于 "?" 之前的路径部分，query
//     完全不受影响
//   - rest 解码后若含 dot segment 使收敛结果逃出 /api/integrations/ 前缀，
//     直接 404，不发起任何转发（见文件头注释「路由完整性」）
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

	// path.Clean 折叠 rest 段里可能出现的 dot segment——mux 对多段通配值做
	// 解码，调用方可以用 "%2E%2E" 令 r.PathValue("rest") 拿到字面量 ".."，
	// 裸 "../" 会被 mux 的 cleanPath 重定向拦下，但百分号编码形式不会：那道
	// 重定向跑在转义前的原始请求路径上，看不到解码后才出现的 ".."。不收敛
	// 这一步，"../../security/health" 这类 rest 就能让 targetPath 逃出
	// /api/integrations/ 前缀，把请求连同 nodetransport 为该 host 注入的凭据
	// 一起打到目标机任意端点。
	targetPath := path.Clean(integrationsProxyBasePath + "/" + r.PathValue("rest"))
	if targetPath != integrationsProxyBasePath && !strings.HasPrefix(targetPath, integrationsProxyBasePath+"/") {
		// 这是一条**安全控制**，拒绝必须留痕：不打日志的话，`%2E%2E` 这类
		// 借代理越权打到目标机任意端点的尝试在事后是零审计痕迹的。
		// 路径可以进日志（目标机自己的 handler 也这么做），token 值与文件内容不行；
		// 这里既不读也不转发请求体，落的只有 host_id 与被拒的收敛结果。
		name, _, _ := principalFromRequest(r)
		log.Printf("[SuperDev] integrations: 代理路径收敛守卫拒绝 host=%s %s rest=%q 收敛后=%s by=%s",
			hostID, r.Method, r.PathValue("rest"), targetPath, name)
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	// 预算必须在拼 RawQuery **之前**按纯路径判定：拼上查询串后
	// "/api/integrations/exec" 与 "/api/integrations/exec?x=1" 不再字面相等。
	budget := integrationsProxyBudget(targetPath)
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

	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	// 从空白重新构造请求头：绝不拷贝调用方（桌面端）的 Authorization 等头部，
	// 见文件头注释「转发头纪律」。目标机的全部 integrations 端点都以 JSON
	// 交互，固定声明 Content-Type 与纳管代理一致。
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	resp, _, _, err := a.doAgentRequestSchemeAware(ctx, hostID, r.Method, targetPath, headers, forwardBody)
	if err != nil {
		// 传输层故障（拨号/握手/超时），与目标机业务错误是不同故障域，见文件头注释。
		// 下面这行日志和 502 响应体都会带上 targetPath（可能含 ?path=<文件路径>）
		// 与 err.Error()（*url.Error 的消息里可能含完整目标 URL）——这是可接受的：
		// 路径不是 token/密钥，目标机自己的 handler 也把路径打进日志（见
		// handler_integrations_fs.go:106）；绝不落的是请求体内容（被转发的文件内容本身）。
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
