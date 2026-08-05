// 本文件提供与「陌生 agent」首次接触的 scheme 感知请求助手。
//
// 职责：
//   - 对可能已被其他控制面 provision 成自签 HTTPS 的目标机，先以不验证证书的
//     HTTPS 尝试请求，失败再退明文 HTTP——屏蔽 nodetransport.tlsSpecForRequest
//     「本地记录未 provisioned ⇒ 远端明文」的默认收敛（该假设在纳管/安装守卫
//     场景下系统性失真：目标机恰恰是被别人 provision 过的）
//   - 两条 scheme 都失败时给出「确定无监听 / 无法断定」的保守分类，供安装守卫
//     决定静默放行还是带警示放行
//
// 边界：
//   - 只服务安装守卫探测与纳管接入通道这两类「与陌生 agent 首次接触、尚无信任
//     锚」的流量；常规带凭据流量必须走各自 Agent.Security.TLS 的正常证书校验，
//     不得使用本助手
//   - 不缓存 scheme 结论，每次调用独立尝试；不负责解释 HTTP 状态码语义
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"syscall"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// agentProbeVerdict 是 scheme 感知请求两条 scheme 都失败时的保守分类。
type agentProbeVerdict string

const (
	// agentProbeUnreachable 表示可以断定目标端口上没有任何监听者（连接被拒）。
	agentProbeUnreachable agentProbeVerdict = "unreachable"
	// agentProbeInconclusive 表示无法断定（超时 / 握手异常 / 非 agent 服务等）。
	agentProbeInconclusive agentProbeVerdict = "inconclusive"
)

// doAgentRequestSchemeAware 对目标机执行一次「先 HTTPS(不验证证书) 再退明文」的请求。
//
// 参数：
//   - ctx: 调用方控制总超时；两次尝试顺序执行、共享同一预算
//   - hostID: 经 nodeTransport 按 host_id 寻址（direct 或 tunnel 链皆可）
//   - method/path/headers: 请求要素
//   - body: 可重放的请求体字节，两次尝试各自新建 reader；nil 表示无 body
//
// 返回：
//   - 任一 scheme 拿到 HTTP 响应即成功（含非 2xx——状态码语义由调用方解释），
//     第二个返回值为实际成功的 scheme（"https" / "http"）
//   - 两条 scheme 都失败时返回保守分类 verdict 与合并错误
//
// 注意：
//   - 先试 HTTPS：被 provision 过的 agent 默认（tls_mode=auto）以自签 HTTPS
//     监听，先探 HTTPS 才能在纳管场景探到它；明文目标下多付一次握手失败的
//     开销可接受（守卫探测与纳管均为低频交互路径）。tunnel 链路上失败尝试
//     可能触发一次隧道重建，同样以低频为前提可接受
//   - 经本助手转发的 body 可能含一次性 adoption token，绝不落日志
//   - **非幂等请求不无条件重放**：换 scheme 重试等于把同一份 body 再发一次，
//     而第一次失败并不代表它没被送达（响应读取失败 / 隧道中断都是「已送达但
//     没拿到回执」）。判据见 plaintextRetrySafe
func (a *App) doAgentRequestSchemeAware(ctx context.Context, hostID string, method, path string, headers http.Header, body []byte) (nodetransport.NodeResponse, string, agentProbeVerdict, error) {
	attempts := []struct {
		scheme string
		tls    model.AgentTLSSpec
	}{
		{"https", model.AgentTLSSpec{Mode: model.AgentTLSModeAuto, InsecureSkipVerify: true}},
		{"http", model.AgentTLSSpec{Mode: model.AgentTLSModeOff}},
	}
	var errs []error
	refused := false
	for i, attempt := range attempts {
		tlsSpec := attempt.tls
		req := nodetransport.NodeRequest{
			Method:      method,
			Path:        path,
			Headers:     headers,
			TLSOverride: &tlsSpec,
		}
		if body != nil {
			req.Body = bytes.NewReader(body)
		}
		resp, err := a.nodeTransport.Do(ctx, hostID, req)
		if err == nil {
			if attempt.scheme == "http" && len(errs) > 0 {
				// 只在「HTTPS 失败、明文成功」时点一笔：这是判断目标 TLS 姿态的
				// 关键诊断线索（path 是固定端点路径，不含任何秘密值）。
				log.Printf("[SuperDev] scheme 感知请求：host=%s %s HTTPS 尝试未通，已退明文成功", hostID, path)
			}
			return resp, attempt.scheme, "", nil
		}
		if isConnectionRefused(err) {
			refused = true
		}
		errs = append(errs, fmt.Errorf("%s: %w", attempt.scheme, err))
		if ctx.Err() != nil {
			// 总预算已被第一次尝试耗尽，第二次注定超时，不再做无谓尝试。
			break
		}
		if i+1 < len(attempts) && !plaintextRetrySafe(method, err) {
			// 非幂等请求 + 无法证明「请求没被送达」：不换 scheme 重放。
			// 直接返回失败让调用方看见错误，比悄悄把同一份 body 再发一次安全
			// 得多，理由与代价见 plaintextRetrySafe 头注释。
			log.Printf("[SuperDev] scheme 感知请求：host=%s %s %s HTTPS 尝试失败且无法确认请求未送达，"+
				"非幂等请求不回退明文", hostID, method, path)
			break
		}
	}
	verdict := agentProbeInconclusive
	if refused {
		// 两种 scheme 打的是同一个 TCP 端口，任一尝试收到「连接被拒」即可断定
		// 端口上没有监听者——另一次尝试的失败形态（握手错乱等）只是同一事实
		// 的不同表达。除此之外（超时/握手异常/非 agent 服务）一律保守判定为
		// 「无法断定」，让调用方带警示放行而不是静默放行。
		verdict = agentProbeUnreachable
	}
	return nodetransport.NodeResponse{}, "", verdict, errors.Join(errs...)
}

