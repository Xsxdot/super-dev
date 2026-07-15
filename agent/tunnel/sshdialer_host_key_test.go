// sshdialer_host_key_test.go 验证 tunnel 生产 Dialer 的 host-key pin 合同。
//
// 职责：
//   - 通过真实 SSH 握手证明正确 pin 才能建立本地 tunnel
//   - 证明 mismatch 不会留下可复用 Conn 或安全证据
//
// 边界：
//   - 不通过 tunnel 发送 Agent 请求
//   - 不连接外部主机
package tunnel_test

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/testsupport/sshtest"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

func TestSSHDialerRequiresExactTrustedHostKey(t *testing.T) {
	server := sshtest.Start(t)
	hostname, portText, err := net.SplitHostPort(server.Address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	target := tunnel.Target{
		HostID:                "h1",
		SSHHost:               hostname,
		SSHPort:               port,
		SSHUser:               "ops",
		SSHPassword:           "pw",
		SSHHostKeyFingerprint: server.Fingerprint,
		RemoteAgentPort:       57017,
	}

	connection, err := tunnel.NewSSHDialer().Dial(target)
	require.NoError(t, err)
	connection.Close()

	target.SSHHostKeyFingerprint = ""
	_, err = tunnel.NewSSHDialer().Dial(target)
	require.Error(t, err)
	assert.ErrorIs(t, err, tunnel.ErrHostKeyFingerprintRequired)
	assert.Equal(t, "ssh_host_key_pin_required", tunnel.PublicError(err))
	assert.NotContains(t, err.Error(), hostname)

	target.SSHHostKeyFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = tunnel.NewSSHDialer().Dial(target)
	require.Error(t, err)
	assert.Equal(t, "ssh_host_key_mismatch", tunnel.PublicError(err))
	assert.NotContains(t, err.Error(), server.Fingerprint)
	assert.NotContains(t, err.Error(), hostname)
}
