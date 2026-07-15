// agent_dto.go 集中定义 Host 与 Agent 的 HTTP DTO 转换。
//
// 职责：
//   - 将 Host 写请求与不含秘密的 Host API 读视图分离
//   - 将独立 Agent 与 NodeRegistry 快照组合为 Agent API 视图
//   - 统一处理空 tags、运行态和探活结果的协议形状
//
// 边界：
//   - 不读写 remote.Store
//   - 不发起探活或 transport 请求
//   - 不包含安装命令或安装方式逻辑
package api

import (
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type hostWriteDTO struct {
	ID                    string   `json:"id,omitempty"`
	Name                  string   `json:"name"`
	PublicIP              string   `json:"public_ip,omitempty"`
	PrivateIP             string   `json:"private_ip,omitempty"`
	Tags                  []string `json:"tags"`
	SSHHost               string   `json:"ssh_host,omitempty"`
	SSHPort               int      `json:"ssh_port,omitempty"`
	SSHUser               string   `json:"ssh_user,omitempty"`
	SSHPassword           string   `json:"ssh_password,omitempty"`
	SSHPrivateKey         string   `json:"ssh_private_key,omitempty"`
	SSHKeyPath            string   `json:"ssh_key_path,omitempty"`
	SSHHostKeyFingerprint string   `json:"ssh_host_key_fingerprint,omitempty"`
	ClearSSHPassword      bool     `json:"clear_ssh_password,omitempty"`
	ClearSSHPrivateKey    bool     `json:"clear_ssh_private_key,omitempty"`
	ClearSSHHostKeyPin    bool     `json:"clear_ssh_host_key_fingerprint,omitempty"`
}

type hostViewDTO struct {
	ID                              string   `json:"id"`
	Name                            string   `json:"name"`
	PublicIP                        string   `json:"public_ip,omitempty"`
	PrivateIP                       string   `json:"private_ip,omitempty"`
	Tags                            []string `json:"tags"`
	SSHHost                         string   `json:"ssh_host,omitempty"`
	SSHPort                         int      `json:"ssh_port,omitempty"`
	SSHUser                         string   `json:"ssh_user,omitempty"`
	SSHCredentialConfigured         bool     `json:"ssh_credential_configured"`
	SSHPasswordConfigured           bool     `json:"ssh_password_configured"`
	SSHPrivateKeyConfigured         bool     `json:"ssh_private_key_configured"`
	SSHHostKeyFingerprintConfigured bool     `json:"ssh_host_key_fingerprint_configured"`
	IsSelf                          bool     `json:"is_self"`
	NodeID                          string   `json:"node_id,omitempty"`
}

type agentDTO struct {
	HostID    string                    `json:"host_id"`
	HostName  string                    `json:"host_name"`
	Tags      []string                  `json:"tags"`
	Transport model.TransportConfig     `json:"transport"`
	Config    model.AgentConfig         `json:"config"`
	Runtime   model.AgentRuntime        `json:"runtime"`
	Security  agentSecurityDTO          `json:"security"`
	Node      *nodetransport.NodeStatus `json:"node,omitempty"`
	LastError string                    `json:"last_error,omitempty"`
	UpdatedAt *time.Time                `json:"updated_at,omitempty"`
}

type agentSecurityDTO struct {
	TokenConfigured bool               `json:"token_configured"`
	ProvisionState  string             `json:"provision_state"`
	TLS             model.AgentTLSSpec `json:"tls"`
}

type agentCreateDTO struct {
	HostID    string                `json:"host_id"`
	Transport model.TransportConfig `json:"transport"`
	Config    model.AgentConfig     `json:"config"`
	Security  model.AgentSecurity   `json:"security"`
}

type agentTransportUpdateDTO struct {
	Transport model.TransportConfig `json:"transport"`
}

type agentConfigUpdateDTO struct {
	Config   model.AgentConfig   `json:"config"`
	Security model.AgentSecurity `json:"security"`
}

func toHostViewDTO(h model.Host) hostViewDTO {
	passwordConfigured := h.SSHPassword != ""
	privateKeyConfigured := h.SSHPrivateKey != ""
	return hostViewDTO{
		ID:                              h.ID,
		Name:                            h.Name,
		PublicIP:                        h.PublicIP,
		PrivateIP:                       h.PrivateIP,
		Tags:                            normalizeTags(h.Tags),
		SSHHost:                         h.SSHHost,
		SSHPort:                         h.SSHPort,
		SSHUser:                         h.SSHUser,
		SSHCredentialConfigured:         passwordConfigured || privateKeyConfigured,
		SSHPasswordConfigured:           passwordConfigured,
		SSHPrivateKeyConfigured:         privateKeyConfigured,
		SSHHostKeyFingerprintConfigured: h.SSHHostKeyFingerprint != "",
	}
}

func hostFromWriteDTO(dto hostWriteDTO) model.Host {
	return model.Host{
		ID:                    dto.ID,
		Name:                  dto.Name,
		PublicIP:              dto.PublicIP,
		PrivateIP:             dto.PrivateIP,
		Tags:                  normalizeTags(dto.Tags),
		SSHHost:               dto.SSHHost,
		SSHPort:               dto.SSHPort,
		SSHUser:               dto.SSHUser,
		SSHPassword:           dto.SSHPassword,
		SSHPrivateKey:         dto.SSHPrivateKey,
		SSHHostKeyFingerprint: dto.SSHHostKeyFingerprint,
	}
}

func updatedHostFromWriteDTO(existing model.Host, dto hostWriteDTO) model.Host {
	updated := hostFromWriteDTO(dto)
	updated.ID = existing.ID
	// 安全读视图不会回显秘密；编辑表单留空必须保留旧值，避免一次普通改名清除 SSH 能力。
	if dto.ClearSSHPassword {
		updated.SSHPassword = ""
	} else if strings.TrimSpace(dto.SSHPassword) == "" {
		updated.SSHPassword = existing.SSHPassword
	}
	if dto.ClearSSHPrivateKey {
		updated.SSHPrivateKey = ""
	} else if strings.TrimSpace(dto.SSHPrivateKey) == "" && strings.TrimSpace(dto.SSHKeyPath) == "" {
		updated.SSHPrivateKey = existing.SSHPrivateKey
	}
	if dto.ClearSSHHostKeyPin {
		updated.SSHHostKeyFingerprint = ""
	} else if strings.TrimSpace(dto.SSHHostKeyFingerprint) == "" {
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

func toAgentDTO(h model.Host, agent model.Agent, node *nodetransport.NodeStatus) agentDTO {
	model.ApplyAgentDefaults(&agent)
	dto := agentDTO{
		HostID:    h.ID,
		HostName:  h.Name,
		Tags:      normalizeTags(h.Tags),
		Transport: agent.Transport,
		Config:    agent.Config,
		Runtime:   agent.Runtime,
		Security: agentSecurityDTO{
			TokenConfigured: agent.Security.TokenConfigured || agent.Secret.Token != "",
			ProvisionState:  string(agent.Security.ProvisionState),
			TLS:             agent.Security.TLS,
		},
	}
	if node != nil {
		nodeCopy := *node
		dto.Node = &nodeCopy
		dto.Runtime = node.Agent
		dto.LastError = node.Error
		if !node.UpdatedAt.IsZero() {
			updatedAt := node.UpdatedAt
			dto.UpdatedAt = &updatedAt
		}
	}
	return dto
}

func agentRuntimeFromInfo(info agenthealth.Info) model.AgentRuntime {
	runtime := model.AgentRuntime{
		Health:    model.AgentHealth(info.Status),
		Version:   info.Version,
		Reachable: info.Status == agenthealth.StatusHealthy || info.Status == agenthealth.StatusVersionMismatch,
	}
	runtime.Installed = runtime.Reachable
	return runtime
}
