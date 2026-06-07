// Package remote 提供本机端 Agent 持久化。
//
// 职责：
//   - 读写 agents.json
//   - 按 host_id 管理一台 Host 对应的 Agent 配置
//   - 将 legacy hosts.json 内嵌 agent 抽离到 agents.json
//
// 边界：
//   - 不建立 transport 连接
//   - 不执行安装或 provision
//   - 不向前端暴露 secret token，由 API DTO 控制
package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/xsxdot/super-dev/agent/model"
)

// AgentStore 管理本机 agents.json 的读写和 legacy Host.Agent 抽离。
type AgentStore struct {
	mu         sync.Mutex
	agentsPath string
	hostStore  *Store
}

// NewAgentStore 创建 AgentStore。
//
// 参数：
//   - agentsPath: agents.json 持久化路径
//   - hostStore: legacy 迁移时用于读取和回写 hosts.json，可为空
//
// 返回：
//   - 可直接用于 CRUD 和迁移的 AgentStore
func NewAgentStore(agentsPath string, hostStore *Store) *AgentStore {
	return &AgentStore{agentsPath: agentsPath, hostStore: hostStore}
}

// ListAgents 返回所有已配置 Agent。
func (s *AgentStore) ListAgents() ([]model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadAgents()
}

// AgentByHostID 按 Host ID 查询 Agent。
//
// 返回：
//   - Agent 配置
//   - 是否存在
//   - 读取错误
func (s *AgentStore) AgentByHostID(hostID string) (model.Agent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents, err := s.loadAgents()
	if err != nil {
		return model.Agent{}, false, err
	}
	for _, agent := range agents {
		if agent.HostID == hostID {
			model.ApplyAgentDefaults(&agent)
			return agent, true, nil
		}
	}
	return model.Agent{}, false, nil
}

// UpsertAgent 新增或覆盖同一 Host 的 Agent 配置。
//
// 注意：
//   - 同一 Host 当前只允许一个 Agent，因此 HostID 是持久化主键
//   - Secret 会写入本机 agents.json，但由 API DTO 决定是否向前端暴露
func (s *AgentStore) UpsertAgent(agent model.Agent) (model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	model.ApplyAgentDefaults(&agent)
	agents, err := s.loadAgents()
	if err != nil {
		return model.Agent{}, err
	}
	for i := range agents {
		if agents[i].HostID == agent.HostID {
			agents[i] = agent
			return agent, s.saveAgents(agents)
		}
	}
	agents = append(agents, agent)
	return agent, s.saveAgents(agents)
}

// RemoveAgent 删除指定 Host 上的 Agent 配置。
//
// 注意：
//   - 删除 Agent 不会删除 Host
//   - 不存在时也会回写 agents.json，保持幂等
func (s *AgentStore) RemoveAgent(hostID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents, err := s.loadAgents()
	if err != nil {
		return err
	}
	filtered := agents[:0]
	for _, agent := range agents {
		if agent.HostID != hostID {
			filtered = append(filtered, agent)
		}
	}
	return s.saveAgents(filtered)
}

// MigrateLegacyHostAgents 将旧 hosts.json 中内嵌的 agent 抽离到 agents.json。
//
// 注意：
//   - Host 只接收 legacy tunnel 中缺失的 SSH 登录字段
//   - Agent transport 会去除 SSH/TLS 重复字段
//   - 旧 token 写入 Agent.Secret，TLS 写入 Agent.Security
func (s *AgentStore) MigrateLegacyHostAgents() error {
	if s.hostStore == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	hosts, err := s.loadLegacyHosts()
	if err != nil {
		return err
	}
	agents, err := s.loadAgents()
	if err != nil {
		return err
	}
	exists := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		exists[agent.HostID] = struct{}{}
	}

	changedHosts := false
	for i := range hosts {
		legacy := hosts[i].Agent
		if legacy == nil {
			continue
		}
		host := hosts[i].toHost()
		legacy.applyHostSSH(&host)
		hosts[i].applyHost(host)
		changedHosts = true

		if _, found := exists[hosts[i].ID]; found {
			continue
		}
		agent := legacy.toAgent(hosts[i].ID)
		model.ApplyAgentDefaults(&agent)
		agents = append(agents, agent)
		exists[agent.HostID] = struct{}{}
	}
	if changedHosts {
		cleanHosts := make([]model.Host, 0, len(hosts))
		for i := range hosts {
			host := hosts[i].toHost()
			model.ApplyHostDefaults(&host)
			cleanHosts = append(cleanHosts, host)
		}
		if err := s.hostStore.saveHosts(cleanHosts); err != nil {
			return err
		}
	}
	return s.saveAgents(agents)
}

