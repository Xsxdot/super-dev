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

import "time"

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

// Service 表示一个受 agent 管理的本地服务进程。
//
// YAML 字段来自配置文件（如 superdev.yaml），运行时字段（Status、PID）
// 不参与序列化，仅在内存中维护。
//
// Deployments 描述该服务在各个环境下的运行配置。
// 新格式（environments + deployments）使用 Deployments；
// 老格式加载后自动迁移，Command/WorkDir 等字段仍保留供兼容。
type Service struct {
	ID        string            `json:"id"         yaml:"id"`
	ProjectID string            `json:"project_id" yaml:"-"`
	Name      string            `json:"name"       yaml:"name"`
	Command   string            `json:"command"    yaml:"command"`
	WorkDir   string            `json:"work_dir"   yaml:"working_dir"`
	Required  bool              `json:"required"   yaml:"required"`
	Order     int               `json:"order"      yaml:"order"`
	EnvFile   string            `json:"env_file,omitempty" yaml:"env_file,omitempty"`
	Env       map[string]string `json:"env,omitempty"      yaml:"env,omitempty"`

	// Deployments 描述该服务在各环境的运行配置（新格式）。
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
	ID                 string        `json:"id"`
	Name               string        `json:"name"                 yaml:"name"`
	RootPath           string        `json:"root_path"            yaml:"-"`
	Environments       []Environment `json:"environments,omitempty"`
	Services           []Service     `json:"services"             yaml:"services"`
	SelectedServiceIDs []string      `json:"selected_service_ids" yaml:"selected_service_ids"`
}

// LogEntry 表示一条从服务进程捕获的日志记录。
//
// Stream 区分 stdout 和 stderr，RunID 标识本次启动的唯一会话，
// 便于区分同一服务多次运行的日志。
// SourceID 标识日志来源节点的稳定 ID：本机日志为本机 node_id，
// 远程日志为对应 Host 的 ID。取代「没有 host_id 就是本地」的隐式约定。
// Step1 仅添加字段，填充逻辑在 Step2。
type LogEntry struct {
	ID        int64     `json:"id"`
	ServiceID string    `json:"service_id"`
	SourceID  string    `json:"source_id,omitempty"`
	RunID     string    `json:"run_id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Stream    string    `json:"stream"` // stdout / stderr
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
	// LogSourceTypeDocker 表示通过 docker logs 采集容器日志。
	LogSourceTypeDocker LogSourceType = "docker"
)

// IsValid 判断 LogSourceType 是否在允许的枚举范围内。
func (t LogSourceType) IsValid() bool {
	return t == LogSourceTypeJournalctl || t == LogSourceTypeDocker
}

// Host 表示一台被监听的远程主机。
//
// 持久化字段会写入 ~/.superdev/hosts.json（权限 0600）。
// LocalTunnelPort 在首次连接时分配并写回，复用同端口便于前端 URL 稳定。
type Host struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	SSHPassword     string   `json:"ssh_password"`
	SSHKeyPath      string   `json:"ssh_key_path"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	Tags            []string `json:"tags"`
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

// Deployment 描述服务在某个环境下的一份具体实例。
//
// 职责：
//   - 描述服务「跑在哪」（local / remote）
//   - 描述「怎么看日志」（本地 buffer / journalctl / docker）
//   - 描述「能不能控制」（StartCommand/StopCommand 均为空则只读）
//
// 边界：
//   - 不负责把代码/构建包传到远程主机（那是部署系统的职责）
//   - 运行时字段（Status、PID）不持久化到配置文件
type Deployment struct {
	ID       string         `json:"id"`
	EnvName  string         `json:"env_name"`
	Location DeployLocation `json:"location"`

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

	// 远程可选启停命令；均为空时该 deployment 只读
	StartCommand string `json:"start_command,omitempty"`
	StopCommand  string `json:"stop_command,omitempty"`

	// 运行时字段，不持久化
	Status ServiceStatus `json:"status" yaml:"-"`
	PID    int           `json:"pid,omitempty" yaml:"-"`
}

// IsReadOnly 报告该 deployment 是否只能查看日志、不能启停。
//
// local deployment 始终可启停；remote deployment 需要同时配置
// StartCommand 和 StopCommand 才能启停，否则为只读。
func (d Deployment) IsReadOnly() bool {
	if d.Location == LocationLocal {
		return false
	}
	return d.StartCommand == "" || d.StopCommand == ""
}

// Collector 是远端 agent 维护的采集任务运行时记录。
//
// 远端不持久化 Collector,仅在内存中保存，配合 process.Manager 跑虚拟 Service。
type Collector struct {
	ID        string        `json:"id"` // 由 hash(name+type) 生成，幂等
	Name      string        `json:"name"`
	Type      LogSourceType `json:"type"`
	ServiceID string        `json:"service_id"` // 等于 Collector.ID，作为虚拟 Service 的 ID
	Status    ServiceStatus `json:"status"`
}
