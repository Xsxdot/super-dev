// Package model 定义 SuperDev agent 的核心数据模型。
//
// 职责：
//   - 定义服务（Service）、项目（Project）、日志条目（LogEntry）、日志规则（LogRule）等核心结构体
//   - 提供运行时状态常量（ServiceStatus）和规则类型常量（RuleType、RuleLogic）
//
// 边界：
//   - 仅包含纯数据结构定义，不包含业务逻辑
//   - 不依赖任何外部服务或 I/O 操作
//   - 运行时字段（Status、PID）不参与 YAML 序列化
package model

import (
	"encoding/json"
	"time"
)

// ServiceStatus 表示服务的运行状态。
type ServiceStatus string

const (
	// StatusStopped 表示服务已停止（初始状态，对应 Go 零值，无需显式设置）。
	StatusStopped ServiceStatus = ""
	// StatusStarting 表示服务正在启动中。
	StatusStarting ServiceStatus = "starting"
	// StatusRunning 表示服务正在运行。
	StatusRunning ServiceStatus = "running"
	// StatusFailed 表示服务启动或运行失败。
	StatusFailed ServiceStatus = "failed"
)

// RuleType 表示日志过滤规则的类型（包含或排除）。
type RuleType string

// RuleLogic 表示日志过滤规则关键字之间的逻辑关系。
type RuleLogic string

const (
	// RuleTypeInclude 表示该规则为包含规则，只保留匹配的日志。
	RuleTypeInclude RuleType = "include"
	// RuleTypeExclude 表示该规则为排除规则，过滤掉匹配的日志。
	RuleTypeExclude RuleType = "exclude"

	// RuleLogicAND 表示所有关键字都需要匹配。
	RuleLogicAND RuleLogic = "and"
	// RuleLogicOR 表示任意关键字匹配即可。
	RuleLogicOR RuleLogic = "or"
)

// Service 表示一组同名服务的逻辑分组，本身不携带运行配置。
//
// Service 仅作为 Deployment 的容器：一个 Service 在不同环境下对应若干
// Deployment，真正的运行配置（命令、工作目录、环境变量、启停方式等）
// 全部落在 Deployment 上，由 EnvName 区分环境。
//
// YAML 字段来自配置文件（如 superdev.yaml），运行时字段（Status、PID）
// 不参与序列化，仅在内存中维护。
type Service struct {
	ID        string `json:"id"         yaml:"id"`
	ProjectID string `json:"project_id" yaml:"-"`
	Name      string `json:"name"       yaml:"name"`
	Required  bool   `json:"required"   yaml:"required"`
	Order     int    `json:"order"      yaml:"order"`

	// Language 描述服务主要实现语言，用于推导调试器适配。探测得出，可手动修正。
	Language ServiceLanguage `json:"language,omitempty" yaml:"language,omitempty"`

	// Deployments 描述该服务在各环境的运行配置。
	Deployments []Deployment `json:"deployments,omitempty" yaml:"deployments,omitempty"`

	// 运行时字段，不持久化到配置文件。
	Status ServiceStatus `json:"status"        yaml:"-"`
	PID    int           `json:"pid,omitempty" yaml:"-"`
}

// Project 表示一个开发项目，包含多个服务定义。
//
// Environments 定义该项目的运行环境列表，每个 Service 的 Deployment
// 通过 EnvName 引用其中一个环境。
type Project struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"                 yaml:"name"`
	RootPath     string            `json:"root_path"            yaml:"-"`
	Variables    map[string]string `json:"variables,omitempty" yaml:"variables,omitempty"`
	Environments []Environment     `json:"environments,omitempty"`
	Services     []Service         `json:"services"             yaml:"services"`
	Pipelines    []ProjectPipeline `json:"pipelines,omitempty" yaml:"pipelines,omitempty"`
	// EnvSelectedServiceIDs 按环境名存储该环境下用户选中要启动的服务名列表。
	// key 为 env 名称（如 "dev"、"test"），value 为服务名列表，
	// 从而实现 env 级隔离的选中状态。
	EnvSelectedServiceIDs map[string][]string `json:"env_selected_service_ids,omitempty" yaml:"env_selected_service_ids,omitempty"`
}

// LogEntry 表示一条从服务进程捕获的日志记录。
//
// Stream 区分 stdout 和 stderr，RunID 标识本次启动的唯一会话，
// 便于区分同一服务多次运行的日志。
// DeploymentID 标识日志所属的部署单元，是日志的归属标识。
// SourceID 标识日志来源节点的稳定 ID：本机日志为本机 node_id，
// 远程日志为对应 Host 的 ID。取代「没有 host_id 就是本地」的隐式约定。
// Step1 仅添加字段，填充逻辑在 Step2。
type LogEntry struct {
	ID           int64     `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	SourceID     string    `json:"source_id,omitempty"`
	RunID        string    `json:"run_id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Stream       string    `json:"stream"` // stdout / stderr
	// RepeatCount 是该折叠行代表的原始日志条数；未折叠为 1。
	RepeatCount int `json:"repeat_count"`
	// FoldKey 标识折叠段，供实时增量推送时前端对齐"当前折叠行"；
	// 同一段（同 deployment + 同签名 + 时间窗内）共享同一 FoldKey。
	FoldKey string `json:"fold_key,omitempty"`
}

