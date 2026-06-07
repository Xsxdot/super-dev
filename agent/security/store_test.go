// Package security_test 验证 agent 自举与长期 token 状态。
package security_test

import (
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
