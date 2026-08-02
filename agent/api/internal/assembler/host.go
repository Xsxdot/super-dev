// Package assembler 负责 Agent HTTP DTO 与领域模型之间的纯字段转换。
//
// 职责：
//   - 将 Host 领域模型投影为不含秘密的安全读视图
//   - 将 Host 写请求转换为创建模型或合并到已有模型
//
// 边界：
//   - 不访问 Store、文件系统、网络或 tunnel runtime
//   - 不校验 fingerprint 格式，不导入 SSH 私钥文件
package assembler

import (
	"strings"

	"github.com/xsxdot/super-dev/agent/api/internal/dto"
	"github.com/xsxdot/super-dev/agent/model"
)

// HostAssembler 转换 Host HTTP DTO 与领域模型。
type HostAssembler struct{}

// NewHostAssembler 创建无状态 Host Assembler。
//
// 返回：
//   - 可复用的 HostAssembler
func NewHostAssembler() *HostAssembler {
	return &HostAssembler{}
}

// ToView 将 Host 模型投影为安全读视图。
//
// 参数：
//   - host: 已持久化的 Host 领域模型
//
// 返回：
//   - 不含 SSH 秘密和原始 fingerprint 的 HostView
func (a *HostAssembler) ToView(host model.Host) dto.HostView {
	passwordConfigured := host.SSHPassword != ""
	privateKeyConfigured := host.SSHPrivateKey != ""
	return dto.HostView{
		ID:                              host.ID,
		Name:                            host.Name,
		PublicIP:                        host.PublicIP,
		PrivateIP:                       host.PrivateIP,
		Tags:                            normalizeTags(host.Tags),
		SSHHost:                         host.SSHHost,
		SSHPort:                         host.SSHPort,
		SSHUser:                         host.SSHUser,
		SSHCredentialConfigured:         passwordConfigured || privateKeyConfigured,
		SSHPasswordConfigured:           passwordConfigured,
		SSHPrivateKeyConfigured:         privateKeyConfigured,
		SSHHostKeyFingerprintConfigured: host.SSHHostKeyFingerprint != "",
		DevMachineMode:                  host.DevMachineMode,
	}
}

// ToModel 将 Host 写请求转换为新领域模型。
//
// 参数：
//   - write: 已完成边界校验的 Host 写请求
//
// 返回：
//   - 可用于创建的 Host 模型
//
// 注意：
//   - SSHKeyPath 是文件导入指令，不属于持久化模型，因此不会在此映射
func (a *HostAssembler) ToModel(write dto.HostWrite) model.Host {
	return model.Host{
		ID:                    write.ID,
		Name:                  write.Name,
		PublicIP:              write.PublicIP,
		PrivateIP:             write.PrivateIP,
		Tags:                  normalizeTags(write.Tags),
		SSHHost:               write.SSHHost,
		SSHPort:               write.SSHPort,
		SSHUser:               write.SSHUser,
		SSHPassword:           write.SSHPassword,
		SSHPrivateKey:         write.SSHPrivateKey,
		SSHHostKeyFingerprint: write.SSHHostKeyFingerprint,
		DevMachineMode:        write.DevMachineMode,
	}
}

// ApplyUpdate 将 Host 写请求合并到已有领域模型。
//
// 参数：
//   - existing: 已持久化的 Host 模型
//   - write: 更新请求
//
// 返回：
//   - 保留稳定 ID 并应用秘密更新语义的新 Host 模型
//
// 注意：
//   - 安全读视图不会回显秘密；更新请求留空必须保留旧值
//   - 只有显式 clear 字段才能清除已有秘密或 fingerprint
func (a *HostAssembler) ApplyUpdate(existing model.Host, write dto.HostWrite) model.Host {
	updated := a.ToModel(write)
	updated.ID = existing.ID
	// outbox 标记属于持久化内部状态，HTTP 写 DTO 无权覆盖或清除。
	updated.PendingTunnelInvalidationRevision = existing.PendingTunnelInvalidationRevision
	if write.ClearSSHPassword {
		updated.SSHPassword = ""
	} else if strings.TrimSpace(write.SSHPassword) == "" {
		updated.SSHPassword = existing.SSHPassword
	}
	if write.ClearSSHPrivateKey {
		updated.SSHPrivateKey = ""
	} else if strings.TrimSpace(write.SSHPrivateKey) == "" && strings.TrimSpace(write.SSHKeyPath) == "" {
		updated.SSHPrivateKey = existing.SSHPrivateKey
	}
	if write.ClearSSHHostKeyFingerprint {
		updated.SSHHostKeyFingerprint = ""
	} else if strings.TrimSpace(write.SSHHostKeyFingerprint) == "" {
		updated.SSHHostKeyFingerprint = existing.SSHHostKeyFingerprint
	}
	return updated
}

func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