// LogRule 表示一条日志过滤规则。
//
// 规则可启用/禁用（Enabled），Type 决定匹配到的日志是被保留还是过滤，
// Logic 决定多个 Keywords 之间是 AND 还是 OR 关系。
type LogRule struct {
	ID       string    `json:"id"       yaml:"id"`
	Name     string    `json:"name"     yaml:"name"`
	Type     RuleType  `json:"type"     yaml:"type"`
	Keywords []string  `json:"keywords" yaml:"keywords"`
	Logic    RuleLogic `json:"logic"    yaml:"logic"`
	Enabled  bool      `json:"enabled"  yaml:"enabled"`
}

// LogSourceType 表示采集任务的类型。
type LogSourceType string

const (
	// LogSourceTypeJournalctl 表示通过 journalctl 采集 systemd 服务日志。
	LogSourceTypeJournalctl LogSourceType = "journalctl"
	// LogSourceTypeMacOSLog 表示通过 macOS unified logging 采集日志。
	LogSourceTypeMacOSLog LogSourceType = "macos_log"
	// LogSourceTypeDocker 表示通过 docker logs 采集容器日志。
	LogSourceTypeDocker LogSourceType = "docker"
	// LogSourceTypeFileTail 表示通过 tail -F 采集文件日志。
	LogSourceTypeFileTail LogSourceType = "file_tail"
	// LogSourceTypeCommand 表示通过自定义命令采集日志输出。
	LogSourceTypeCommand LogSourceType = "command"
)

// IsValid 判断 LogSourceType 是否在允许的枚举范围内。
func (t LogSourceType) IsValid() bool {
	return t == LogSourceTypeJournalctl ||
		t == LogSourceTypeMacOSLog ||
		t == LogSourceTypeDocker ||
		t == LogSourceTypeFileTail ||
		t == LogSourceTypeCommand
}

// TransportType 表示某台 Host 的 agent 通信方式。
type TransportType string

const (
	// TransportTypeTunnel 表示通过本机 SSH 隧道访问远端 agent。
	TransportTypeTunnel TransportType = "tunnel"
	// TransportTypeDirect 表示通过直连地址访问远端 agent，本阶段只保留数据槽位。
	TransportTypeDirect TransportType = "direct"
	// TransportTypeMQ 表示通过消息队列访问远端 agent，本阶段只保留数据槽位。
	TransportTypeMQ TransportType = "mq"
	// TransportTypeBridge 表示通过桥接服务访问远端 agent，本阶段只保留数据槽位。
	TransportTypeBridge TransportType = "bridge"
)

// AgentHealth 表示远端 agent 的运行健康状态快照。
type AgentHealth string

const (
	// AgentHealthUnknown 表示尚未探活或状态未知。
	AgentHealthUnknown AgentHealth = "unknown"
	// AgentHealthHealthy 表示 agent 可达且接口齐全。
	AgentHealthHealthy AgentHealth = "healthy"
	// AgentHealthUnreachable 表示 agent 不可达。
	AgentHealthUnreachable AgentHealth = "unreachable"
	// AgentHealthVersionMismatch 表示 agent 可达但接口版本不匹配。
	AgentHealthVersionMismatch AgentHealth = "version-mismatch"
	// AgentHealthAuthFailed 表示 agent 可达但 token 认证失败。
	AgentHealthAuthFailed AgentHealth = "auth-failed"
	// AgentHealthPendingBootstrap 表示 agent 可达但仍处于待自举状态。
	AgentHealthPendingBootstrap AgentHealth = "pending-bootstrap"
)

// AgentProvisionState 表示本地记录的远端 agent 安全配置下发状态。
type AgentProvisionState string

const (
	// AgentProvisionStateNotConfigured 表示尚未配置 token 或 TLS。
	AgentProvisionStateNotConfigured AgentProvisionState = "not-configured"
	// AgentProvisionStatePendingBootstrap 表示等待安装命令或 bootstrap token 完成首次下发。
	AgentProvisionStatePendingBootstrap AgentProvisionState = "pending-bootstrap"
	// AgentProvisionStateProvisioned 表示长期 token 和 TLS 配置已经下发。
	AgentProvisionStateProvisioned AgentProvisionState = "provisioned"
)

// AgentTLSMode 表示 agent HTTP 服务的 TLS 策略。
type AgentTLSMode string

const (
	// AgentTLSModeOff 表示 agent 以明文 HTTP 暴露。
	AgentTLSModeOff AgentTLSMode = "off"
	// AgentTLSModeAuto 表示由本机生成/下发自动 TLS 配置。
	AgentTLSModeAuto AgentTLSMode = "auto"
	// AgentTLSModeManual 表示使用用户提供的 CA 和服务名校验远端证书。
	AgentTLSModeManual AgentTLSMode = "manual"
)

