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
	for _, attempt := range attempts {
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
