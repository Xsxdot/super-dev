// server_tls_test.go 验证 agent 启动层能消费 security store 中的 TLS 材料。
//
// 职责：
//   - 证明 auto TLS provision 后，App 可构造 HTTPS listener 所需的 TLSConfig
//   - 证明该 TLSConfig 能服务 security health 端点
//
// 边界：
//   - 不执行真实服务安装或重启
//   - 不测试 direct transport 的客户端 CA 逻辑
package api

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

func TestAppTLSConfigUsesProvisionedAutoCertificate(t *testing.T) {
	dataDir := t.TempDir()
	store, err := security.NewStore(filepath.Join(dataDir, "security.json"), security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	provision, err := store.Provision("bootstrap", security.ProvisionRequest{
		Token:   "long-token",
		TLSMode: security.TLSModeAuto,
		Hosts:   []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, provision.CACert)

	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	defer app.Close()
	tlsConfig, enabled, err := app.tlsConfigForListen()
	require.NoError(t, err)
	require.True(t, enabled)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: app.Handler(), TLSConfig: tlsConfig}
	go func() {
		_ = server.ServeTLS(ln, "", "")
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(provision.CACert)))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "127.0.0.1",
	}}}
	resp, err := client.Get("https://" + ln.Addr().String() + "/api/security/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
