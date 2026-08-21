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

	"github.com/xsxdot/super-dev/agent/dbprovision"
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
// YAML 字段来自配置文件（如 superdev.yaml），运行时字段不参与序列化，
// 仅在内存中维护。
type Service struct {
	ID        string `json:"id"         yaml:"id"`
	ProjectID string `json:"project_id" yaml:"-"`
	Name      string `json:"name"       yaml:"name"`
	Required  bool   `json:"required"   yaml:"required"`
	Order     int    `json:"order"      yaml:"order"`

	// AINote 是 AI 可见的非敏感运行说明，会出现在普通配置和运行快照中。
	AINote string `json:"ai_note,omitempty" yaml:"ai_note,omitempty"`
	// AuthHint 是 AI 可见的非敏感鉴权提示，仅用于说明登录、换 token 等流程。
	AuthHint string `json:"auth_hint,omitempty" yaml:"auth_hint,omitempty"`

	// Language 是服务的实现语言，属于服务的固有身份。导入/探测时确立，可手动修正。
	// 调试器、（未来的）LSP、任务运行器等都是按此身份适配的下游消费者，language 不为任何单一消费者而存在。
	Language ServiceLanguage `json:"language,omitempty" yaml:"language,omitempty"`

	// Deployments 描述该服务在各环境的运行配置。
	Deployments []Deployment `json:"deployments,omitempty" yaml:"deployments,omitempty"`

	// DebugCredentials 是服务级专属调试凭据(如某服务自己的 api-key)，同 name 覆盖项目级。
	DebugCredentials []DebugCredential `json:"debug_credentials,omitempty" yaml:"debug_credentials,omitempty"`

	// HasDebugCredentials 表示该服务存在可通过 get_debug_credentials 获取的调试凭据。
	HasDebugCredentials bool `json:"has_debug_credentials,omitempty" yaml:"-"`
	// DebugCredentialHints 是可进入普通快照的非敏感凭据元信息，不包含 value。
	DebugCredentialHints []DebugCredentialHint `json:"debug_credential_hints,omitempty" yaml:"-"`

	// 运行时字段，不持久化到配置文件。
	Status ServiceStatus `json:"status"        yaml:"-"`
	PID    int           `json:"pid,omitempty" yaml:"-"`
}

// DebugCredential 是专供 AI 调试取用的凭据(测试账号/密码、服务自定义 api-key 等)。
//
// 职责：
//   - 承载一条供 AI 读取明文使用的调试凭据
//
// 边界：
//   - 与给进程启动用的 RuntimeConfig.Env/EnvVars 相反：env 对 AI 脱敏，本类型对 AI 明文返回
//   - 不在 list_services / 运行态快照中渲染，明文唯一出口是 get_debug_credentials 工具
//   - 安全语义：写入即视为对 AI 授信，不另设开关；不想授信则不填/删除
type DebugCredential struct {
	Name  string `json:"name"  yaml:"name"`  // 标识，如 "test_login" / "internal_api_key"
	Value string `json:"value" yaml:"value"` // 明文值
	Desc  string `json:"desc"  yaml:"desc"`  // 一句自然语言说明用途，AI 据此正确使用
}

// MergedDebugCredential 是合并后的凭据，附带来源标记，供 AI 知道凭据出处。
type MergedDebugCredential struct {
	DebugCredential
	Source       string `json:"source"`        // "project" | "ephemeral_project" | "service" | "ephemeral_service"
	ValuePresent bool   `json:"value_present"` // 允许脱敏证据证明本次读取确有值，不暴露 value/hash
}

// DebugCredentialLayer 描述一层有明确来源和覆盖优先级的调试凭据。
type DebugCredentialLayer struct {
	Credentials []DebugCredential
	Source      string
}

// DebugCredentialHint 是可暴露给 MCP 普通快照的非敏感凭据提示。
//
// 职责：
//   - 告诉 AI 有哪些凭据名称和用途可用，以便主动调用 get_debug_credentials
//
// 边界：
//   - 不包含 Value、token、密码或任何 secret 明文
type DebugCredentialHint struct {
	Name   string `json:"name"`
	Desc   string `json:"desc,omitempty"`
	Source string `json:"source"` // "project" | "ephemeral_project" | "service" | "ephemeral_service"
}