const (
	// DefaultSSHPort 是 Host SSH 登录的默认端口。
	DefaultSSHPort = 22
	// DefaultRemoteAgentPort 是远端 agent 默认监听端口。
	DefaultRemoteAgentPort = 57017
	// DefaultAgentListenPort 是 agent 配置的默认监听端口。
	DefaultAgentListenPort = 57017

	// LoopbackBindAddress 是 agent 仅供本机访问时的 bind 地址。
	// tunnel 经 SSH 在远端连 127.0.0.1，verify 在远端 curl 127.0.0.1，
	// 二者都依赖 loopback；链路只含 tunnel 时 bind 此地址即足够且最安全。
	LoopbackBindAddress = "127.0.0.1"
	// PublicBindAddress 是 agent 需被外部直连时的 bind 地址。
	// 链路含 direct 时，桌面端从远端机器之外访问 direct.address，
	// 必须 bind 0.0.0.0 才能既满足外部直连，又保留 loopback 给 tunnel/verify。
	PublicBindAddress = "0.0.0.0"
)

// Host 表示一台被管理的远程主机身份。
//
// 职责：
//   - 保存机器身份、展示名、地址元数据和标签
//   - 保存这台机器的 SSH 登录信息，供安装、隧道和远程执行复用
//
// 边界：
//   - 不感知是否安装 Agent
//   - 不保存 agent token、TLS 或 transport chain
//   - SSHPrivateKey 保存密钥内容，SSHKeyPath 仅可作为 API 导入入口
type Host struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	PublicIP      string   `json:"public_ip,omitempty"`
	PrivateIP     string   `json:"private_ip,omitempty"`
	Tags          []string `json:"tags"`
	SSHHost       string   `json:"ssh_host,omitempty"`
	SSHPort       int      `json:"ssh_port,omitempty"`
	SSHUser       string   `json:"ssh_user,omitempty"`
	SSHPassword   string   `json:"ssh_password,omitempty"`
	SSHPrivateKey string   `json:"ssh_private_key,omitempty"`
}

// Agent 表示安装在某台 Host 上的 SuperDev agent 配置。
//
// HostID 是 Agent 与 Host 的唯一关联；Transport 只保存各传输方式自己的
// 参数，统一监听、安全和私密 token 分别落在 Config、Security 和 Secret。
type Agent struct {
	HostID    string          `json:"host_id"`
	Transport TransportConfig `json:"transport"`
	Config    AgentConfig     `json:"config"`
	Security  AgentSecurity   `json:"security"`
	Secret    AgentSecret     `json:"secret,omitempty"`
	Runtime   AgentRuntime    `json:"-"`
}

// AgentConfig 表示 agent 进程统一监听配置。
type AgentConfig struct {
	ListenAddress string `json:"listen_address,omitempty"`
	ListenPort    int    `json:"listen_port,omitempty"`
}

// AgentSecurity 表示 agent 统一安全配置。
type AgentSecurity struct {
	ProvisionState  AgentProvisionState `json:"provision_state"`
	TokenConfigured bool                `json:"token_configured"`
	TLS             AgentTLSSpec        `json:"tls"`
}

// AgentSecret 表示仅保存在本机 agents.json 的敏感字段。
type AgentSecret struct {
	Token string `json:"token,omitempty"`
}

// AgentTLSSpec 表示 agent HTTP 服务的 TLS 校验配置。
type AgentTLSSpec struct {
	Mode       AgentTLSMode `json:"mode"`
	CACert     string       `json:"ca_cert,omitempty"`
	ServerName string       `json:"server_name,omitempty"`
}

// TransportConfig 持有一台 host 的有序传输链。
type TransportConfig struct {
	Chain []TransportEntry `json:"chain"`
}

// TransportEntry 是链上的一种传输方式及其参数。
type TransportEntry struct {
	Type   TransportType `json:"type"`
	Tunnel *TunnelParams `json:"tunnel,omitempty"`
	Direct *DirectParams `json:"direct,omitempty"`
}

// TunnelParams 是 SSH 隧道传输自己的持久化参数。
type TunnelParams struct {
	RemoteAgentPort int `json:"remote_agent_port"`
}

// DirectParams 是直连传输自己的持久化参数。token 和 TLS 属于 Agent 统一配置。
type DirectParams struct {
	Address string `json:"address,omitempty"`
}

type transportConfigJSON struct {
	Chain  []TransportEntry    `json:"chain,omitempty"`
	Type   TransportType       `json:"type,omitempty"`
	Tunnel *legacyTunnelParams `json:"tunnel,omitempty"`
	Direct *legacyDirectParams `json:"direct,omitempty"`
}

type legacyTunnelParams struct {
	SSHHost         string `json:"ssh_host"`
	SSHPort         int    `json:"ssh_port"`
	SSHUser         string `json:"ssh_user"`
	SSHPassword     string `json:"ssh_password,omitempty"`
	SSHKeyPath      string `json:"ssh_key_path,omitempty"`
	SSHPrivateKey   string `json:"ssh_private_key,omitempty"`
	RemoteAgentPort int    `json:"remote_agent_port"`
}

