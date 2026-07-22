// agent_dto.go 定义 Agent HTTP DTO 与运行态投影转换。
//
// 职责：
//   - 将独立 Agent 与 NodeRegistry 快照组合为 Agent API 视图
//   - 统一处理空 tags、运行态和探活结果的协议形状
//
// 边界：
//   - Host DTO 与模型转换由 internal/dto 和 internal/assembler 负责
//   - 不读写 remote.Store
//   - 不发起探活或 transport 请求
//   - 不包含安装命令或安装方式逻辑
package api

import (
	"time"

	"github.com/xsxdot/super-dev/agent/agenthealth"
	apidto "github.com/xsxdot/super-dev/agent/api/internal/dto"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type hostWriteDTO = apidto.HostWrite
type hostViewDTO = apidto.HostView

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