// MergeDebugCredentials 合并项目级与服务级调试凭据。
//
// 参数：
//   - project: 项目级凭据
//   - service: 服务级凭据
//
// 返回：
//   - 合并结果；同 Name 时服务级覆盖项目级，并标记 Source
//
// 注意：
//   - 保持项目级先于服务级追加的顺序，覆盖时原地替换值与来源
func MergeDebugCredentials(project, service []DebugCredential) []MergedDebugCredential {
	return MergeDebugCredentialLayers(
		DebugCredentialLayer{Credentials: project, Source: "project"},
		DebugCredentialLayer{Credentials: service, Source: "service"},
	)
}

// MergeDebugCredentialLayers 按调用方给出的低到高优先级合并多层调试凭据。
//
// 参数：
//   - layers: 每层的凭据和稳定 source；后面的层覆盖前面同名凭据
//
// 返回：
//   - 保持首次出现顺序、同时携带最终来源的合并结果
func MergeDebugCredentialLayers(layers ...DebugCredentialLayer) []MergedDebugCredential {
	total := 0
	for _, layer := range layers {
		total += len(layer.Credentials)
	}
	out := make([]MergedDebugCredential, 0, total)
	indexByName := map[string]int{}
	for _, layer := range layers {
		for _, credential := range layer.Credentials {
			if idx, ok := indexByName[credential.Name]; ok {
				// 后一层代表更具体或更临时的授权，必须原地覆盖而不能同时返回两个同名 secret。
				out[idx] = MergedDebugCredential{DebugCredential: credential, Source: layer.Source, ValuePresent: credential.Value != ""}
				continue
			}
			indexByName[credential.Name] = len(out)
			out = append(out, MergedDebugCredential{DebugCredential: credential, Source: layer.Source, ValuePresent: credential.Value != ""})
		}
	}
	return out
}

// 疑似密钥的作用域取值。前两个是可处置作用域（机器层 local.yaml 有对应
// schema，人可以把值按到本机层）；pipeline 开头的几个是只告警作用域——
// 机器层没有流水线的表达能力，能做的只有把「它会随 git 提交出去」讲清楚。
const (
	// SuspectScopeVariables 是项目级 variables。
	SuspectScopeVariables = "variables"
	// SuspectScopeEnvVars 是某个 service 在某个 env 下的环境变量（三载体合并视图）。
	SuspectScopeEnvVars = "env_vars"
	// SuspectScopePipelineVariables 是项目级流水线的 variables。
	SuspectScopePipelineVariables = "pipeline_variables"
	// SuspectScopePipelineEnvVariables 是流水线在某环境下覆盖的 variables。
	SuspectScopePipelineEnvVariables = "pipeline_env_variables"
	// SuspectScopePipelineStepVariables 是流水线 DAG 自身的 variables。
	SuspectScopePipelineStepVariables = "pipeline_dag_variables"
	// SuspectScopePipelineStepWith 是流水线某个 step 的插件私有参数 with。
	SuspectScopePipelineStepWith = "pipeline_step_with"
	// SuspectScopePipelineSyncCommand 是流水线的 sync_command（远端自取代码的命令）。
	SuspectScopePipelineSyncCommand = "pipeline_sync_command"
)

// SuspectEntry 是一条疑似密钥线索：可能来自项目级 variables、某个 service 在
// 某个 env 下的 env_vars，也可能来自流水线里的自由文本载体。Masked 是脱敏后
// 的值，绝不携带明文——扫描「不挡、只亮」，去留由人决定，但它本身不能变成
// 泄露渠道。
//
// 定义在 model 而非 config 包：Project 要随手带上共享层的告警清单
// （SharedSecretWarnings），而 config 依赖 model，反向依赖会成环。
type SuspectEntry struct {
	Scope   string `json:"scope"`
	Service string `json:"service,omitempty"` // env_vars 时为服务名
	Env     string `json:"env,omitempty"`
	Key     string `json:"key"`
	Masked  string `json:"masked_value"`
	Reason  string `json:"reason"`
	// Pipeline 是 pipeline_* 作用域下的流水线 ID/名称。
	Pipeline string `json:"pipeline,omitempty"`
	// Detail 是 pipeline_step_with 下的步骤定位（phase/step 名），用于让人在
	// 一份几十步的流水线里找得到这一条到底在哪。
	Detail string `json:"detail,omitempty"`
	// WarnOnly 表示该条只能告警、无法处置：机器层 local.yaml 对流水线没有任何
	// schema 表达，用户即便想把它按到本机层也无处可放。迁移对话框据此不给这类
	// 条目渲染去向单选，ApplyMigration 也不会搬动它们。
	WarnOnly bool `json:"warn_only,omitempty"`
}

