// 本文件测试 integrations 跨机代理：method/path/RawQuery/body 转发正确性、
// 目标机状态码与响应体原样透传（含错误响应，不被代理二次解释）、目标不可达
// 时的 502 降级，以及未知 host / 匿名请求的拒绝路径。
package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// integrationsProxyFakeTransport 记录转发要素（含请求头，用于验证 Authorization
// 绝不透传）并回放注入的目标机响应。
type integrationsProxyFakeTransport struct {
	testNodeTransport
	status     int
	body       string
	err        error
	gotMethod  string
	gotPath    string
	gotBody    []byte
	gotHeaders http.Header
}

func (t *integrationsProxyFakeTransport) Do(_ context.Context, _ string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	t.gotMethod = req.Method
	t.gotPath = req.Path
	t.gotHeaders = req.Headers
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

func newIntegrationsProxyTestApp(t *testing.T, tr nodetransport.NodeTransport) (*App, string) {
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

// TestIntegrationsProxyForwardsStatQueryVerbatim 钉死 RawQuery 原样透传：
// path 查询参数里含空格与中文，代理层不得二次编码/解码破坏它。
func TestIntegrationsProxyForwardsStatQueryVerbatim(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{"exists":true,"is_dir":false}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	rawPath := "/Users/xsx/中文 目录/settings.json"
	query := "path=" + url.QueryEscape(rawPath)
	resp := httptestDo(t, app, http.MethodGet, "/api/agents/"+hostID+"/integrations/fs/stat?"+query, nil)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, http.MethodGet, tr.gotMethod)
	// gotPath 必须与 RawQuery 逐字节相同——不是重新 url.Values{}.Encode() 出来的等价形式。
	assert.Equal(t, "/api/integrations/fs/stat?"+query, tr.gotPath)
	assert.JSONEq(t, `{"exists":true,"is_dir":false}`, resp.Body.String())
}

// TestIntegrationsProxyForwardsWriteBatchBody 钉死大请求体（PUT）原样转发，
// 且未知 rest 段（write-batch）正确拼接到目标路径。
func TestIntegrationsProxyForwardsWriteBatchBody(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{"written":["a.txt"]}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	reqBody := `{"dir":"/home/x/.claude","files":[{"rel_path":"a.txt","content":"aGVsbG8="}]}`
	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID+"/integrations/fs/write-batch", bytes.NewBufferString(reqBody))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, http.MethodPut, tr.gotMethod)
	assert.Equal(t, "/api/integrations/fs/write-batch", tr.gotPath)
	assert.JSONEq(t, reqBody, string(tr.gotBody))
	assert.JSONEq(t, `{"written":["a.txt"]}`, resp.Body.String())
}

// TestIntegrationsProxyPassesThroughTargetForbidden 钉死目标机 403（白名单拒绝）
// 原样透传，不被代理改写成 502 或吞成通用错误。
func TestIntegrationsProxyPassesThroughTargetForbidden(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusForbidden, body: `{"code":"path_not_allowed","error":"path not allowed"}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodDelete, "/api/agents/"+hostID+"/integrations/fs?path=%2Fetc%2Fpasswd", nil)

	require.Equal(t, http.StatusForbidden, resp.Code)
	assert.Contains(t, resp.Body.String(), `"code":"path_not_allowed"`)
}

// TestIntegrationsProxyPassesThroughTargetTooLarge 钉死目标机 413（write-batch
// 内容总量超限）原样透传。
func TestIntegrationsProxyPassesThroughTargetTooLarge(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusRequestEntityTooLarge, body: `{"error":"content too large"}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID+"/integrations/fs/write-batch", bytes.NewBufferString(`{"dir":"/x","files":[]}`))

	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
	assert.Contains(t, resp.Body.String(), "content too large")
}