// plaintextRetrySafe 判断「HTTPS 尝试失败后再用同一份 body 试一次明文」是否安全。
//
// 参数：
//   - method: 本次请求的 HTTP method
//   - err: HTTPS 尝试的失败原因
//
// 返回：
//   - true 表示可以回退明文（重放无害或可证明请求未送达）
//
// 为什么需要它：换 scheme 重试是一次**重放**——同一份 body 再发一次。传输层
// 错误里既有「压根没连上」，也有「请求已经送达、只是响应没读回来 / 隧道中断」，
// 两者在调用方眼里长得一样。对非幂等请求盲目重放会造成真实损害，两个已实证的
// 具体后果：
//   - POST fs/rename：第一次其实成功了，第二次 from 已不存在 → 500 →
//     用户看到「安装失败」，而远端其实已经生效
//   - PUT fs/write backup:true：第二次备份的是**第一次刚写进去的新内容** →
//     用户原始配置的备份被销毁，回滚依据没了
//
// 判据（deny by default，只对能证明「请求根本没上路」的失败放行）：
//   - 幂等 method（GET/HEAD/OPTIONS）一律放行——重放无副作用，且这是安装守卫
//     探测与纳管状态轮询的形态，行为与本判据引入前逐字节一致
//   - 连接被拒：TCP 都没建起来，body 不可能发出去
//   - TLS 握手阶段失败：HTTP 请求字节要等握手完成才写，握手没成就一定没送达。
//     明文目标下 HTTPS 尝试的**真实**形态就在这里——Go 的 net/http 会把首个
//     record 形如 "HTTP/" 的 tls.RecordHeaderError 换成
//     "http: server gave HTTP response to HTTPS client" 这条纯文本错误（类型
//     信息在那一步被丢掉，所以既 errors.As 又匹文本，两条都要）
//
// 代价：非幂等请求遇到无法归类的传输层错误时直接失败，即便目标其实是明文的。
// 这是刻意选的方向——让用户看见一次明确失败并重试，好过静默写坏远端配置。
func plaintextRetrySafe(method string, err error) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return isPreDeliveryFailure(err)
	default:
		return true
	}
}

// preDeliveryErrorMarkers 是「失败发生在请求送达之前」的错误文本特征。
//
// 与 isConnectionRefused 同一理由用文本兜底：错误经 NodeError / url.Error 多层
// 包装后 errors.As 未必还能取到原始类型（tunnel 链路尤其）。这里的漏判方向是
// 安全的——归不了类就不重放。
var preDeliveryErrorMarkers = []string{
	// net/http 对「明文服务端应答了 TLS ClientHello」的归一化文案。
	"server gave http response to https client",
	// tls.RecordHeaderError 的原始文案（首个 record 不是 "HTTP/" 时的形态）。
	"does not look like a tls handshake",
	// 握手阶段的告警：对端确实在说 TLS，但没谈成——同样没有请求字节被写出去。
	"tls: handshake failure",
	"remote error: tls:",
}

// isPreDeliveryFailure 判断错误是否可证明「请求还没被送到对端」。
func isPreDeliveryFailure(err error) bool {
	if err == nil {
		return false
	}
	if isConnectionRefused(err) {
		return true
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range preDeliveryErrorMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// isConnectionRefused 判断错误链中是否含「连接被拒」。
//
// tunnel 链路的错误经多层包装（SSH 转发 → NodeError）后 errors.Is 未必可达
// 原始 syscall 错误，用错误文本兜底——宁可把个别「refused」误判漏成
// inconclusive（后果只是多一条警示），不把 inconclusive 误判成 refused。
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}
