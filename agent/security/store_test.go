// Package security_test 验证 agent 自举与长期 token 状态。
package security_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

func TestStoreProvisionBurnsBootstrapAndVerifiesToken(t *testing.T) {
	store, err := security.NewStore(filepath.Join(t.TempDir(), "security.json"), security.Options{
		BootstrapToken: "bootstrap",
		RequireAuth:    true,
	})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStatePendingBootstrap, store.State().ProvisionState)

	resp, err := store.Provision("bootstrap", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeOff})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStateProvisioned, resp.ProvisionState)
	assert.True(t, store.VerifyToken("long-token"))
	assert.False(t, store.VerifyBootstrap("bootstrap"))
	assert.False(t, store.VerifyToken("wrong"))
}

func TestStoreProvisionIsIdempotentForSameToken(t *testing.T) {
	store, err := security.NewStore(filepath.Join(t.TempDir(), "security.json"), security.Options{
		BootstrapToken: "bootstrap",
		RequireAuth:    true,
	})
	require.NoError(t, err)

	_, err = store.Provision("bootstrap", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeOff})
	require.NoError(t, err)
	resp, err := store.Provision("bootstrap", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeOff})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStateProvisioned, resp.ProvisionState)
}

func TestStoreRejectsWrongBootstrap(t *testing.T) {
	store, err := security.NewStore(filepath.Join(t.TempDir(), "security.json"), security.Options{
		BootstrapToken: "bootstrap",
		RequireAuth:    true,
	})
	require.NoError(t, err)

	_, err = store.Provision("wrong", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeOff})
	require.ErrorIs(t, err, security.ErrBootstrapRejected)
}

// 重装场景：security.json 残留 provisioned 状态，但启动参数带来新 bootstrap token。
// 修复前 load() 无条件信任磁盘，新 token 被丢弃，provision 永久 401 且重装无法自愈。
func TestStoreAdoptsNewBootstrapTokenOverStaleProvisionedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	store, err := security.NewStore(path, security.Options{BootstrapToken: "old-bootstrap", RequireAuth: true})
	require.NoError(t, err)
	_, err = store.Provision("old-bootstrap", security.ProvisionRequest{Token: "old-long", TLSMode: security.TLSModeAuto})
	require.NoError(t, err)
	require.Equal(t, security.ProvisionStateProvisioned, store.State().ProvisionState)

	// 模拟重装：同一个 data 目录，unit 里换成新的 bootstrap token。
	reinstalled, err := security.NewStore(path, security.Options{BootstrapToken: "new-bootstrap", RequireAuth: true})
	require.NoError(t, err)

	state := reinstalled.State()
	assert.Equal(t, security.ProvisionStatePendingBootstrap, state.ProvisionState)
	assert.True(t, reinstalled.VerifyBootstrap("new-bootstrap"))
	assert.False(t, reinstalled.VerifyBootstrap("old-bootstrap"))
	// 旧长期 token 与 TLS 材料必须一并失效，否则 agent 会继续用旧证书监听 HTTPS。
	assert.False(t, reinstalled.VerifyToken("old-long"))
	assert.Empty(t, state.TokenHash)
	assert.Empty(t, state.TLSMode)
	assert.Empty(t, state.ServerCert)
	assert.Empty(t, state.ServerKey)

	// 新 token 能真正完成自举。
	resp, err := reinstalled.Provision("new-bootstrap", security.ProvisionRequest{Token: "new-long", TLSMode: security.TLSModeOff})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStateProvisioned, resp.ProvisionState)
	assert.True(t, reinstalled.VerifyToken("new-long"))
}

// 普通重启（unit 未变）不得把已完成的 provision 打回 pending，否则每次重启都掉线。
func TestStoreKeepsProvisionedStateOnRestartWithSameBootstrapToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	store, err := security.NewStore(path, security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	_, err = store.Provision("bootstrap", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeAuto})
	require.NoError(t, err)
	caCert := store.State().CACert
	require.NotEmpty(t, caCert)

	// 连续两次重启都必须稳定保持 provisioned。
	for range 2 {
		restarted, err := security.NewStore(path, security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
		require.NoError(t, err)
		state := restarted.State()
		assert.Equal(t, security.ProvisionStateProvisioned, state.ProvisionState)
		assert.True(t, restarted.VerifyToken("long-token"))
		assert.Equal(t, security.TLSModeAuto, state.TLSMode)
		assert.Equal(t, caCert, state.CACert)
	}
}

// 未 provision 前的重启也应保留待自举状态与原 bootstrap token。
func TestStoreKeepsPendingBootstrapOnRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	_, err := security.NewStore(path, security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)

	restarted, err := security.NewStore(path, security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStatePendingBootstrap, restarted.State().ProvisionState)
	assert.True(t, restarted.VerifyBootstrap("bootstrap"))
}

// 真机卡死态回归：旧版本写下的 provisioned + tls_mode=auto，且没有
// consumed_bootstrap_hash（本版本之前的数据形态）。重装下发新 token 时必须重置，
// 否则 agent 继续用旧证书监听 HTTPS 并拒绝新 token —— 就是本次修复的那个卡死。
func TestStoreResetsLegacyProvisionedStateWhenReinstalledWithNewToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	legacy := `{
  "require_auth": true,
  "provision_state": "provisioned",
  "token_hash": "` + sha256Hex("old-long") + `",
  "tls_mode": "auto",
  "ca_cert": "ca",
  "server_cert": "cert",
  "server_key": "key"
}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o600))

	reinstalled, err := security.NewStore(path, security.Options{BootstrapToken: "brand-new", RequireAuth: true})
	require.NoError(t, err)

	state := reinstalled.State()
	assert.Equal(t, security.ProvisionStatePendingBootstrap, state.ProvisionState)
	assert.True(t, reinstalled.VerifyBootstrap("brand-new"))
	assert.False(t, reinstalled.VerifyToken("old-long"))
	// TLS 材料必须清空，否则 agent 仍会以 HTTPS 起监听，
	// 而桌面端 provision 是按明文发的，直接对不上。
	assert.Empty(t, state.TLSMode)
	assert.Empty(t, state.ServerCert)
	assert.Empty(t, state.ServerKey)
	assert.Empty(t, state.CACert)

	// 重置后新 token 必须真的能完成自举。
	_, err = reinstalled.Provision("brand-new", security.ProvisionRequest{Token: "fresh-long", TLSMode: security.TLSModeOff})
	require.NoError(t, err)
	assert.True(t, reinstalled.VerifyToken("fresh-long"))

	// 且此后普通重启保持稳定，不再反复重置。
	restarted, err := security.NewStore(path, security.Options{BootstrapToken: "brand-new", RequireAuth: true})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStateProvisioned, restarted.State().ProvisionState)
	assert.True(t, restarted.VerifyToken("fresh-long"))
}

// 不带 bootstrap token 启动（如手工运行）不得改动磁盘上的既有状态。
func TestStoreWithoutBootstrapTokenKeepsExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	store, err := security.NewStore(path, security.Options{BootstrapToken: "bootstrap", RequireAuth: true})
	require.NoError(t, err)
	_, err = store.Provision("bootstrap", security.ProvisionRequest{Token: "long-token", TLSMode: security.TLSModeOff})
	require.NoError(t, err)

	reopened, err := security.NewStore(path, security.Options{})
	require.NoError(t, err)
	assert.Equal(t, security.ProvisionStateProvisioned, reopened.State().ProvisionState)
	assert.True(t, reopened.VerifyToken("long-token"))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
