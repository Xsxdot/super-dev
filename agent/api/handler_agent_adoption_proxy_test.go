// 本文件测试纳管代理端点：路径/方法/请求体透传、响应状态码与稳定错误码透传、
// 目标不可达时的 502 降级。
package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// adoptionProxyFakeTransport 记录转发要素并回放注入的目标机响应。
type adoptionProxyFakeTransport struct {
	testNodeTransport
	status    int
	body      string
	err       error
	gotMethod string
	gotPath   string
	gotBody   []byte
}

func (t *adoptionProxyFakeTransport) Do(_ context.Context, _ string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	t.gotMethod = req.Method
	t.gotPath = req.Path
	if req.Body != nil {
		t.gotBody, _ = io.ReadAll(req.Body)
	}
	if t.err != nil {
		return nodetransport.NodeResponse{}, t.err
	}
	return nodetransport.NodeResponse{
		StatusCode: t.status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func newAdoptionProxyTestApp(t *testing.T, tr nodetransport.NodeTransport) (*App, string) {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"100.117.127.123","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)
	app.nodeTransport = tr
	return app, hostID
}

func TestAdoptionProxyCreateForwardsVerbatim(t *testing.T) {
	tr := &adoptionProxyFakeTransport{status: http.StatusOK, body: `{"id":"req-1","pairing_code":"123456","state":"pending","expires_at":"2026-08-04T11:00:00Z"}`}
	app, hostID := newAdoptionProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adoption-requests", bytes.NewBufferString(`{"name":"CP-B"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, http.MethodPost, tr.gotMethod)
	assert.Equal(t, "/api/security/adoption-requests", tr.gotPath)
	// 请求体必须原样转发——目标机对 name 有自己的截断/脱敏纪律，代理不改写。
	assert.JSONEq(t, `{"name":"CP-B"}`, string(tr.gotBody))
	assert.Contains(t, resp.Body.String(), `"pairing_code":"123456"`)
}

func TestAdoptionProxyStatusAndExchangePaths(t *testing.T) {
	tr := &adoptionProxyFakeTransport{status: http.StatusOK, body: `{"state":"approved"}`}
	app, hostID := newAdoptionProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/"+hostID+"/adoption-requests/req-9", nil)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "/api/security/adoption-requests/req-9", tr.gotPath)
	assert.Equal(t, http.MethodGet, tr.gotMethod)

	resp = httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adoption-requests/req-9/exchange", bytes.NewBufferString(`{"adoption_token":"one-time"}`))
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "/api/security/adoption-requests/req-9/exchange", tr.gotPath)
	assert.JSONEq(t, `{"adoption_token":"one-time"}`, string(tr.gotBody))
}

// TestAdoptionProxyPassesThroughTargetErrors 钉死「代理不做二次解释」：目标机的
// 稳定错误码（429 限流等）必须连状态码带 body 原样到达桌面端。
func TestAdoptionProxyPassesThroughTargetErrors(t *testing.T) {
	tr := &adoptionProxyFakeTransport{status: http.StatusTooManyRequests, body: `{"code":"adoption_rate_limited","error":"too many pending requests"}`}
	app, hostID := newAdoptionProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adoption-requests", bytes.NewBufferString(`{"name":"CP-B"}`))

	require.Equal(t, http.StatusTooManyRequests, resp.Code)
	assert.Contains(t, resp.Body.String(), `"code":"adoption_rate_limited"`)
}

func TestAdoptionProxyTargetUnreachableIs502(t *testing.T) {
	tr := &adoptionProxyFakeTransport{err: errors.New("dial tcp: connect timed out")}
	app, hostID := newAdoptionProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adoption-requests", bytes.NewBufferString(`{"name":"CP-B"}`))

	require.Equal(t, http.StatusBadGateway, resp.Code)
	assert.Contains(t, resp.Body.String(), `"code":"adoption_target_unreachable"`)
}

func TestAdoptionProxyUnknownHost404(t *testing.T) {
	tr := &adoptionProxyFakeTransport{status: http.StatusOK, body: `{}`}
	app, _ := newAdoptionProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/no-such-host/adoption-requests", bytes.NewBufferString(`{"name":"CP-B"}`))

	require.Equal(t, http.StatusNotFound, resp.Code)
}
