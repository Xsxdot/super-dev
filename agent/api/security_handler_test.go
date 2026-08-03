package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

func TestSecurityHealthBypassesAuthWhilePendingBootstrap(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()

	resp := httptestDo(t, app, http.MethodGet, "/api/security/health", nil)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"provision_state":"pending-bootstrap"`)
}

func TestSecurityMiddlewareRequiresTokenAfterProvision(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()

	provision := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/provision",
		bytes.NewBufferString(`{"token":"long-token","tls_mode":"off"}`),
		map[string]string{"Authorization": "Bearer bootstrap"},
	)
	require.Equal(t, http.StatusOK, provision.Code)

	// httptestDo 委托 httptestDoWithHeader(headers=nil) 会默认注入本机 token，
	// 这里要验证的是"没有任何凭据"，必须显式传空 Authorization 关掉默认注入。
	unauthorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	authorized := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil,
		map[string]string{"Authorization": "Bearer long-token"},
	)
	require.Equal(t, http.StatusOK, authorized.Code)
}

// 鉴权常开：open 态（无 bootstrap、未 provision）的 agent 也必须拒绝裸请求。
//
// 这是 Task 2 的核心行为翻转：Task 1 之前，AuthRequired()==false 的 open 态会让
// withSecurity 整体放行；常开语义下 open 态只是"未配置长期 token"，不等于"不需要凭据"。
func TestSecurityMiddlewareRequiresTokenEvenInOpenState(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	rec := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil, map[string]string{
		"Authorization": "", // 显式覆盖 helper 默认注入，模拟裸请求
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "agent token required")
}

// 本机 token 走 Authorization 头可通过，且无需完成 bootstrap/provision 流程——
// local-access-token 与长期 token 是两条独立且都被接受的鉴权路径。
func TestSecurityMiddlewareAcceptsLocalAccessToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	rec := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil, map[string]string{
		"Authorization": "Bearer " + app.LocalAccessToken(),
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

// WS 前缀路径接受 access_token 查询参数；HTTP API 路径不接受。
//
// 路径选择：/ws/nodes 是 server.go Handler() 中真实注册的 WS 路由（a.wsNodes），
// 无需路径参数，是最小可用样本。
func TestSecurityMiddlewareAccessTokenQueryOnlyForWebSocketPaths(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	token := app.LocalAccessToken()

	// /ws/ 前缀：query token 应通过鉴权（后续可能因非 Upgrade 请求被 handler 拒，但绝不是 401）
	rec := httptestDoWithHeader(t, app, http.MethodGet, "/ws/nodes?access_token="+token, nil, map[string]string{
		"Authorization": "",
	})
	require.NotEqual(t, http.StatusUnauthorized, rec.Code, "WS 路径 query token 应通过鉴权层")

	// /api/ 路径：query token 不生效，仍 401
	rec = httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health?access_token="+token, nil, map[string]string{
		"Authorization": "",
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "HTTP API 不接受 query token")
}

// loopback 请求可见 local_token_path；非 loopback 不可见。
// 路径本身不是秘密（可预测），但仍不对远端披露——最小信息面。
func TestSecurityHealthExposesLocalTokenPathOnlyToLoopback(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/security/health", nil)
	req.RemoteAddr = "127.0.0.1:55555"
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Version        string `json:"version"`
		LocalTokenPath string `json:"local_token_path"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Version)
	require.True(t, strings.HasSuffix(resp.LocalTokenPath, "local-access-token"), "loopback 请求应拿到 token 文件路径")

	// httptest.NewRequest 默认 RemoteAddr 是 192.0.2.1:1234（TEST-NET，非 loopback）
	req2 := httptest.NewRequest(http.MethodGet, "/api/security/health", nil)
	rec2 := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.NotContains(t, rec2.Body.String(), "local_token_path", "非 loopback 不披露")
}

// bypass 白名单四条在常开语义下仍免鉴权。
func TestSecurityBypassPathsRemainOpen(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	// bypass 的语义是「跳过 Bearer 中间件」，不是「永不 401」：
	// install.sh / install-binary 有自己的一次性安装 token 鉴权（query 参数），
	// 缺 token 时返回自有文案的 401。因此这里断言的是响应不含中间件的
	// "agent token required" 拒绝文案，而非状态码不等于 401。
	for _, path := range []string{
		"/api/security/health",
		"/api/security/provision",
		"/api/agents/install.sh",
		"/api/agents/install-binary",
	} {
		rec := httptestDoWithHeader(t, app, http.MethodGet, path, nil, map[string]string{
			"Authorization": "",
		})
		require.NotContains(t, rec.Body.String(), "agent token required", path)
	}
}

// probePrincipal 包一层 app.withSecurity，让下游 handler 把校验后 ctx 里的
// Principal 读出来，供以下测试断言——不新增生产路由，只在测试内组装。
func probePrincipal(app *App) (http.Handler, func() (security.Principal, bool)) {
	var got security.Principal
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = security.PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	return app.withSecurity(next), func() (security.Principal, bool) { return got, ok }
}

// 本机 token 命中时，ctx 应挂载 {local, "local", "本机"}——withSecurity 从
// VerifyLocalToken 分支推导 Principal，供后续任务（如审批裁决审计）读取真实身份。
func TestWithSecurityInjectsPrincipalForLocalToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	handler, principal := probePrincipal(app)
	req := httptest.NewRequest(http.MethodGet, "/api/exec/health", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	got, ok := principal()
	require.True(t, ok, "本机 token 命中应注入 Principal")
	require.Equal(t, security.Principal{Type: security.PrincipalLocal, ID: "local", Name: "本机"}, got)
}

// 远程 TokenRecord 命中时，ctx 应挂载 {remote, rec.ID, rec.Name}——这是 Task 4
// 取代请求体自报 decided_by 的唯一身份来源。
func TestWithSecurityInjectsPrincipalForRemoteToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	record, err := app.securityStore.AppendTokenRecord("CP-A", "remote-token")
	require.NoError(t, err)

	handler, principal := probePrincipal(app)
	req := httptest.NewRequest(http.MethodGet, "/api/exec/health", nil)
	req.Header.Set("Authorization", "Bearer remote-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	got, ok := principal()
	require.True(t, ok, "远程 token 命中应注入 Principal")
	require.Equal(t, security.Principal{Type: security.PrincipalRemote, ID: record.ID, Name: "CP-A"}, got)
}

// bypass 白名单路径无凭据校验，也就无法推导「谁在请求」——不应注入 Principal，
// 避免下游误把零值/伪造身份当成真实主体使用。
func TestWithSecurityDoesNotInjectPrincipalOnBypassPath(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	handler, principal := probePrincipal(app)
	req := httptest.NewRequest(http.MethodGet, "/api/security/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	_, ok := principal()
	require.False(t, ok, "bypass 路径不应注入 Principal")
}
