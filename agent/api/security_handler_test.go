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

	for _, path := range []string{"/api/security/health"} {
		rec := httptestDoWithHeader(t, app, http.MethodGet, path, nil, map[string]string{
			"Authorization": "",
		})
		require.NotEqual(t, http.StatusUnauthorized, rec.Code, path)
	}
}