type legacyDirectParams struct {
	Address string `json:"address,omitempty"`
	TLS     bool   `json:"tls,omitempty"`
	CACert  string `json:"ca_cert,omitempty"`
}

// MarshalJSON 始终写出新的 chain 形状，避免 hosts.json 继续扩散旧单选格式。
func (c TransportConfig) MarshalJSON() ([]byte, error) {
	type chainOnly struct {
		Chain []TransportEntry `json:"chain"`
	}
	chain := c.Chain
	if chain == nil {
		chain = []TransportEntry{}
	}
	return json.Marshal(chainOnly{Chain: chain})
}

// UnmarshalJSON 只为一次性迁移读取旧单选格式；后续保存会统一写回 chain。
func (c *TransportConfig) UnmarshalJSON(data []byte) error {
	var raw transportConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Chain) > 0 {
		c.Chain = raw.Chain
		return nil
	}
	if raw.Type == "" {
		c.Chain = []TransportEntry{}
		return nil
	}
	c.Chain = []TransportEntry{{
		Type:   raw.Type,
		Tunnel: tunnelParamsFromLegacy(raw.Tunnel),
		Direct: directParamsFromLegacy(raw.Direct),
	}}
	return nil
}

func tunnelParamsFromLegacy(params *legacyTunnelParams) *TunnelParams {
	if params == nil {
		return nil
	}
	return &TunnelParams{RemoteAgentPort: params.RemoteAgentPort}
}

func directParamsFromLegacy(params *legacyDirectParams) *DirectParams {
	if params == nil {
		return nil
	}
	return &DirectParams{Address: params.Address}
}

// AgentRuntime 是不持久化的运行时快照。
type AgentRuntime struct {
	Installed bool        `json:"installed"`
	Version   string      `json:"version,omitempty"`
	Health    AgentHealth `json:"health"`
	Reachable bool        `json:"reachable"`
	LocalPort int         `json:"local_port,omitempty"`
}

// TransportEntry 返回指定类型的链项。
func (a Agent) TransportEntry(typ TransportType) (*TransportEntry, bool) {
	for i := range a.Transport.Chain {
		if a.Transport.Chain[i].Type == typ {
			return &a.Transport.Chain[i], true
		}
	}
	return nil, false
}

// TunnelParams 返回 Agent 当前 chain 中的 tunnel 参数。
func (a Agent) TunnelParams() (*TunnelParams, bool) {
	entry, ok := a.TransportEntry(TransportTypeTunnel)
	if !ok || entry.Tunnel == nil {
		return nil, false
	}
	return entry.Tunnel, true
}

// DirectParams 返回 Agent 当前 chain 中的 direct 参数。
func (a Agent) DirectParams() (*DirectParams, bool) {
	entry, ok := a.TransportEntry(TransportTypeDirect)
	if !ok || entry.Direct == nil {
		return nil, false
	}
	return entry.Direct, true
}

// HasDirectTransport 报告 Agent 的传输链是否包含 direct 直连。
//
// 返回：
//   - true 表示链上存在 direct 链项，意味着 agent 需要被远端机器之外直连
//
// 注意：
//   - direct 的有无是 bind 地址自动推导的唯一依据，见 ResolveBindAddress
func (a Agent) HasDirectTransport() bool {
	for _, entry := range a.Transport.Chain {
		if entry.Type == TransportTypeDirect {
			return true
		}
	}
	return false
}

// ResolveBindAddress 根据传输链自动推导 agent 进程应当 bind 的地址。
//
// 返回：
//   - 链路含 direct 时返回 PublicBindAddress(0.0.0.0)，使外部可直连
//   - 否则返回 LoopbackBindAddress(127.0.0.1)，仅暴露给本机的 tunnel/verify
//
// 注意：
//   - bind 不再由用户填写，意图(要不要 direct)决定暴露范围，避免把 loopback
//     这条 tunnel/verify 的生命线误绑到具体 IP 上导致两者失效
//   - bind 0.0.0.0 的安全前提是开启 token 认证，由调用方在安装流程强制保证
func (a Agent) ResolveBindAddress() string {
	if a.HasDirectTransport() {
		return PublicBindAddress
	}
	return LoopbackBindAddress
}

// EnsureTunnelTransport 确保 Agent 拥有 tunnel 链项，并返回可修改的 tunnel 参数。
func (a *Agent) EnsureTunnelTransport() *TunnelParams {
	entry := a.ensureTransportEntry(TransportTypeTunnel)
	if entry.Tunnel == nil {
		entry.Tunnel = &TunnelParams{}
	}
	return entry.Tunnel
}

// EnsureDirectTransport 确保 Agent 拥有 direct 链项，并返回可修改的 direct 参数。
func (a *Agent) EnsureDirectTransport() *DirectParams {
	entry := a.ensureTransportEntry(TransportTypeDirect)
	if entry.Direct == nil {
		entry.Direct = &DirectParams{}
	}
	return entry.Direct
}

