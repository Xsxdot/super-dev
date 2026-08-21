// types.go —— AI 临时资源供给层的公共类型与错误定义。
//
// 职责：集中定义 Registry、Provisioner、LeaseManager 之间的稳定数据契约。
// 边界：只描述数据与错误，不执行外部 I/O，也不依赖 HTTP、MCP 或具体存储实现。
package dbprovision

import (
	"errors"
	"time"
)

// 资源类型标识。新增类型时在此追加常量，并在对应实现里 RegisterProvisioner。
const (
	KindPostgres = "postgres"
	KindRedis    = "redis"
)

// 供给层的错误哨兵。api / mcp 层据此映射错误码，不要用字符串匹配。
var (
	// ErrQuotaExceeded 表示项目并发租约数已达上限。
	ErrQuotaExceeded = errors.New("test database quota exceeded")
	// ErrTemplateBusy 表示模板库有活跃连接且项目禁用了断连。
	ErrTemplateBusy = errors.New("template database is busy")
	// ErrNoFreeDB 表示 Redis 实例上没有可分配的空闲 db 号。
	ErrNoFreeDB = errors.New("no free redis db")
	// ErrBindingMissing 表示项目尚未绑定数据源。
	ErrBindingMissing = errors.New("project has no data source binding")
	// ErrDataSourceNotFound 表示按名字或 ID 找不到数据源登记。
	ErrDataSourceNotFound = errors.New("data source not found")
	// ErrLeaseNotFound 表示租约不存在或已释放。
	ErrLeaseNotFound = errors.New("lease not found")
	// ErrLeaseLifetimeExceeded 表示租约已达绝对寿命上限，不能再续。
	ErrLeaseLifetimeExceeded = errors.New("lease absolute lifetime exceeded")
	// ErrUnsupportedKind 表示没有注册对应 kind 的 Provisioner。
	ErrUnsupportedKind = errors.New("unsupported resource kind")
	// ErrDataSourceInUse 表示数据源上仍有活跃租约，不能移除。
	ErrDataSourceInUse = errors.New("data source still has active leases")
)

// DataSource 是一条管理连接登记。
//
// 注意：Password 是明文，只允许出现在本机 datasources.json（0600）与进程内存中；
// 任何对外结构（HTTP 响应、日志、审计）都必须先经 Sanitized() 脱敏。
type DataSource struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	User      string            `json:"user,omitempty"`
	Password  string            `json:"password,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
	Probe     ProbeResult       `json:"probe"`
	Source    string            `json:"source"`
	CreatedAt time.Time         `json:"created_at"`
}

// Sanitized 返回一份把密码抹成空的副本，供对外输出使用。
func (d DataSource) Sanitized() DataSource {
	clone := d
	clone.Password = ""
	return clone
}

// ProbeResult 是登记时或重探时的连通性与能力探测结果。
type ProbeResult struct {
	OK           bool              `json:"ok"`
	CheckedAt    time.Time         `json:"checked_at"`
	ServerVer    string            `json:"server_version,omitempty"`
	Capabilities map[string]bool   `json:"capabilities,omitempty"`
	Facts        map[string]string `json:"facts,omitempty"`
	Missing      []string          `json:"missing,omitempty"`
	FixHint      string            `json:"fix_hint,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// ProjectBinding 是项目与数据源的绑定，随 project.yaml 共享，不含任何密码。
type ProjectBinding struct {
	Postgres            *PostgresBinding `json:"postgres,omitempty" yaml:"postgres,omitempty"`
	Redis               *RedisBinding    `json:"redis,omitempty" yaml:"redis,omitempty"`
	MaxConcurrentLeases int              `json:"max_concurrent_leases,omitempty" yaml:"max_concurrent_leases,omitempty"`
	DefaultTTLMinutes   int              `json:"default_ttl_minutes,omitempty" yaml:"default_ttl_minutes,omitempty"`
}

// PostgresBinding 是项目的 PG 绑定。
type PostgresBinding struct {
	DataSourceName       string `json:"datasource_name" yaml:"datasource_name"`
	DevDatabase          string `json:"dev_database" yaml:"dev_database"`
	TerminateConnections bool   `json:"terminate_connections" yaml:"terminate_connections"`
}

// RedisBinding 是项目的 Redis 绑定。
type RedisBinding struct {
	DataSourceName string `json:"datasource_name" yaml:"datasource_name"`
}

// PlanRequest 是 Plan 的输入，由 LeaseManager 从项目绑定与 acquire 参数组装。
type PlanRequest struct {
	ProjectID  string
	NameSeed   string
	Binding    ProjectBinding
	TakenHints []string
}

// Plan 描述「本次将要做什么」，供审批门禁与 dry-run 消费。
//
// 注意：Plan 只做只读探测，实现不得在其中产生任何副作用。
type Plan struct {
	Kind         string            `json:"kind"`
	ResourceName string            `json:"resource_name"`
	Steps        []string          `json:"steps"`
	SideEffects  []SideEffect      `json:"side_effects,omitempty"`
	Detail       map[string]string `json:"detail,omitempty"`
}

// SideEffectTerminateConnections 是唯一一种对既有运行态可见的副作用。
const SideEffectTerminateConnections = "terminate_connections"

// SideEffect 是对既有运行态的可见影响，非空即触发审批门禁。
type SideEffect struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	Count  int    `json:"count"`
}

// Resource 是一个已供给的资源实例。
type Resource struct {
	Kind string            `json:"kind"`
	Name string            `json:"name"`
	DSN  string            `json:"dsn,omitempty"`
	Meta map[string]string `json:"meta,omitempty"`
}

// WithoutSecret 返回一份清空 DSN 的副本，供落盘、日志与列表接口使用。
func (r Resource) WithoutSecret() Resource {
	clone := r
	clone.DSN = ""
	return clone
}

// Orphan 是对账发现的、登记表之外的残留资源。
type Orphan struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AcquireRequest 是一次临时资源申请。
type AcquireRequest struct {
	ProjectID string
	Purpose   string
	TTL       time.Duration
	Kinds     []string
}

// Lease 是一次申请产出的租约，含一组共享生命周期的资源。
type Lease struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Purpose    string     `json:"purpose"`
	Resources  []Resource `json:"resources"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RenewCount int        `json:"renew_count"`
}

// DryRunResult 是试跑结果，DSN 已脱敏。
type DryRunResult struct {
	Plans      []Plan   `json:"plans"`
	MaskedDSNs []string `json:"masked_dsns"`
	Succeeded  bool     `json:"succeeded"`
	Error      string   `json:"error,omitempty"`
}

// ReconcileReport 是一次对账的产出。
type ReconcileReport struct {
	ExpiredReclaimed int      `json:"expired_reclaimed"`
	OrphansReclaimed []Orphan `json:"orphans_reclaimed"`
	Errors           []string `json:"errors,omitempty"`
}
