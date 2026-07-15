// handler_hosts_tunnel_security_test.go 验证 Host trust 配置变更会立即失效旧 tunnel 证据。
//
// 职责：
//   - 锁定 pin rotation 与显式清除后的 tunnel 状态转换
//   - 防止 Host 当前配置与已缓存 SSH 握手证据发生漂移
//
// 边界：
//   - 不建立真实 SSH 连接，使用携带已验证证据的 fake dialer
//   - 不验证 host-key 加密算法，算法合同由 tunnel 包测试覆盖
package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

type blockingHostPinDialer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingHostPinDialer() *blockingHostPinDialer {
	return &blockingHostPinDialer{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingHostPinDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.once.Do(func() { close(d.started) })
	<-d.release
	identity, err := tunnel.HostKeyIdentitySHA256(target.SSHHostKeyFingerprint)
	if err != nil {
		return nil, err
	}
	return tunnel.NewFakeVerifiedConn(57100, identity), nil
}

func TestUpdateHostPinChangeInvalidatesExistingTunnelEvidence(t *testing.T) {
	tests := map[string]string{
		"rotate": `"ssh_host_key_fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`,
		"clear":  `"clear_ssh_host_key_fingerprint":true`,
	}
	for name, pinMutation := range tests {
		t.Run(name, func(t *testing.T) {
			app, err := NewApp(AppConfig{DataDir: t.TempDir()})
			require.NoError(t, err)
			defer app.Close()
			app.tunnels = tunnel.NewManager(successTunnelDialer{})
			host, err := app.remoteStore.AddHost(model.Host{
				ID:                    "h1",
				Name:                  "edge",
				SSHHost:               "ssh.example.com",
				SSHUser:               "deploy",
				SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
			})
			require.NoError(t, err)
			_, err = app.tunnels.EnsureConnected(tunnel.Target{
				HostID:                host.ID,
				SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
			})
			require.NoError(t, err)
			assert.True(t, app.tunnels.HostKeyEvidenceOf(host.ID).Verified)

			body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy",` + pinMutation + `}`)
			req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
			req.SetPathValue("id", host.ID)
			rr := httptest.NewRecorder()

			app.updateHost(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))
			assert.Equal(t, tunnel.HostKeyEvidence{}, app.tunnels.HostKeyEvidenceOf(host.ID))
		})
	}
}

func TestUpdateHostPinChangeInvalidatesInFlightTunnelAttempt(t *testing.T) {
	tests := map[string]string{
		"rotate": `"ssh_host_key_fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`,
		"clear":  `"clear_ssh_host_key_fingerprint":true`,
	}
	for name, pinMutation := range tests {
		t.Run(name, func(t *testing.T) {
			app, err := NewApp(AppConfig{DataDir: t.TempDir()})
			require.NoError(t, err)
			defer app.Close()
			dialer := newBlockingHostPinDialer()
			app.tunnels = tunnel.NewManager(dialer)
			host, err := app.remoteStore.AddHost(model.Host{
				ID:                    "h1",
				Name:                  "edge",
				SSHHost:               "ssh.example.com",
				SSHUser:               "deploy",
				SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
			})
			require.NoError(t, err)
			result := make(chan error, 1)
			go func() {
				_, connectErr := app.tunnels.EnsureConnected(tunnel.Target{
					HostID:                host.ID,
					SSHHostKeyFingerprint: host.SSHHostKeyFingerprint,
				})
				result <- connectErr
			}()
			<-dialer.started

			body := bytes.NewBufferString(`{"name":"edge","ssh_host":"ssh.example.com","ssh_user":"deploy",` + pinMutation + `}`)
			req := httptest.NewRequest(http.MethodPut, "/api/hosts/"+host.ID, body)
			req.SetPathValue("id", host.ID)
			rr := httptest.NewRecorder()
			app.updateHost(rr, req)
			require.Equal(t, http.StatusOK, rr.Code)

			close(dialer.release)
			require.Error(t, <-result)
			assert.Equal(t, tunnel.StatusDisconnected, app.tunnels.Status(host.ID))
			assert.Zero(t, app.tunnels.LocalPort(host.ID))
			assert.Equal(t, tunnel.HostKeyEvidence{}, app.tunnels.HostKeyEvidenceOf(host.ID))
		})
	}
}