func (a *Agent) ensureTransportEntry(typ TransportType) *TransportEntry {
	for i := range a.Transport.Chain {
		if a.Transport.Chain[i].Type == typ {
			return &a.Transport.Chain[i]
		}
	}
	a.Transport.Chain = append(a.Transport.Chain, TransportEntry{Type: typ})
	return &a.Transport.Chain[len(a.Transport.Chain)-1]
}

// ApplyHostDefaults 填充 Host 的展示和 SSH 默认值。
func ApplyHostDefaults(h *Host) {
	if h.Tags == nil {
		h.Tags = []string{}
	}
	if h.SSHPort == 0 {
		h.SSHPort = DefaultSSHPort
	}
}

// ApplyAgentDefaults 填充 Agent 监听、安全和 transport 默认值。
func ApplyAgentDefaults(a *Agent) {
	if a.Transport.Chain == nil {
		a.Transport.Chain = []TransportEntry{}
	}
	if a.Config.ListenPort == 0 {
		a.Config.ListenPort = DefaultAgentListenPort
	}
	if a.Security.ProvisionState == "" {
		if a.Secret.Token != "" {
			a.Security.ProvisionState = AgentProvisionStateProvisioned
			a.Security.TokenConfigured = true
		} else {
			a.Security.ProvisionState = AgentProvisionStatePendingBootstrap
		}
	}
	if a.Security.TLS.Mode == "" {
		a.Security.TLS.Mode = AgentTLSModeAuto
	}
	for i := range a.Transport.Chain {
		entry := &a.Transport.Chain[i]
		if entry.Type != TransportTypeTunnel {
			continue
		}
		if entry.Tunnel == nil {
			entry.Tunnel = &TunnelParams{}
		}
		if entry.Tunnel.RemoteAgentPort == 0 {
			entry.Tunnel.RemoteAgentPort = DefaultRemoteAgentPort
		}
	}
}

// RuntimeLocalPort 返回 Agent 当前运行期本地隧道端口。
func (a Agent) RuntimeLocalPort() int {
	return a.Runtime.LocalPort
}

// SetRuntimeLocalPort 写入 Agent 当前运行期本地隧道端口。
func (a *Agent) SetRuntimeLocalPort(port int) {
	a.Runtime.LocalPort = port
}

// LogSource 表示一个监听任务：在哪些 Host 上以何种 type 采集哪个 name。
//
// Tags 是监听任务自身的标签，与关联 Host 的 Tags 无关。
// ExtraArgs 是追加给采集命令的额外参数（白名单校验后追加），如 ["--since", "1h"]。
// ProjectID 和 ServiceID 是可选的：当设置时，表示该监听任务绑定到某个本地项目/服务；
// 否则（空字符串）表示该任务是独立的远程监听任务。
type LogSource struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Type      LogSourceType `json:"type"`
	HostIDs   []string      `json:"host_ids"`
	Tags      []string      `json:"tags"`
	ExtraArgs []string      `json:"extra_args"`
	ProjectID string        `json:"project_id,omitempty"`
	ServiceID string        `json:"service_id,omitempty"`
}

// DeployLocation 表示 Deployment 的运行位置。
type DeployLocation string

const (
	// LocationLocal 表示服务在本机由 agent 直接管理。
	LocationLocal DeployLocation = "local"
	// LocationRemote 表示服务在远程主机上运行，通过 SSH 隧道采集日志。
	LocationRemote DeployLocation = "remote"
)

// RuntimeType 表示服务在某环境下由哪种运行基座接管生命周期。
type RuntimeType string

const (
	// RuntimeTypeCommand 表示本机或远程 shell 命令运行。
	RuntimeTypeCommand RuntimeType = "command"
	// RuntimeTypeSystemd 表示由 systemd service 接管运行。
	RuntimeTypeSystemd RuntimeType = "systemd"
	// RuntimeTypeLaunchd 表示由 macOS launchd 接管运行。
	RuntimeTypeLaunchd RuntimeType = "launchd"
	// RuntimeTypeDocker 表示由 Docker 容器接管运行。
	RuntimeTypeDocker RuntimeType = "docker"
	// RuntimeTypeNginxStatic 表示由 Nginx 托管静态资源。
	RuntimeTypeNginxStatic RuntimeType = "nginx_static"
	// RuntimeTypeExternal 表示 SuperDev 只观测外部系统托管的服务。
	RuntimeTypeExternal RuntimeType = "external"
)

// ControlMode 表示 agent 对服务运行态的能力边界。
type ControlMode string

const (
	// ControlModeMonitor 表示只监控日志和运行状态，不接管启停。
	ControlModeMonitor ControlMode = "monitor"
	// ControlModeManaged 表示 agent 接管启动、停止、重启，并监控日志和运行状态。
	ControlModeManaged ControlMode = "managed"
)

// LogKind 表示 Deployment 的日志采集方式。
type LogKind string

