// Package tunnel_test 验证隧道配置构造和认证选项选择逻辑。
package tunnel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/tunnel"
)

func TestBuildClientConfigPrefersKey(t *testing.T) {
	// 同时给密钥和密码,应优先用密钥。
	keyContent := dummyEd25519Key(t)
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{
		User:       "ops",
		Password:   "pw",
		PrivateKey: keyContent,
	})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigPasswordOnly(t *testing.T) {
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops", Password: "pw"})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigRequiresAuth(t *testing.T) {
	_, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops"})
	require.Error(t, err)
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
