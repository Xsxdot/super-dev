// Package ingress 提供 SuperDev 入口配置子系统的领域模型。
//
// 职责：
//   - 定义入口声明、DNS 记录、证书、预览和落地状态
//   - 提供声明校验和 DNS 目标推断的纯函数
//
// 边界：
//   - 不访问远端主机、DNS 服务商或证书服务
//   - 不渲染具体 proxy 配置
package ingress

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// ProviderNginx 是第一版内置的 nginx proxy provider 名称。
	ProviderNginx = "nginx"
	// ProviderManual 是只返回人工 DNS 操作指令的 provider 名称。
	ProviderManual = "manual"
	// ProviderACME 是内置 ACME 证书 provider 名称。
	ProviderACME = "acme"
)

// RecordType 表示 DNS 记录类型。
type RecordType string

const (
	// RecordA 表示 IPv4 A 记录。
	RecordA RecordType = "A"
	// RecordAAAA 表示 IPv6 AAAA 记录。
	RecordAAAA RecordType = "AAAA"
	// RecordTXT 表示 TXT 记录，ACME DNS-01 使用该类型。
	RecordTXT RecordType = "TXT"
	// RecordCNAME 表示 CNAME 记录。
	RecordCNAME RecordType = "CNAME"
)

// Duration 用 Go duration 字符串表示时间长度，例如 "60s"。
type Duration struct {
	time.Duration
}

// MarshalJSON 将 duration 编码为 Go duration 字符串。
//
// 返回：
//   - JSON 字符串字节
//   - 编码失败时返回错误
//
// 注意：
//   - 零值编码为空字符串，便于前端表单表达未设置状态
func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return json.Marshal("")
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON 从 Go duration 字符串解码 duration。
//
// 参数：
//   - data: JSON 字符串字节
//
// 返回：
//   - 解析失败时返回错误
//
// 注意：
//   - 空字符串会被解析为零值
func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

// Ingress 描述一个期望存在的对外入口。
type Ingress struct {
	ID           string       `json:"id"`
	ProjectID    string       `json:"project_id"`
	Name         string       `json:"name"`
	Domain       string       `json:"domain"`
	SourceHint   SourceHint   `json:"source_hint,omitempty"`
	Proxy        ProxyConfig  `json:"proxy"`
	Upstreams    []Upstream   `json:"upstreams"`
	ProxyOptions ProxyOptions `json:"proxy_options,omitempty"`
	TLS          TLSConfig    `json:"tls"`
	DNS          DNSConfig    `json:"dns"`
	CreatedAt    time.Time    `json:"created_at,omitempty"`
	UpdatedAt    time.Time    `json:"updated_at,omitempty"`
}

// SourceHint 记录入口默认值由哪个项目流水线线索推断。
type SourceHint struct {
	EnvName    string `json:"env_name,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
	Role       string `json:"role,omitempty"`
	Service    string `json:"service,omitempty"`
}

// ProxyConfig 描述反向代理 provider 和部署目标节点。
type ProxyConfig struct {
	Provider string   `json:"provider"`
	HostIDs  []string `json:"host_ids"`
}

// Upstream 描述 nginx upstream 中一个可编辑 IP:port 行。
type Upstream struct {
	HostID string `json:"host_id,omitempty"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
}

// ProxyOptions 描述 proxy provider 的可配置覆盖项。
type ProxyOptions struct {
	Websocket      bool             `json:"websocket,omitempty"`
	ProxyTimeout   Duration         `json:"proxy_timeout,omitempty"`
	ExtraLocations []LocationOption `json:"extra_locations,omitempty"`
	RawTemplate    string           `json:"raw_template,omitempty"`
}

// LocationOption 描述 nginx location 级别的原始配置片段。
type LocationOption struct {
	Path string `json:"path"`
	Raw  string `json:"raw"`
}

// TLSConfig 描述入口是否启用 TLS 以及引用的全局证书。
type TLSConfig struct {
	Enabled bool   `json:"enabled"`
	CertID  string `json:"cert_id,omitempty"`
}

// DNSConfig 描述服务解析记录的 provider 和目标记录。
type DNSConfig struct {
	Provider string   `json:"provider"`
	Records  []Record `json:"records"`
}

// Record 描述一条 DNS 记录。
type Record struct {
	ID    string     `json:"id,omitempty"`
	Type  RecordType `json:"type"`
	Name  string     `json:"name"`
	Value string     `json:"value"`
	TTL   int        `json:"ttl,omitempty"`
}

// RecordResult 描述一次 DNS 收敛结果。
type RecordResult struct {
	Record       Record   `json:"record"`
	Changed      bool     `json:"changed"`
	Manual       bool     `json:"manual,omitempty"`
	Instructions []string `json:"instructions,omitempty"`
}

