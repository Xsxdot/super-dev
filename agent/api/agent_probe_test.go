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
