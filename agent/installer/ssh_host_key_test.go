// ssh_host_key_test.go 验证 push-over-SSH installer 的 host-key pin 合同。
//
// 职责：
//   - 通过真实 SSH 握手证明正确 pin 可建立 installer Remote
//   - 证明 mismatch 在任何远端命令或上传前被拒绝
//
// 边界：
//   - 不执行安装命令或 SCP 上传
//   - 不连接外部主机
package installer_test

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/testsupport/sshtest"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

func TestNewSSHRemoteRequiresExactTrustedHostKey(t *testing.T) {
	server := sshtest.Start(t)
	hostname, portText, err := net.SplitHostPort(server.Address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	host := model.Host{
		ID:                    "h1",
		SSHHost:               hostname,
		SSHPort:               port,
		SSHUser:               "ops",
		SSHPassword:           "pw",
		SSHHostKeyFingerprint: server.Fingerprint,
	}

	remote, err := installer.NewSSHRemote(host)
	require.NoError(t, err)
	require.NoError(t, remote.Close())

	host.SSHHostKeyFingerprint = ""
	_, err = installer.NewSSHRemote(host)
	require.Error(t, err)
	assert.ErrorIs(t, err, tunnel.ErrHostKeyFingerprintRequired)
	assert.Equal(t, "ssh_host_key_pin_required", tunnel.PublicError(err))
	assert.NotContains(t, err.Error(), hostname)

	host.SSHHostKeyFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = installer.NewSSHRemote(host)
	require.Error(t, err)
	assert.Equal(t, "ssh_host_key_mismatch", tunnel.PublicError(err))
	assert.NotContains(t, err.Error(), server.Fingerprint)
	assert.NotContains(t, err.Error(), hostname)
}