// Project 表示一个开发项目，包含多个服务定义。
//
// Environments 定义该项目的运行环境列表，每个 Service 的 Deployment
// 通过 EnvName 引用其中一个环境。
type Project struct {
	ID       string `json:"id"`
	Name     string `json:"name"                 yaml:"name"`
	RootPath string `json:"root_path"            yaml:"-"`
	// ConfigFormat 是运行时探测的配置格式（"legacy" | "split"），不持久化，
	// 每次 Loader.Load 重新填充。desktop 用它决定是否显示迁移横幅。
	ConfigFormat string `json:"config_format,omitempty" yaml:"-"`
	// ConfigStaleLegacy 表示该项目在 split 格式下仍并存着一份被忽略的
	// .superdev/config.yaml。运行时探测，不持久化。
	// 典型现场：队友迁移后提交了 project.yaml，本机 pull 下来时旁边还留着自己
	// 那份 gitignore 的 config.yaml——split 胜出，本机的路径与密钥被整份忽略，
	// 服务起不来却没有任何提示。config_format 此时报的是 "split"，横幅不会触发，
	// 所以必须有一个独立的标记把这个状态显式亮出来。
	ConfigStaleLegacy bool `json:"config_stale_legacy,omitempty" yaml:"-"`
	// SharedSecretWarnings 是共享层（project.yaml，入库文件）里被判定为疑似密钥
	// 的条目（值已脱敏）。运行时扫描，不持久化。
	// 「不挡、只亮」：它只负责把「这些值会随 git 提交出去」摆到人眼前，既不阻止
	// 保存也不改变写入内容，去留仍由人决定。
	SharedSecretWarnings []SuspectEntry    `json:"shared_secret_warnings,omitempty" yaml:"-"`
	Variables            map[string]string `json:"variables,omitempty" yaml:"variables,omitempty"`
	Environments         []Environment     `json:"environments,omitempty"`
	Services             []Service         `json:"services"             yaml:"services"`
	// AINote 是 AI 可见的非敏感项目说明，会出现在普通配置和运行快照中。
	AINote string `json:"ai_note,omitempty" yaml:"ai_note,omitempty"`
	// AuthHint 是 AI 可见的非敏感鉴权提示，仅用于说明登录、换 token 等流程。
	AuthHint string `json:"auth_hint,omitempty" yaml:"auth_hint,omitempty"`
	// DebugCredentials 是项目级公共调试凭据(如全系统通用的登录测试账号)，供 AI 取明文使用。
	DebugCredentials []DebugCredential `json:"debug_credentials,omitempty" yaml:"debug_credentials,omitempty"`
	// HasDebugCredentials 表示该项目存在可通过 get_debug_credentials 获取的调试凭据。
	HasDebugCredentials bool `json:"has_debug_credentials,omitempty" yaml:"-"`
	// DebugCredentialHints 是可进入普通快照的非敏感凭据元信息，不包含 value。
	DebugCredentialHints []DebugCredentialHint `json:"debug_credential_hints,omitempty" yaml:"-"`
	Pipelines            []ProjectPipeline     `json:"pipelines,omitempty" yaml:"pipelines,omitempty"`
	// EnvSelectedServiceIDs 按环境名存储该环境下用户选中要启动的服务名列表。
	// key 为 env 名称（如 "dev"、"test"），value 为服务名列表，
	// 从而实现 env 级隔离的选中状态。
	EnvSelectedServiceIDs map[string][]string `json:"env_selected_service_ids,omitempty" yaml:"env_selected_service_ids,omitempty"`
	// HomeHostID 是项目当前归属的主机 ID，空表示归属本机。运行时从
	// projecthome.Store 查询填充，不持久化到 project.yaml——归属描述的是
	// "这台控制面自己怎么消费这个项目"，随 project.yaml 走 git 流动会把
	// A 机的归属强加给 B 机。
	HomeHostID string `json:"home_host_id,omitempty" yaml:"-"`
	// HomeHostName 是 HomeHostID 对应的主机展示名，运行时从 remoteStore 查询
	// 填充。归属主机若已被删除，此字段留空但 HomeHostID 仍保留——优雅降级，
	// 不能让主机被删导致整个项目列表接口出错。
	HomeHostName string `json:"home_host_name,omitempty" yaml:"-"`
	// DataSourceBinding 是该项目的 AI 临时库数据源绑定。
	//
	// 注意：只含数据源名与库名，不含任何密码——密码保存在机器层
	// datasources.json，因此本字段随 project.yaml 入库共享是安全的。
	DataSourceBinding *dbprovision.ProjectBinding `json:"data_source_binding,omitempty" yaml:"data_source_binding,omitempty"`
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
	// Seq 是该 deployment 内的单调逻辑序号，由产生日志的 agent 在采集入口分配。
	// 它是日志的持久身份（期 2b 的流协议缺口检测也基于它）；0 表示旧数据未回填/未分配。
	Seq uint64 `json:"seq,omitempty"`
	// LastSeenAt 是折叠段最近一次出现的时间；nil 表示未折叠或旧数据。
	// 段首行的 Timestamp 保持段开始时间不再被 UPSERT 回写，段的"最新时间"由本字段承载。
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
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
//   - SSHHostKeyFingerprint 可能来自人工预置，也可能来自首次连接时的自动采集；
//     系统有意不区分两种来源（若日后需要「生产机必须人工预置」这类策略，
//     须先补来源字段，无法从现有数据推断）
type Host struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	PublicIP              string   `json:"public_ip,omitempty"`
	PrivateIP             string   `json:"private_ip,omitempty"`
	Tags                  []string `json:"tags"`
	SSHHost               string   `json:"ssh_host,omitempty"`
	SSHPort               int      `json:"ssh_port,omitempty"`
	SSHUser               string   `json:"ssh_user,omitempty"`
	SSHPassword           string   `json:"ssh_password,omitempty"`
	SSHPrivateKey         string   `json:"ssh_private_key,omitempty"`
	SSHHostKeyFingerprint string   `json:"ssh_host_key_fingerprint,omitempty"`
	// DevMachineMode 标记该主机被本控制面当作开发机消费（端口镜像开关）。
	// 这是控制面本地设置：只落本机 hosts.json，不下发节点、不跨控制面同步——
	// 开发机自己的桌面端天然不镜像自己（spec §3）。
	DevMachineMode bool `json:"dev_machine_mode,omitempty"`
	// PendingTunnelInvalidationRevision 与连接目标变更同文件落盘，作为待完成安全审计的 outbox 标记。
	PendingTunnelInvalidationRevision string `json:"pending_tunnel_invalidation_revision,omitempty"`
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
	// PendingTunnelInvalidationRevision 与 tunnel target 变更同文件落盘，供失败重试补齐审计。
	PendingTunnelInvalidationRevision string `json:"pending_tunnel_invalidation_revision,omitempty"`
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
	// InsecureSkipVerify 仅供 nodetransport.NodeRequest.TLSOverride 的「scheme
	// 探测」场景使用：目标机可能已被别的控制面 provision 成自签 HTTPS 监听，
	// 而本机没有对方的 CA，探测想问清「有没有 agent 在听」只能跳过证书校验。
	// json:"-" 保证该字段永不持久化进 agents.json、也不会经 API 下发——
	// 常规带凭据流量绝不允许走不验证证书的 TLS，只有安装守卫探测与纳管
	// 接入通道（本身就是与陌生 agent 的首次接触，尚无信任锚）允许使用。
	InsecureSkipVerify bool `json:"-"`
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
	// RuntimeTypeCommand 表示本机或远程命令运行；默认使用 shell，显式 executable/args 时本机直启。
	RuntimeTypeCommand RuntimeType = "command"
	// RuntimeTypeLanguage 表示本地 managed dev 进程由 Language Runtime Provider
	// 生成执行计划（command 的结构化继任者）。不覆盖 systemd/launchd/docker 等其他基座。
	RuntimeTypeLanguage RuntimeType = "language"
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
	Type    RuntimeType `json:"type" yaml:"type"`
	Command string      `json:"command,omitempty" yaml:"command,omitempty"`
	// Executable 与 Args 为 command runtime 提供不经 shell 的结构化启动契约。
	// Executable 为空时继续使用 Command，保持用户已有 shell 命令的兼容性。
	Executable string            `json:"executable,omitempty" yaml:"executable,omitempty"`
	Args       []string          `json:"args,omitempty" yaml:"args,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	EnvFile    string            `json:"env_file,omitempty" yaml:"env_file,omitempty"`
	EnvVars    map[string]string `json:"env_vars,omitempty" yaml:"env_vars,omitempty"`
	// CWD 是 language runtime 的通用工作目录；语言 runtime 下优先于 WorkingDir。
	CWD string `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	// Env 是 language runtime 下 Start/Debug/Attach 共享的环境变量；优先于 EnvVars。
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	// Config 是由 service.language 判别 schema 的 provider-specific 配置（如 Go 的 program）。
	Config      map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
	ServiceName string         `json:"service_name,omitempty" yaml:"service_name,omitempty"`
	ReleaseDir  string         `json:"release_dir,omitempty" yaml:"release_dir,omitempty"`
	CurrentDir  string         `json:"current_dir,omitempty" yaml:"current_dir,omitempty"`
	ExecStart   string         `json:"exec_start,omitempty" yaml:"exec_start,omitempty"`
	Label       string         `json:"label,omitempty" yaml:"label,omitempty"`
	PlistPath   string         `json:"plist_path,omitempty" yaml:"plist_path,omitempty"`
	Container   string         `json:"container,omitempty" yaml:"container,omitempty"`
	Domain      string         `json:"domain,omitempty" yaml:"domain,omitempty"`
}