const (
	// LogKindProcess 表示捕获本机子进程 stdout/stderr。
	LogKindProcess LogKind = "process"
	// LogKindJournalctl 表示通过 journalctl 采集 systemd 日志。
	LogKindJournalctl LogKind = "journalctl"
	// LogKindMacOSLog 表示通过 macOS unified logging 采集日志。
	LogKindMacOSLog LogKind = "macos_log"
	// LogKindDocker 表示通过 docker logs 采集容器日志。
	LogKindDocker LogKind = "docker"
	// LogKindNginx 表示采集 Nginx 访问或错误日志。
	LogKindNginx LogKind = "nginx"
	// LogKindFileTail 表示通过 tail -F 采集文件日志。
	LogKindFileTail LogKind = "file_tail"
	// LogKindCommand 表示通过自定义日志命令采集输出。
	LogKindCommand LogKind = "command"
)

// RuntimeConfig 描述服务在某环境下的运行基座和生命周期参数。
//
// 职责：
//   - 描述服务最终如何被启动、停止、重启
//   - 描述制品被部署后应该放在哪些运行目录
//
// 边界：
//   - 不描述构建、传输、健康检查等部署过程
//   - 不执行命令，仅作为配置模型
type RuntimeConfig struct {
	Type        RuntimeType       `json:"type" yaml:"type"`
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	EnvFile     string            `json:"env_file,omitempty" yaml:"env_file,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty" yaml:"env_vars,omitempty"`
	ServiceName string            `json:"service_name,omitempty" yaml:"service_name,omitempty"`
	ReleaseDir  string            `json:"release_dir,omitempty" yaml:"release_dir,omitempty"`
	CurrentDir  string            `json:"current_dir,omitempty" yaml:"current_dir,omitempty"`
	ExecStart   string            `json:"exec_start,omitempty" yaml:"exec_start,omitempty"`
	Label       string            `json:"label,omitempty" yaml:"label,omitempty"`
	PlistPath   string            `json:"plist_path,omitempty" yaml:"plist_path,omitempty"`
	Container   string            `json:"container,omitempty" yaml:"container,omitempty"`
	Domain      string            `json:"domain,omitempty" yaml:"domain,omitempty"`
}

// LogConfig 描述服务在某环境下的日志采集配置。
//
// 职责：
//   - 统一表达本机进程、journalctl、docker、nginx 等日志来源
//
// 边界：
//   - 不负责日志过滤规则
//   - 不持有运行时游标或订阅状态
type LogConfig struct {
	Type      LogKind  `json:"type" yaml:"type"`
	Target    string   `json:"target,omitempty" yaml:"target,omitempty"`
	Path      string   `json:"path,omitempty" yaml:"path,omitempty"`
	Command   string   `json:"command,omitempty" yaml:"command,omitempty"`
	ExtraArgs []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty"`
}

// PipelineEnvironment 描述项目级流水线在某个环境下覆盖的变量。
type PipelineEnvironment struct {
	Variables map[string]string `json:"variables,omitempty" yaml:"variables,omitempty"`
}

// ProjectPipelineRole 描述项目级流水线角色如何解析到主机列表。
type ProjectPipelineRole struct {
	FromService  string              `json:"from_service,omitempty" yaml:"from_service,omitempty"`
	Hosts        []string            `json:"hosts,omitempty" yaml:"hosts,omitempty"`
	Environments map[string][]string `json:"environments,omitempty" yaml:"environments,omitempty"`
}

// SyncMode 标识构建产物到达目标机的方式。
type SyncMode string

const (
	// SyncModeTransfer 由 agent 打包上传产物到目标机。
	SyncModeTransfer SyncMode = "transfer"
	// SyncModeRemoteCmd 目标机执行命令自行获取代码（如 git clone/pull）。
	SyncModeRemoteCmd SyncMode = "remote_cmd"
)

// ProjectPipeline 描述项目级部署流水线，可一次部署多个服务。
//
// 职责：
//   - 保存构建、传输、部署编排
//   - 保存环境变量覆盖和服务目标角色解析规则
//
// 边界：
//   - 不描述单个服务如何长期运行，该职责属于 Deployment.Runtime
//   - 不保存 Run 状态
type ProjectPipeline struct {
	ID       string   `json:"id" yaml:"id"`
	Name     string   `json:"name" yaml:"name"`
	Services []string `json:"services,omitempty" yaml:"services,omitempty"`
	// ArtifactKind 声明本流水线产出的制品类型，决定引擎如何登记与回滚。
	// 制品位置由保留字变量 artifact 提供：file 时是本地路径，image 时是 registry/image:tag。
	// 为空时按 file 兜底。
	ArtifactKind ArtifactKind                   `json:"artifact_kind,omitempty" yaml:"artifact_kind,omitempty"`
	Variables    map[string]string              `json:"variables,omitempty" yaml:"variables,omitempty"`
	Environments map[string]PipelineEnvironment `json:"environments,omitempty" yaml:"environments,omitempty"`
	Roles        map[string]ProjectPipelineRole `json:"roles,omitempty" yaml:"roles,omitempty"`
	// SyncMode 声明构建产物如何到达部署目标机。
	// transfer = agent 打包上传；remote_cmd = 目标机执行命令（如 git 拉取）自取。
	// 为空时消费方按 transfer 兜底。
	SyncMode SyncMode `json:"sync_mode,omitempty" yaml:"sync_mode,omitempty"`
	// SyncCommand 是 remote_cmd 同步模式下 transfer 步骤未显式配置 remote_cmd 时使用的默认命令。
	SyncCommand string   `json:"sync_command,omitempty" yaml:"sync_command,omitempty"`
	Pipeline    Pipeline `json:"pipeline" yaml:"pipeline"`
}

