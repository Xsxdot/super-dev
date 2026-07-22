// host_test.go 验证 Host HTTP DTO 与领域模型之间的纯转换合同。
//
// 职责：
//   - 锁定安全读视图不会返回 SSH 秘密或原始 fingerprint
//   - 锁定创建与更新时的字段映射、留空保留和显式清除语义
//
// 边界：
//   - 不经过 HTTP handler 或持久化 Store
//   - 不读取 SSH 私钥文件，不校验 fingerprint 格式
package assembler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/api/internal/dto"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestHostAssemblerToViewReturnsOnlySafeCredentialState(t *testing.T) {
	assembler := NewHostAssembler()
	host := model.Host{
		ID:                    "host-1",
		Name:                  "edge",
		Tags:                  nil,
		SSHPassword:           "secret-password",
		SSHPrivateKey:         "private-key",
		SSHHostKeyFingerprint: "SHA256:trusted",
	}

	view := assembler.ToView(host)

	assert.Equal(t, "host-1", view.ID)
	assert.Empty(t, view.Tags)
	assert.True(t, view.SSHCredentialConfigured)
	assert.True(t, view.SSHPasswordConfigured)
	assert.True(t, view.SSHPrivateKeyConfigured)
	assert.True(t, view.SSHHostKeyFingerprintConfigured)
}

func TestHostAssemblerToModelMapsWritableFields(t *testing.T) {
	assembler := NewHostAssembler()
	write := dto.HostWrite{
		ID:                    "host-1",
		Name:                  "edge",
		Tags:                  nil,
		SSHHost:               "ssh.example.com",
		SSHPort:               22,
		SSHUser:               "deploy",
		SSHPassword:           "secret-password",
		SSHPrivateKey:         "private-key",
		SSHHostKeyFingerprint: "SHA256:trusted",
	}

	host := assembler.ToModel(write)

	assert.Equal(t, "host-1", host.ID)
	assert.Empty(t, host.Tags)
	assert.Equal(t, "ssh.example.com", host.SSHHost)
	assert.Equal(t, "deploy", host.SSHUser)
	assert.Equal(t, "secret-password", host.SSHPassword)
	assert.Equal(t, "private-key", host.SSHPrivateKey)
	assert.Equal(t, "SHA256:trusted", host.SSHHostKeyFingerprint)
}

func TestHostAssemblerApplyUpdatePreservesBlankSecretsAndHonorsClearIntent(t *testing.T) {
	assembler := NewHostAssembler()
	existing := model.Host{
		ID:                                "host-1",
		Name:                              "edge",
		SSHPassword:                       "stored-password",
		SSHPrivateKey:                     "stored-private-key",
		SSHHostKeyFingerprint:             "SHA256:stored",
		PendingTunnelInvalidationRevision: "pending-revision",
	}

	preserved := assembler.ApplyUpdate(existing, dto.HostWrite{ID: "ignored", Name: "renamed"})
	cleared := assembler.ApplyUpdate(existing, dto.HostWrite{
		Name:                       "renamed",
		ClearSSHPassword:           true,
		ClearSSHPrivateKey:         true,
		ClearSSHHostKeyFingerprint: true,
	})

	assert.Equal(t, "host-1", preserved.ID)
	assert.Equal(t, "stored-password", preserved.SSHPassword)
	assert.Equal(t, "stored-private-key", preserved.SSHPrivateKey)
	assert.Equal(t, "SHA256:stored", preserved.SSHHostKeyFingerprint)
	assert.Equal(t, "pending-revision", preserved.PendingTunnelInvalidationRevision)
	assert.Empty(t, cleared.SSHPassword)
	assert.Empty(t, cleared.SSHPrivateKey)
	assert.Empty(t, cleared.SSHHostKeyFingerprint)
}