// EffectiveCWD 返回 language runtime 首选工作目录，兼容旧字段。
func (r RuntimeConfig) EffectiveCWD() string {
	if r.CWD != "" {
		return r.CWD
	}
	return r.WorkingDir
}

// EffectiveEnv 返回 language runtime 首选环境变量，兼容旧字段。
func (r RuntimeConfig) EffectiveEnv() map[string]string {
	if r.Env != nil {
		return r.Env
	}
	return r.EnvVars
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
	ID    string `json:"id" yaml:"id,omitempty"`
	Name  string `json:"name" yaml:"name"`
	IsDev bool   `json:"is_dev" yaml:"is_dev"`
	Order int    `json:"order" yaml:"order"`
	// AINote 是 AI 可见的非敏感环境说明，会出现在普通配置和运行快照中。
	AINote string `json:"ai_note,omitempty" yaml:"ai_note,omitempty"`
	// AuthHint 是 AI 可见的非敏感鉴权提示，仅用于说明登录、换 token 等流程。
	AuthHint string `json:"auth_hint,omitempty" yaml:"auth_hint,omitempty"`
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

// ReadinessProbe 描述如何探测一个 deployment 是否已就绪。
//
// 职责：
//   - 声明「本服务何时算起来了」，供编排器在放行依赖它的服务前等待
//   - 同时驱动 starting→running 的状态转换
//
// 边界：
//   - 不复用 WebReadinessConfig：那是「浏览器打开前等前端入口」的专用语义，
//     与「后台服务监听端口了吗」会各自演化，绑死会互相掣肘
//   - 首版只支持 http / tcp；为空表示「进程起来即就绪」
type ReadinessProbe struct {
	// Type 为 "http" 或 "tcp"。
	Type string `json:"type" yaml:"type"`
	// Target：http 时为 URL；tcp 时为 host:port。
	Target string `json:"target" yaml:"target"`
	// TimeoutSeconds 为就绪等待上限，<=0 时编排器按默认 30s 处理。
	TimeoutSeconds int `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

// WebAIDebugConfig 描述该 Web 入口是否允许 AI 创建浏览器调试会话。
type WebAIDebugConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// ServiceLanguage 表示服务的实现语言，是服务的一等身份属性。
//
// 由项目目录标记文件探测得出，用户可在服务表单中修正。
// 各类语言相关能力（调试、未来的 LSP/格式化/任务运行）都按此身份适配；
// debug provider 是其中一个消费者，由语言推导，不再单独配置。
type ServiceLanguage string

const (
	LanguageGo     ServiceLanguage = "go"
	LanguageNode   ServiceLanguage = "node"
	LanguagePython ServiceLanguage = "python"
	// JVM 系：Java 与 Kotlin 共用 JVM 运行/调试链路。
	LanguageJava   ServiceLanguage = "java"
	LanguageKotlin ServiceLanguage = "kotlin"
	// 原生系：Rust 与 C/C++ 共用 lldb attach-pid 链路。
	LanguageRust ServiceLanguage = "rust"
	LanguageCpp  ServiceLanguage = "cpp"
)

// Known 返回该语言是否为当前已注册支持的调试语言。
func (l ServiceLanguage) Known() bool {
	switch l {
	case LanguageGo, LanguageNode, LanguagePython,
		LanguageJava, LanguageKotlin, LanguageRust, LanguageCpp:
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
	// CodeDebugProviderJVM 表示用 java-debug/JDWP 调试 JVM 系（Java/Kotlin）服务。
	CodeDebugProviderJVM CodeDebugProvider = "jvm"
	// CodeDebugProviderNative 表示用 lldb-dap 调试原生系（Rust/C/C++）服务。
	CodeDebugProviderNative CodeDebugProvider = "native"
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
//   - adapter 字段只覆盖 DAP adapter 行为，启动入口统一来自 language runtime
//
// 边界：
//   - 不再声明调试开关、provider 和运行态保留等旧配置；provider 由 service.language 推导
//   - 不保存 program/args/working_dir/env_vars，避免 code_debug 成为第二套启动入口
//   - 不保存运行时 session ID
type CodeDebugConfig struct {
	Policy         CodeDebugPolicy `json:"policy,omitempty" yaml:"policy,omitempty"`
	Mode           CodeDebugMode   `json:"mode,omitempty" yaml:"mode,omitempty"`
	AdapterCommand string          `json:"adapter_command,omitempty" yaml:"adapter_command,omitempty"`
	AdapterArgs    []string        `json:"adapter_args,omitempty" yaml:"adapter_args,omitempty"`
	StopOnEntry    bool            `json:"stop_on_entry,omitempty" yaml:"stop_on_entry,omitempty"`
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
	// Ports 声明该 deployment 运行时监听的本机端口（服务自身绑定的端口）。
	// 端口镜像据此建立同端口转发——「不猜端口、不扫描」的唯一事实来源。
	// 属共享层配置（project.yaml 随 git 流动）；为空表示不参与端口镜像。
	Ports []int `json:"ports,omitempty" yaml:"ports,omitempty"`
	// CodeDebug 描述该 deployment 是否允许 AI 打开本机代码调试会话。
	CodeDebug *CodeDebugConfig `json:"code_debug,omitempty" yaml:"code_debug,omitempty"`

	// StartOnBoot 标记该 deployment 是否在 agent 启动时自动拉起。
	// 仅对 location=local + control_mode=managed 生效；其余取值被编排器忽略。
	StartOnBoot bool `json:"start_on_boot,omitempty" yaml:"start_on_boot,omitempty"`

	// DependsOn 声明启动本 deployment 前需先就绪的服务（同项目内 service ID 列表）。
	// 编排器据此拓扑排序，在当前 env 内把 service ID 解析成对应 deployment。
	// 存 ID 而非 name：ID 是稳定主键，免疫改名导致的悬空引用。
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`

	// Readiness 描述「本 deployment 何时算就绪」，供依赖它的服务等待，
	// 也用于驱动 starting→running。为空时进程拉起且未立即退出即视为就绪。
	Readiness *ReadinessProbe `json:"readiness,omitempty" yaml:"readiness,omitempty"`

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
