// server_tls_loopback_test.go —— TLS 姿态下本机 loopback 客户端可用性验证。
//
// 职责：
//   - 复现根因：纯 TLS 监听器对明文请求回 400，TLS-on 后本机明文客户端整体失效
//   - 验证修复：Serve 的同端口协议嗅探让 loopback 明文客户端完成
//     /api/security/health 自举 + 一次带 token 的 API 调用，同时 HTTPS 不受影响
//   - 验证明文豁免的判定边界（loopback 判定函数）
//
// 边界：
//   - 不覆盖真实跨机流量（非 loopback 明文拒绝路径由 isLoopbackAddr 单测保证，
//     测试环境无法可靠制造非 loopback TCP 连接）
package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/mcp"
	"github.com/xsxdot/super-dev/agent/security"
)

// provisionTLSAutoApp 构造一个已被控制面 provision 成 tls_mode=auto 的 App，
// 返回 app 与 CA PEM。复刻真实时序：先落 security.json，再 NewApp 读取生效。
func provisionTLSAutoApp(t *testing.T) (*App, string) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := security.NewStore(filepath.Join(dataDir, "security.json"), security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	provision, err := store.Provision("bootstrap", security.ProvisionRequest{
		Token:   "control-plane-token",
		TLSMode: security.TLSModeAuto,
		Hosts:   []string{"192.0.2.10"}, // 刻意不含 loopback：真实 provision 只填控制面可达地址
	})
	require.NoError(t, err)
	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })
	return app, provision.CACert
}

// TestTLSOnlyListenerRejectsLoopbackPlaintext 复现修复前的根因：
// 老实现（ListenAndServeTLS 纯 TLS 监听）对本机明文请求回 Go 标准库的明文 400，
// superdev-mcp（默认 http://127.0.0.1:57017）与桌面端明文探测因此整体失效。
func TestTLSOnlyListenerRejectsLoopbackPlaintext(t *testing.T) {
	app, _ := provisionTLSAutoApp(t)
	tlsConfig, enabled, err := app.tlsConfigForListen()
	require.NoError(t, err)
	require.True(t, enabled)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: app.Handler(), TLSConfig: tlsConfig}
	go func() { _ = server.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = server.Close() })

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/security/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// Go TLS 监听器对明文请求的固定回复：这正是本机客户端拿到的失败形态。
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "HTTP request to an HTTPS server")
}

// TestServeAllowsLoopbackPlaintextBootstrapWhenTLSOn 验证修复主路径（需求 4）：
// TLS-on agent + loopback 明文 mcp 客户端完成 local-access-token 自举
// （/api/security/health → 读 token 文件）与一次带 token 的 API 调用。
func TestServeAllowsLoopbackPlaintextBootstrapWhenTLSOn(t *testing.T) {
	app, _ := provisionTLSAutoApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = app.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })

	// 与 cmd/superdev-mcp 生产入口完全同构：http:// 默认地址 + 本机凭据自举。
	agentURL := "http://" + ln.Addr().String()
	source := mcp.NewLocalFileTokenSource(agentURL, nil)
	client := mcp.NewHTTPAgentClientWithToken(agentURL, nil, source)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := source.Token(ctx)
	require.NoError(t, err, "loopback 明文自举应能在 TLS-on agent 上完成")
	require.NotEmpty(t, token)

	projects, err := client.ListProjects(ctx)
	require.NoError(t, err, "带 local-access-token 的 API 调用应通过鉴权")
	assert.NotNil(t, projects)
}

// TestServeStillServesTLSOnSamePort 验证嗅探分流不影响 TLS 流量：
// 同一端口上，带 CA 校验的 HTTPS 客户端仍能正常完成握手与请求。
func TestServeStillServesTLSOnSamePort(t *testing.T) {
	app, caPEM := provisionTLSAutoApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = app.Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(caPEM)))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "192.0.2.10",
	}}}
	resp, err := client.Get("https://" + ln.Addr().String() + "/api/security/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestIsLoopbackAddr 验证明文豁免判定的边界：仅 loopback 放行，
// 解析异常一律保守拒绝。
func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{"ipv4 loopback", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}, true},
		{"ipv4 loopback range", &net.TCPAddr{IP: net.IPv4(127, 8, 8, 8), Port: 1234}, true},
		{"ipv6 loopback", &net.TCPAddr{IP: net.IPv6loopback, Port: 1234}, true},
		{"lan address", &net.TCPAddr{IP: net.IPv4(192, 168, 1, 5), Port: 1234}, false},
		{"public address", &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 1234}, false},
		{"nil addr", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isLoopbackAddr(tc.addr))
		})
	}
}
