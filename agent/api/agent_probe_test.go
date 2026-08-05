// 本文件测试 scheme 感知请求助手：HTTPS 优先、明文回退、失败保守分类。
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// schemeFakeTransport 按 TLSOverride 区分两条 scheme 尝试，分别注入结果。
// 嵌入 testNodeTransport 补齐 NodeTransport 接口的其余方法（本测试只用 Do）。
type schemeFakeTransport struct {
	testNodeTransport
	httpsErr error
	httpErr  error
	body     string
	calls    []string
}

func (t *schemeFakeTransport) Do(_ context.Context, _ string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if req.TLSOverride == nil {
		return nodetransport.NodeResponse{}, errors.New("scheme 感知请求必须显式指定 TLSOverride")
	}
	scheme := "http"
	if req.TLSOverride.InsecureSkipVerify {
		scheme = "https"
	}
	t.calls = append(t.calls, scheme)
	err := t.httpErr
	if scheme == "https" {
		err = t.httpsErr
	}
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	return nodetransport.NodeResponse{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func TestSchemeAwareRequestPrefersHTTPS(t *testing.T) {
	app := newTestAppForPackage(t)
	tr := &schemeFakeTransport{body: "ok"}
	app.nodeTransport = tr

	resp, scheme, verdict, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodGet, "/api/security/health", nil, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "https", scheme)
	assert.Empty(t, string(verdict))
	// HTTPS 成功后不应再发明文尝试。
	assert.Equal(t, []string{"https"}, tr.calls)
}

func TestSchemeAwareRequestFallsBackToPlain(t *testing.T) {
	app := newTestAppForPackage(t)
	tr := &schemeFakeTransport{httpsErr: errors.New("tls: first record does not look like a TLS handshake"), body: "ok"}
	app.nodeTransport = tr

	resp, scheme, verdict, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodGet, "/api/security/health", nil, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "http", scheme)
	assert.Empty(t, string(verdict))
	assert.Equal(t, []string{"https", "http"}, tr.calls)
}

// TestSchemeAwareRequestDoesNotReplayNonIdempotentOnAmbiguousFailure 钉死非幂等
// 请求的重放防线：HTTPS 尝试失败但错误无法证明「请求没被送达」时，绝不用同一份
// body 再打一次明文。
//
// 这条防的是真实损害而不是理论问题：
//   - POST fs/rename 第一次其实成功了，第二次 from 已不存在 → 500 → 用户看到
//     「安装失败」而远端已生效
//   - PUT fs/write backup:true 第二次备份的是第一次刚写进去的新内容 → 用户原始
//     配置的备份被销毁
func TestSchemeAwareRequestDoesNotReplayNonIdempotentOnAmbiguousFailure(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			app := newTestAppForPackage(t)
			// 「已建立连接后失败」的典型形态：请求可能已经送达，回执没读回来。
			tr := &schemeFakeTransport{httpsErr: errors.New("unexpected EOF"), body: "ok"}
			app.nodeTransport = tr

			_, _, _, err := app.doAgentRequestSchemeAware(context.Background(), "h1", method,
				"/api/integrations/fs/write", nil, []byte(`{"path":"/home/u/.claude/settings.json"}`))

			require.Error(t, err, "无法确认请求未送达时必须把失败暴露给调用方")
			assert.Equal(t, []string{"https"}, tr.calls, "非幂等请求不得换 scheme 重放同一份 body")
		})
	}
}

// TestSchemeAwareRequestStillFallsBackForNonIdempotentWhenNothingWasDelivered
// 钉死上一条防线的另一侧：能证明请求根本没上路时，非幂等请求照常回退明文——
// 否则「目标机是明文 agent」这条**生产主路径**（六家 connector 的每一次远端
// 写入）会全线失败。
func TestSchemeAwareRequestStillFallsBackForNonIdempotentWhenNothingWasDelivered(t *testing.T) {
	cases := map[string]error{
		// Go 的 net/http 对「明文服务端应答了 TLS ClientHello」的归一化文案，
		// 也就是明文目标机上真正会看到的那一条。
		"明文服务端应答": errors.New(`Put "https://10.0.0.2:57019/api/integrations/fs/write": http: server gave HTTP response to HTTPS client`),
		"裸 TLS 记录错误": errors.New("tls: first record does not look like a TLS handshake"),
		"连接被拒":     fmt.Errorf("dial tcp 10.0.0.2:57019: %w", syscall.ECONNREFUSED),
	}
	for name, httpsErr := range cases {
		t.Run(name, func(t *testing.T) {
			app := newTestAppForPackage(t)
			tr := &schemeFakeTransport{httpsErr: httpsErr, body: "ok"}
			app.nodeTransport = tr

			resp, scheme, _, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodPut,
				"/api/integrations/fs/write", nil, []byte(`{"path":"/home/u/.claude/settings.json"}`))

			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, "http", scheme)
			assert.Equal(t, []string{"https", "http"}, tr.calls)
		})
	}
}

// TestSchemeAwareRequestKeepsFallbackForIdempotentMethods 钉死幂等 method 的
// 行为**一个字节都没变**：安装守卫探测与纳管状态轮询都是 GET，重放无副作用，
// 不该被这道非幂等防线牵连。
func TestSchemeAwareRequestKeepsFallbackForIdempotentMethods(t *testing.T) {
	app := newTestAppForPackage(t)
	tr := &schemeFakeTransport{httpsErr: errors.New("unexpected EOF"), body: "ok"}
	app.nodeTransport = tr

	resp, scheme, _, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodGet,
		"/api/security/health", nil, nil)

	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "http", scheme)
	assert.Equal(t, []string{"https", "http"}, tr.calls)
}

func TestSchemeAwareRequestRefusedMeansUnreachable(t *testing.T) {
	app := newTestAppForPackage(t)
	// 模拟真实错误链：direct/tunnel 都会把底层 syscall 错误包进自己的错误里。
	refused := fmt.Errorf("dial tcp 127.0.0.1:57017: %w", syscall.ECONNREFUSED)
	app.nodeTransport = &schemeFakeTransport{httpsErr: refused, httpErr: refused}

	_, _, verdict, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodGet, "/api/security/health", nil, nil)
	require.Error(t, err)
	assert.Equal(t, agentProbeUnreachable, verdict)
}

func TestSchemeAwareRequestAmbiguousMeansInconclusive(t *testing.T) {
	app := newTestAppForPackage(t)
	app.nodeTransport = &schemeFakeTransport{
		httpsErr: errors.New("context deadline exceeded"),
		httpErr:  errors.New("unexpected EOF"),
	}

	_, _, verdict, err := app.doAgentRequestSchemeAware(context.Background(), "h1", http.MethodGet, "/api/security/health", nil, nil)
	require.Error(t, err)
	assert.Equal(t, agentProbeInconclusive, verdict)
	// 合并错误里两条 scheme 的失败都要可见，便于排障。
	assert.Contains(t, err.Error(), "https:")
	assert.Contains(t, err.Error(), "http:")
}
