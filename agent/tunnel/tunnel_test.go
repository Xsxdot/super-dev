// Package tunnel_test 验证隧道配置构造和认证选项选择逻辑。
package tunnel_test

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/tunnel"
	"golang.org/x/crypto/ssh"
)

const testHostKeyFingerprint = "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A"

func TestBuildClientConfigPrefersKey(t *testing.T) {
	// 同时给密钥和密码,应优先用密钥。
	keyContent := dummyEd25519Key(t)
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{
		User:               "ops",
		Password:           "pw",
		PrivateKey:         keyContent,
		HostKeyFingerprint: testHostKeyFingerprint,
	})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigPasswordOnly(t *testing.T) {
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops", Password: "pw", HostKeyFingerprint: testHostKeyFingerprint})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigRequiresAuth(t *testing.T) {
	_, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops", HostKeyFingerprint: testHostKeyFingerprint})
	require.Error(t, err)
}

func TestBuildClientConfigRequiresTrustedHostKeyFingerprint(t *testing.T) {
	_, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops", Password: "pw"})

	require.Error(t, err)
	assert.ErrorIs(t, err, tunnel.ErrHostKeyFingerprintRequired)
}

func TestBuildClientConfigEvaluatesHostKeyTrustBeforeAuthenticationFields(t *testing.T) {
	_, err := tunnel.BuildClientConfig(tunnel.Credentials{})

	require.Error(t, err)
	assert.ErrorIs(t, err, tunnel.ErrHostKeyFingerprintRequired)
	assert.Equal(t, "ssh_host_key_pin_required", tunnel.PublicError(err))
}

func TestHostKeyVerifierAcceptsExactFingerprintAndRecordsSafeIdentity(t *testing.T) {
	cfg, verifier, err := tunnel.BuildClientConfigWithHostKeyEvidence(tunnel.Credentials{
		User:               "ops",
		Password:           "pw",
		HostKeyFingerprint: testHostKeyFingerprint,
	})
	require.NoError(t, err)
	publicKey := testHostPublicKey(t)

	err = cfg.HostKeyCallback("ignored", &net.TCPAddr{}, publicKey)

	require.NoError(t, err)
	assert.Equal(t, tunnel.HostKeyEvidence{
		Verified:       true,
		IdentitySHA256: "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68",
	}, verifier.Evidence())
}

func TestHostKeyVerifierRejectsFingerprintMismatchWithoutLeakingFingerprints(t *testing.T) {
	cfg, verifier, err := tunnel.BuildClientConfigWithHostKeyEvidence(tunnel.Credentials{
		User:               "ops",
		Password:           "pw",
		HostKeyFingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	require.NoError(t, err)

	err = cfg.HostKeyCallback("ignored", &net.TCPAddr{}, testHostPublicKey(t))

	require.Error(t, err)
	assert.True(t, errors.Is(err, tunnel.ErrHostKeyMismatch))
	assert.NotContains(t, err.Error(), testHostKeyFingerprint)
	assert.NotContains(t, err.Error(), "SHA256:AAAAAAAA")
	assert.False(t, verifier.Evidence().Verified)
}

func testHostPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	signer, err := ssh.ParsePrivateKey(dummyEd25519Key(t))
	require.NoError(t, err)
	return signer.PublicKey()
}

// dummyEd25519Key 生成一段合法的 PEM 编码 ed25519 私钥,仅用于测试解析路径。
func dummyEd25519Key(t *testing.T) []byte {
	t.Helper()
	// 来自 ssh-keygen -t ed25519 -N "" -f /tmp/k 的样例。
	return []byte(`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDmYwUYsbsa1nC+8M5wkSU5IjmnE3kxiVtP2DWmaT4afgAAAJBmsXAjZrFw
IwAAAAtzc2gtZWQyNTUxOQAAACDmYwUYsbsa1nC+8M5wkSU5IjmnE3kxiVtP2DWmaT4afg
AAAEBPmTjflrZ0fTzWvBwQH8dlmiapVm9rA0LZAfTvLcRb5OZjBRixuxrWcL7wznCRJTki
OacTeTGJW0/YNaZpPhp+AAAACWp1c3RAdGVzdAECAwQ=
-----END OPENSSH PRIVATE KEY-----
`)
}