// TestIntegrationsProxyPassesThroughTargetInternalError 区分「传输错误」与
// 「目标机自己的 5xx 业务错误」：后者必须原样透传状态码与 body，不能被吞成
// 502 integration_target_unreachable——否则两类错误在桌面端不可区分。
func TestIntegrationsProxyPassesThroughTargetInternalError(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusInternalServerError, body: `{"error":"rename source not found"}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/integrations/fs/rename", bytes.NewBufferString(`{"from":"a","to":"b"}`))

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Contains(t, resp.Body.String(), "rename source not found")
	assert.NotContains(t, resp.Body.String(), "integration_target_unreachable")
}

// TestIntegrationsProxyTargetUnreachableIs502 钉死传输层错误（拨号失败/握手异常
// 等，与目标机业务错误完全不同的故障域）映射为 502 稳定错误码。
func TestIntegrationsProxyTargetUnreachableIs502(t *testing.T) {
	tr := &integrationsProxyFakeTransport{err: errors.New("dial tcp: connect timed out")}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/"+hostID+"/integrations/fs/list?path=%2Fhome%2Fx", nil)

	require.Equal(t, http.StatusBadGateway, resp.Code)
	assert.Contains(t, resp.Body.String(), `"code":"integration_target_unreachable"`)
}

// TestIntegrationsProxyExecBudgetExceedsTargetCommandCeiling 钉死三层超时的
// 严格递增关系。
//
// 为什么这条必须存在：exec 是本代理转发的唯一**有副作用**的端点，而副作用发生在
// 目标机上、由目标机自己的时限管着。代理预算一旦小于命令时限上限，失败方式是
// 「桌面端报错、目标机照样把配置写完」——用户看到装失败、机器其实装好了，
// 而且 exec 端点 timed_out=true 那条契约在生产里永远触发不到（代理先超时）。
// 这个关系跨三个文件三种语言，只能靠测试钉，注释钉不住。
func TestIntegrationsProxyExecBudgetExceedsTargetCommandCeiling(t *testing.T) {
	assert.Greater(t, integrationsProxyExecTimeout, integrationsExecMaxTimeout,
		"exec 转发预算必须大于目标机命令时限上限，否则内层 timed_out 语义整个失效")
	// 桌面端 remote_command.rs 的 REMOTE_COMMAND_HTTP_TIMEOUT 必须再大于本值，
	// 那一层由 Rust 侧 remote_command_http_timeout_outlasts_the_agent_proxy_budget 钉。

	assert.Equal(t, integrationsProxyExecTimeout, integrationsProxyBudget("/api/integrations/exec"),
		"exec 走加宽预算")
	assert.Equal(t, integrationsProxyTimeout, integrationsProxyBudget("/api/integrations/fs/write-batch"),
		"文件类端点保持原预算——它们的耗时由本跳决定，目标机侧没有自己的等待时限")
	assert.Equal(t, integrationsProxyTimeout, integrationsProxyBudget("/api/integrations/detect"),
		"detect 保持原预算")
	// 前缀相同但不是 exec 的路径不得蹭到加宽预算。
	assert.Equal(t, integrationsProxyTimeout, integrationsProxyBudget("/api/integrations/exec-something"),
		"只有精确路径命中，前缀相近的端点不得蹭预算")
}

// TestIntegrationsProxyUnknownHost404 覆盖未配置 agent 的 host_id。
func TestIntegrationsProxyUnknownHost404(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{}`}
	app, _ := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/no-such-host/integrations/detect", nil)

	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestIntegrationsProxyRejectsPercentEncodedDotSegmentEscape 钉死路由完整性：
// {rest...} 通配段经 mux 解码后可能含形如 %2E%2E 的百分号编码 dot segment，
// 裸 "../" 会被 mux 的 cleanPath 重定向拦下，但百分号编码形式不会（那道重定向
// 跑在转义前的原始路径上）。必须 404，且断言 fake transport 完全没被调用——
// 仅断言 404 不够，404 也可能来自「未知 host」分支，这里用已知 host 排除歧义。
func TestIntegrationsProxyRejectsPercentEncodedDotSegmentEscape(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/"+hostID+"/integrations/%2E%2E/%2E%2E/security/health", nil)

	require.Equal(t, http.StatusNotFound, resp.Code)
	assert.Empty(t, tr.gotMethod, "越界路径必须在转发前就被拒绝，fake transport 不应被调用")
	assert.Empty(t, tr.gotPath, "越界路径必须在转发前就被拒绝，fake transport 不应被调用")
}

// TestIntegrationsProxyRejectsAnonymousRequest 覆盖鉴权红线：本代理路径绝不进
// securityBypassPath，匿名请求必须 401（与 adoption 代理同一红线）。
func TestIntegrationsProxyRejectsAnonymousRequest(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDoWithHeader(t, app, http.MethodPost, "/api/agents/"+hostID+"/integrations/detect", bytes.NewBufferString(`{"commands":["claude"]}`),
		map[string]string{"Authorization": ""},
	)
	require.Equal(t, http.StatusUnauthorized, resp.Code, "匿名请求必须 401")
	require.Contains(t, resp.Body.String(), "agent token required")
}

// TestIntegrationsProxyDoesNotForwardAuthorization 钉死转发头纪律：桌面侧的
// Authorization 头（本机 token）绝不透传给目标机——目标机的凭据由 nodetransport
// 按其自身 Agent Secret 独立注入，与桌面到本机 agent 这一跳的凭据是两回事。
func TestIntegrationsProxyDoesNotForwardAuthorization(t *testing.T) {
	tr := &integrationsProxyFakeTransport{status: http.StatusOK, body: `{}`}
	app, hostID := newIntegrationsProxyTestApp(t, tr)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/integrations/detect", bytes.NewBufferString(`{"commands":["claude"]}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Empty(t, tr.gotHeaders.Get("Authorization"), "桌面侧 Authorization 绝不能透传给目标机")
}