func (s *AgentStore) loadAgents() ([]model.Agent, error) {
	data, err := os.ReadFile(s.agentsPath)
	if os.IsNotExist(err) {
		return []model.Agent{}, nil
	}
	if err != nil {
		return nil, err
	}
	var agents []model.Agent
	if err := json.Unmarshal(data, &agents); err != nil {
		return nil, err
	}
	for i := range agents {
		model.ApplyAgentDefaults(&agents[i])
	}
	return agents, nil
}

func (s *AgentStore) saveAgents(agents []model.Agent) error {
	if err := os.MkdirAll(filepath.Dir(s.agentsPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.agentsPath, data, 0o600)
}

func (s *AgentStore) loadLegacyHosts() ([]legacyHostRecord, error) {
	data, err := os.ReadFile(s.hostStore.hostsPath)
	if os.IsNotExist(err) {
		return []legacyHostRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []legacyHostRecord
	if err := json.Unmarshal(data, &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

type legacyHostRecord struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	PublicIP      string             `json:"public_ip,omitempty"`
	PrivateIP     string             `json:"private_ip,omitempty"`
	Tags          []string           `json:"tags"`
	SSHHost       string             `json:"ssh_host,omitempty"`
	SSHPort       int                `json:"ssh_port,omitempty"`
	SSHUser       string             `json:"ssh_user,omitempty"`
	SSHPassword   string             `json:"ssh_password,omitempty"`
	SSHPrivateKey string             `json:"ssh_private_key,omitempty"`
	Agent         *legacyAgentRecord `json:"agent,omitempty"`
}

func (h legacyHostRecord) toHost() model.Host {
	return model.Host{
		ID:            h.ID,
		Name:          h.Name,
		PublicIP:      h.PublicIP,
		PrivateIP:     h.PrivateIP,
		Tags:          h.Tags,
		SSHHost:       h.SSHHost,
		SSHPort:       h.SSHPort,
		SSHUser:       h.SSHUser,
		SSHPassword:   h.SSHPassword,
		SSHPrivateKey: h.SSHPrivateKey,
	}
}

func (h *legacyHostRecord) applyHost(host model.Host) {
	h.ID = host.ID
	h.Name = host.Name
	h.PublicIP = host.PublicIP
	h.PrivateIP = host.PrivateIP
	h.Tags = host.Tags
	h.SSHHost = host.SSHHost
	h.SSHPort = host.SSHPort
	h.SSHUser = host.SSHUser
	h.SSHPassword = host.SSHPassword
	h.SSHPrivateKey = host.SSHPrivateKey
	h.Agent = nil
}

type legacyAgentRecord struct {
	Transport legacyTransportConfig `json:"transport"`
	Token     string                `json:"token,omitempty"`
}

func (a legacyAgentRecord) toAgent(hostID string) model.Agent {
	agent := model.Agent{
		HostID: hostID,
		Secret: model.AgentSecret{
			Token: a.Token,
		},
	}
	for _, entry := range a.Transport.entries() {
		switch entry.Type {
		case model.TransportTypeDirect:
			if entry.Direct == nil {
				continue
			}
			agent.Transport.Chain = append(agent.Transport.Chain, model.TransportEntry{
				Type:   model.TransportTypeDirect,
				Direct: &model.DirectParams{Address: entry.Direct.Address},
			})
			applyLegacyDirectTLS(&agent, entry.Direct)
		case model.TransportTypeTunnel:
			if entry.Tunnel == nil {
				continue
			}
			agent.Transport.Chain = append(agent.Transport.Chain, model.TransportEntry{
				Type: model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{
					RemoteAgentPort: entry.Tunnel.RemoteAgentPort,
				},
			})
		default:
			agent.Transport.Chain = append(agent.Transport.Chain, model.TransportEntry{Type: entry.Type})
		}
	}
	return agent
}

func (a legacyAgentRecord) applyHostSSH(host *model.Host) {
	for _, entry := range a.Transport.entries() {
		if entry.Type != model.TransportTypeTunnel || entry.Tunnel == nil {
			continue
		}
		entry.Tunnel.applyHostSSH(host)
		return
	}
}

type legacyTransportConfig struct {
	Chain  []legacyTransportEntry `json:"chain,omitempty"`
	Type   model.TransportType    `json:"type,omitempty"`
	Tunnel *legacyTunnelRecord    `json:"tunnel,omitempty"`
	Direct *legacyDirectRecord    `json:"direct,omitempty"`
}

func (c legacyTransportConfig) entries() []legacyTransportEntry {
	if len(c.Chain) > 0 {
		return c.Chain
	}
	if c.Type == "" {
		return []legacyTransportEntry{}
	}
	return []legacyTransportEntry{{
		Type:   c.Type,
		Tunnel: c.Tunnel,
		Direct: c.Direct,
	}}
}

type legacyTransportEntry struct {
	Type   model.TransportType `json:"type"`
	Tunnel *legacyTunnelRecord `json:"tunnel,omitempty"`
	Direct *legacyDirectRecord `json:"direct,omitempty"`
}

type legacyTunnelRecord struct {
	SSHHost         string `json:"ssh_host"`
	SSHPort         int    `json:"ssh_port"`
	SSHUser         string `json:"ssh_user"`
	SSHPassword     string `json:"ssh_password,omitempty"`
	SSHKeyPath      string `json:"ssh_key_path,omitempty"`
	SSHPrivateKey   string `json:"ssh_private_key,omitempty"`
	RemoteAgentPort int    `json:"remote_agent_port"`
}

func (t legacyTunnelRecord) applyHostSSH(host *model.Host) {
	if host.SSHHost == "" {
		host.SSHHost = t.SSHHost
	}
	if host.SSHPort == 0 {
		host.SSHPort = t.SSHPort
	}
	if host.SSHUser == "" {
		host.SSHUser = t.SSHUser
	}
	if host.SSHPassword == "" {
		host.SSHPassword = t.SSHPassword
	}
	if host.SSHPrivateKey == "" {
		host.SSHPrivateKey = t.SSHPrivateKey
	}
	if host.SSHPrivateKey == "" && t.SSHKeyPath != "" {
		key, err := os.ReadFile(t.SSHKeyPath)
		if err == nil {
			host.SSHPrivateKey = string(key)
		}
	}
}

type legacyDirectRecord struct {
	Address string `json:"address,omitempty"`
	TLS     bool   `json:"tls,omitempty"`
	CACert  string `json:"ca_cert,omitempty"`
}

func applyLegacyDirectTLS(agent *model.Agent, direct *legacyDirectRecord) {
	if direct == nil {
		return
	}
	if !direct.TLS {
		agent.Security.TLS.Mode = model.AgentTLSModeOff
		return
	}
	if direct.CACert != "" {
		agent.Security.TLS.Mode = model.AgentTLSModeManual
		agent.Security.TLS.CACert = direct.CACert
		return
	}
	agent.Security.TLS.Mode = model.AgentTLSModeAuto
}