// Certificate 描述系统托管的证书材料和过期时间。
type Certificate struct {
	Domain     string    `json:"domain"`
	CertPEM    string    `json:"cert_pem"`
	KeyPEM     string    `json:"key_pem"`
	Issuer     string    `json:"issuer,omitempty"`
	ObtainedAt time.Time `json:"obtained_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Provider   string    `json:"provider"`
}

// ManagedCertificate 描述 agent 级全局托管证书。
type ManagedCertificate struct {
	ID          string            `json:"id"`
	Domains     []string          `json:"domains"`
	Issuer      CertificateIssuer `json:"issuer"`
	DNSProvider string            `json:"dns_provider,omitempty"`
	Status      CertStatus        `json:"status"`
	Material    *Certificate      `json:"material,omitempty"`
	Deployments []CertDeployment  `json:"deployments,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	AutoRenew   bool              `json:"auto_renew"`
	CreatedAt   time.Time         `json:"created_at,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at,omitempty"`
}

// CertificateIssuer 表示证书材料的来源。
type CertificateIssuer string

const (
	// CertificateIssuerACME 表示通过 ACME DNS-01 自动签发。
	CertificateIssuerACME CertificateIssuer = "acme"
	// CertificateIssuerManual 表示用户手动上传 PEM 材料。
	CertificateIssuerManual CertificateIssuer = "manual"
)

// CertStatus 表示托管证书当前状态。
type CertStatus string

const (
	// CertPending 表示证书正在申请或等待材料。
	CertPending CertStatus = "pending"
	// CertActive 表示证书材料可用于入口渲染和部署。
	CertActive CertStatus = "active"
	// CertExpiring 表示证书即将进入续期窗口。
	CertExpiring CertStatus = "expiring"
	// CertFailed 表示最近一次申请或续期失败。
	CertFailed CertStatus = "failed"
)