// Environment 表示一个运行环境定义，集中管理名称、排序和开发标记。
//
// 环境名由用户自由定义（dev / staging / prod ...），不做枚举约束。
// IsDev 为 true 时侧边栏默认展开该分组，其余折叠。
type Environment struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	IsDev bool   `json:"is_dev"`
	Order int    `json:"order"`
}

const (
	// WebReadinessHTTP 表示通过 HTTP GET 检查前端入口是否可访问。
	WebReadinessHTTP = "http"
)

// WebEntrypointConfig 描述本机前端服务可由浏览器打开的入口。
//
// 职责：
//   - 保存 deployment 对外提供的本机 Web URL
//   - 声明是否允许 AI 创建浏览器调试会话
//
// 边界：
//   - 不保存浏览器可执行文件路径，浏览器是本机 agent 设置
//   - 不保存运行时 CDP WebSocket，调试会话由 runtime 管理
type WebEntrypointConfig struct {
	Enabled     bool               `json:"enabled" yaml:"enabled"`
	URL         string             `json:"url,omitempty" yaml:"url,omitempty"`
	DefaultPath string             `json:"default_path,omitempty" yaml:"default_path,omitempty"`
	Readiness   WebReadinessConfig `json:"readiness,omitempty" yaml:"readiness,omitempty"`
	AIDebug     WebAIDebugConfig   `json:"ai_debug,omitempty" yaml:"ai_debug,omitempty"`
}

// WebReadinessConfig 描述打开调试浏览器前如何等待前端入口就绪。
type WebReadinessConfig struct {
	Type           string `json:"type,omitempty" yaml:"type,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

// WebAIDebugConfig 描述该 Web 入口是否允许 AI 创建浏览器调试会话。
type WebAIDebugConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// ServiceLanguage 表示服务的主要实现语言，是调试器适配的依据。
//
// 语言由项目目录标记文件探测得出，用户可在服务表单中修正。
// debug provider 由语言推导，不再单独配置。
type ServiceLanguage string

const (
	LanguageGo     ServiceLanguage = "go"
	LanguageNode   ServiceLanguage = "node"
	LanguagePython ServiceLanguage = "python"
)

// Known 返回该语言是否为当前已注册支持的调试语言。
func (l ServiceLanguage) Known() bool {
	switch l {
	case LanguageGo, LanguageNode, LanguagePython:
		return true
	default:
		return false
	}
}

// CodeDebugProvider 表示代码调试会话使用的语言调试器。
type CodeDebugProvider string

const (
	// CodeDebugProviderGo 表示使用 Delve DAP 调试 Go 服务。
	CodeDebugProviderGo CodeDebugProvider = "go"
	// CodeDebugProviderPython 表示使用 debugpy 调试 Python 服务。
	CodeDebugProviderPython CodeDebugProvider = "python"
	// CodeDebugProviderNode 表示使用 Node DAP adapter 调试 Node 服务。
	CodeDebugProviderNode CodeDebugProvider = "node"
)

// CodeDebugMode 表示代码调试会话的启动方式。
type CodeDebugMode string

const (
	// CodeDebugModeLaunch 表示由 SuperDev 调试会话启动目标进程。
	CodeDebugModeLaunch CodeDebugMode = "launch"
)

// CodeDebugPolicy 描述某 deployment 的 AI 调试放行策略。
//
// 关闭调试是显式操作；开启不是——dev 环境默认放行。
type CodeDebugPolicy string

const (
	// CodeDebugPolicyAuto 缺省：随 Environment.IsDev 放行，非 dev 不放行。
	CodeDebugPolicyAuto CodeDebugPolicy = "auto"
	// CodeDebugPolicyEnabled 显式放行，用于非 dev 环境特批。
	CodeDebugPolicyEnabled CodeDebugPolicy = "enabled"
	// CodeDebugPolicyDisabled 显式关闭此 deployment 的 AI 调试。
	CodeDebugPolicyDisabled CodeDebugPolicy = "disabled"
)

// Valid 返回 policy 是否为合法取值（空视为 auto，合法）。
func (p CodeDebugPolicy) Valid() bool {
	switch p {
	case "", CodeDebugPolicyAuto, CodeDebugPolicyEnabled, CodeDebugPolicyDisabled:
		return true
	default:
		return false
	}
}

// Effective 把空 policy 归一化为 auto。
func (p CodeDebugPolicy) Effective() CodeDebugPolicy {
	if p == "" {
		return CodeDebugPolicyAuto
	}
	return p
}

// CodeDebugConfig 描述本机代码调试能力的可选覆盖项。
//
// 职责：
//   - policy 控制 AI 调试放行（缺省随 dev 环境）
//   - 其余字段是 launch 入口/参数/adapter 的可选 override，缺省由探测与推导补齐
//
// 边界：
//   - 不再声明调试开关、provider 和运行态保留等旧配置；provider 由 service.language 推导
//   - 不保存运行时 session ID
type CodeDebugConfig struct {
	Policy         CodeDebugPolicy   `json:"policy,omitempty" yaml:"policy,omitempty"`
	Mode           CodeDebugMode     `json:"mode,omitempty" yaml:"mode,omitempty"`
	Program        string            `json:"program,omitempty" yaml:"program,omitempty"`
	Args           []string          `json:"args,omitempty" yaml:"args,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty" yaml:"env_vars,omitempty"`
	AdapterCommand string            `json:"adapter_command,omitempty" yaml:"adapter_command,omitempty"`
	AdapterArgs    []string          `json:"adapter_args,omitempty" yaml:"adapter_args,omitempty"`
	StopOnEntry    bool              `json:"stop_on_entry,omitempty" yaml:"stop_on_entry,omitempty"`
}

// Deployment 描述服务在某个环境下的一份具体实例。
//
// 职责：
//   - 描述服务「跑在哪」（local / remote）
//   - 描述「怎么看日志」（本地 buffer / journalctl / docker / tail / command）
//   - 描述「怎么控制运行态」（ControlMode 为 managed 时允许启停）
//
// 边界：
//   - 不负责把代码/构建包传到远程主机（那是部署系统的职责）
//   - 不保存部署流水线，部署编排由 Project.Pipelines 统一承载
//   - 运行时字段（Status、PID）不持久化到配置文件
type Deployment struct {
	ID       string         `json:"id"`
	EnvName  string         `json:"env_name"`
	Location DeployLocation `json:"location"`

	// ControlMode 描述 agent 是只监控，还是接管启停。为空时兼容旧配置。
	ControlMode ControlMode `json:"control_mode,omitempty" yaml:"control_mode,omitempty"`

	// Runtime 描述该服务在当前环境下的运行基座。迁移期保留下面的扁平字段。
	Runtime *RuntimeConfig `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	// Logs 描述该服务在当前环境下的日志采集方式。迁移期保留 LogType 等扁平字段。
	Logs *LogConfig `json:"logs,omitempty" yaml:"logs,omitempty"`
	// Web 描述该 deployment 暴露给本机浏览器的前端入口。
	Web *WebEntrypointConfig `json:"web,omitempty" yaml:"web,omitempty"`
	// CodeDebug 描述该 deployment 是否允许 AI 打开本机代码调试会话。
	CodeDebug *CodeDebugConfig `json:"code_debug,omitempty" yaml:"code_debug,omitempty"`

	// location=local 时使用
	Command string            `json:"command,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	EnvFile string            `json:"env_file,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// location=remote 时使用
	HostIDs   []string      `json:"host_ids,omitempty"`
	LogType   LogSourceType `json:"log_type,omitempty"`
	LogTarget string        `json:"log_target,omitempty"`
	ExtraArgs []string      `json:"extra_args,omitempty"`

	// ReadOnly 是 control_mode 出现前的旧字段，迁移期仅用于读旧配置。
	ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`

	// 远程可选启停命令；是否允许启停由 ReadOnly 显式控制。
	StartCommand string `json:"start_command,omitempty"`
	StopCommand  string `json:"stop_command,omitempty"`

	// 运行时字段，不持久化
	Status ServiceStatus `json:"status" yaml:"-"`
	PID    int           `json:"pid,omitempty" yaml:"-"`
}

// EffectiveControlMode 返回 deployment 的有效运行态控制模式。
//
// control_mode 是新配置的唯一语义来源。为空时兼容旧配置：
// read_only=true 映射为 monitor，否则默认为 managed。
func (d Deployment) EffectiveControlMode() ControlMode {
	if d.ControlMode != "" {
		return d.ControlMode
	}
	if d.ReadOnly {
		return ControlModeMonitor
	}
	return ControlModeManaged
}

// IsReadOnly 报告该 deployment 是否不能被启动、停止或重启。
func (d Deployment) IsReadOnly() bool {
	return d.EffectiveControlMode() == ControlModeMonitor
}

// Collector 是远端 agent 维护的采集任务运行时记录。
//
// 远端不持久化 Collector,仅在内存中保存，配合 process.Manager 跑日志采集进程。
type Collector struct {
	ID           string        `json:"id"` // 由 hash(name+type) 生成，幂等
	Name         string        `json:"name"`
	Type         LogSourceType `json:"type"`
	DeploymentID string        `json:"deployment_id"` // 等于 Collector.ID，作为采集日志的 DeploymentID 归属
	Status       ServiceStatus `json:"status"`
}