// CertDeployment 记录证书材料已经部署到哪个 host 和路径。
type CertDeployment struct {
	HostID            string    `json:"host_id"`
	CertPath          string    `json:"cert_path"`
	KeyPath           string    `json:"key_path"`
	PostDeployCommand string    `json:"post_deploy_command,omitempty"`
	SourceType        string    `json:"source_type,omitempty"`
	SourceID          string    `json:"source_id,omitempty"`
	Status            string    `json:"status,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	DeployedAt        time.Time `json:"deployed_at"`
}

// ACMEAccount 保存全局唯一 ACME 账号配置。
type ACMEAccount struct {
	Email        string    `json:"email"`
	DirectoryURL string    `json:"directory_url,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// RenderedConfig 是 proxy provider 渲染出的目标配置。
type RenderedConfig struct {
	Domain      string       `json:"domain"`
	Filename    string       `json:"filename"`
	Content     string       `json:"content"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

// AppliedState 记录一次入口收敛后落地的资源。
type AppliedState struct {
	IngressID string      `json:"ingress_id"`
	Records   []Record    `json:"records,omitempty"`
	Hosts     []HostState `json:"hosts,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// HostState 记录单个 host 上的 proxy 配置和证书路径。
type HostState struct {
	HostID         string          `json:"host_id"`
	ConfigPath     string          `json:"config_path"`
	CertPaths      []string        `json:"cert_paths,omitempty"`
	CertDeployment *CertDeployment `json:"cert_deployment,omitempty"`
}

// OrphanConfig 表示 host 上由 SuperDev 管理但不再属于声明的配置。
type OrphanConfig struct {
	HostID string `json:"host_id"`
	Path   string `json:"path"`
	Domain string `json:"domain"`
}

// OrphanReport 聚合待人工确认删除的 host 配置和 DNS 记录。
type OrphanReport struct {
	Configs []OrphanConfig `json:"configs"`
	Records []Record       `json:"records"`
}

// DNSValueDecision 描述 DNS 记录值推断是否可用以及是否需要用户确认。
type DNSValueDecision struct {
	OK                   bool   `json:"ok"`
	Value                string `json:"value,omitempty"`
	RequiresInput        bool   `json:"requires_input,omitempty"`
	RequiresConfirmation bool   `json:"requires_confirmation,omitempty"`
	Message              string `json:"message,omitempty"`
}

// Validate 校验 Ingress 声明是否具备收敛所需的最小字段。
//
// 返回：
//   - 声明有效时返回 nil
//   - 缺少必要字段或组合不兼容时返回错误
//
// 注意：
//   - manual DNS 无法在 ACME DNS-01 的时间窗口内自动写 TXT 记录，因此直接拒绝
func (in Ingress) Validate() error {
	if strings.TrimSpace(in.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(in.Domain) == "" {
		return errors.New("domain is required")
	}
	if strings.TrimSpace(in.Proxy.Provider) == "" {
		return errors.New("proxy.provider is required")
	}
	if len(in.Proxy.HostIDs) == 0 {
		return errors.New("at least one proxy host is required")
	}
	if len(in.Upstreams) == 0 {
		return errors.New("at least one upstream is required")
	}
	for _, upstream := range in.Upstreams {
		if strings.TrimSpace(upstream.IP) == "" {
			return errors.New("upstream.ip is required")
		}
		if upstream.Port <= 0 {
			return errors.New("upstream.port is required")
		}
	}
	if strings.TrimSpace(in.ProxyOptions.RawTemplate) == "" {
		return errors.New("raw_template is required")
	}
	if strings.TrimSpace(in.DNS.Provider) == "" {
		return errors.New("dns.provider is required")
	}
	if len(in.DNS.Records) == 0 {
		return errors.New("at least one dns record is required")
	}
	if in.TLS.Enabled && strings.TrimSpace(in.TLS.CertID) == "" {
		return errors.New("tls.cert_id is required")
	}
	return nil
}

// ResolveDNSRecordValue 推断或读取服务 A 记录目标值。
//
// 参数：
//   - in: 入口声明，优先使用 in.DNS.Records 的首条显式值
//   - hosts: 声明引用的 host 列表，用于单 host 自动推断公网 IP
//
// 返回：
//   - DNSValueDecision，描述是否可用、是否需要输入或确认
//
// 注意：
//   - 多 host 场景必须显式填写目标值，避免错误地把流量指到单台机器
func ResolveDNSRecordValue(in Ingress, hosts []model.Host) DNSValueDecision {
	if len(in.DNS.Records) > 0 && strings.TrimSpace(in.DNS.Records[0].Value) != "" {
		return DNSValueDecision{OK: true, Value: strings.TrimSpace(in.DNS.Records[0].Value)}
	}
	if len(in.Proxy.HostIDs) != 1 {
		return DNSValueDecision{RequiresInput: true, Message: "dns.record.value is required for multiple hosts"}
	}
	byID := map[string]model.Host{}
	for _, host := range hosts {
		byID[host.ID] = host
	}
	host, ok := byID[in.Proxy.HostIDs[0]]
	if !ok {
		return DNSValueDecision{RequiresInput: true, Message: "dns.record.value is required because host was not found"}
	}
	address := strings.TrimSpace(host.PublicIP)
	if address == "" {
		address = strings.TrimSpace(host.SSHHost)
	}
	ip := net.ParseIP(address)
	if ip == nil || !isPublicIP(ip) {
		return DNSValueDecision{RequiresInput: true, Message: "dns.record.value is required because host address is not a public IP"}
	}
	return DNSValueDecision{OK: true, Value: ip.String(), RequiresConfirmation: true}
}

func isPublicIP(ip net.IP) bool {
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

// WithDefaults 补齐服务解析记录的类型、名称、值和 TTL 默认值。
//
// 参数：
//   - domain: 入口域名，用于缺省记录名
//   - value: 推断或确认后的记录值
//
// 返回：
//   - 补齐默认值后的 Record 副本
func (r Record) WithDefaults(domain string, value string) Record {
	out := r
	if out.Type == "" {
		out.Type = RecordA
	}
	if out.Name == "" {
		out.Name = domain
	}
	if out.Value == "" {
		out.Value = value
	}
	if out.TTL == 0 {
		out.TTL = 300
	}
	return out
}

// DisplayName 返回入口声明的人类可读名称。
//
// 返回：
//   - 显式名称非空时返回 Name
//   - 否则返回 domain/backend 组合摘要
func (in Ingress) DisplayName() string {
	if strings.TrimSpace(in.Name) != "" {
		return in.Name
	}
	if strings.TrimSpace(in.Domain) != "" {
		return in.Domain
	}
	return "unnamed ingress"
}

// MatchCertificate 按域名匹配已有 active 证书，精确匹配优先于通配符匹配。
//
// 参数：
//   - domain: 待匹配域名，大小写不敏感
//   - certs: 候选托管证书列表
//
// 返回：
//   - 命中的证书指针；无匹配时为 nil
//   - 是否命中
//
// 注意：
//   - 通配符只覆盖一级子域，`*.example.com` 不匹配 `example.com` 或 `v1.api.example.com`
func MatchCertificate(domain string, certs []ManagedCertificate) (*ManagedCertificate, bool) {
	normalized := strings.ToLower(strings.TrimSpace(domain))
	if normalized == "" {
		return nil, false
	}
	for i := range certs {
		if certs[i].Status != CertActive {
			continue
		}
		for _, candidate := range certs[i].Domains {
			if strings.ToLower(strings.TrimSpace(candidate)) == normalized {
				return &certs[i], true
			}
		}
	}
	for i := range certs {
		if certs[i].Status != CertActive {
			continue
		}
		for _, candidate := range certs[i].Domains {
			if wildcardMatchesDomain(strings.ToLower(strings.TrimSpace(candidate)), normalized) {
				return &certs[i], true
			}
		}
	}
	return nil, false
}

func wildcardMatchesDomain(pattern string, domain string) bool {
	const prefix = "*."
	if !strings.HasPrefix(pattern, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(pattern, prefix)
	if suffix == "" || domain == suffix || !strings.HasSuffix(domain, "."+suffix) {
		return false
	}
	left := strings.TrimSuffix(domain, "."+suffix)
	return left != "" && !strings.Contains(left, ".")
}
