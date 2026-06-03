// provider.go 定义 Ingress 子系统的可插拔 provider 契约。
//
// 职责：
//   - 定义 proxy、DNS、certificate provider 接口
//   - 提供运行时 provider 注册表
//
// 边界：
//   - 不包含任何具体厂商实现
//   - 不读取 provider 凭据文件
package ingress

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/superdev/agent/model"
)

// ErrProviderNotFound 表示按名称无法找到 provider。
var ErrProviderNotFound = errors.New("provider not found")

// ProxyProvider 定义反向代理实现的渲染、落地和检测能力。
type ProxyProvider interface {
	// Name 返回 provider 注册名。
	Name() string
	// Render 将入口声明渲染为目标配置，不接触远端机器。
	Render(ingress Ingress, cert *Certificate) (RenderedConfig, error)
	// Apply 将配置落地到单台 host，并返回落地状态。
	Apply(ctx context.Context, host model.Host, cfg RenderedConfig) (HostState, error)
	// DeployCertificate 只将证书材料落地到单台 host，并返回证书路径。
	DeployCertificate(ctx context.Context, host model.Host, domain string, cert Certificate) (CertDeployment, error)
	// Detect 探测 host 上由 SuperDev 管理但不再属于声明的配置。
	Detect(ctx context.Context, host model.Host, declared []Ingress) ([]OrphanConfig, error)
	// Remove 删除人工确认后的孤儿 proxy 配置。
	Remove(ctx context.Context, host model.Host, orphan OrphanConfig) error
}

// DnsProvider 定义 DNS 记录收敛和检测能力。
type DnsProvider interface {
	// Name 返回 provider 注册名。
	Name() string
	// EnsureRecord 幂等创建或更新一条 DNS 记录。
	EnsureRecord(ctx context.Context, record Record) (RecordResult, error)
	// ListRecords 列出指定域名相关的 DNS 记录。
	ListRecords(ctx context.Context, domain string) ([]Record, error)
	// RemoveRecord 删除人工确认后的 DNS 记录或 ACME 临时 TXT 记录。
	RemoveRecord(ctx context.Context, record Record) error
}

// CertProvider 定义证书申请、续期和过期时间读取能力。
type CertProvider interface {
	// Name 返回 provider 注册名。
	Name() string
	// Obtain 首次申请一张可覆盖多个域名的证书。
	Obtain(ctx context.Context, domains []string, dns DnsProvider) (Certificate, error)
	// Renew 续期已有托管证书。
	Renew(ctx context.Context, cert Certificate, domains []string, dns DnsProvider) (Certificate, error)
	// ExpiresAt 返回证书过期时间。
	ExpiresAt(cert Certificate) time.Time
}

// Registry 保存已装配的 proxy、DNS 和 certificate providers。
type Registry struct {
	mu    sync.RWMutex
	proxy map[string]ProxyProvider
	dns   map[string]DnsProvider
	cert  map[string]CertProvider
}

// NewRegistry 创建空 provider 注册表。
//
// 返回：
//   - 可注册和解析 provider 的 Registry
func NewRegistry() *Registry {
	return &Registry{
		proxy: map[string]ProxyProvider{},
		dns:   map[string]DnsProvider{},
		cert:  map[string]CertProvider{},
	}
}

// RegisterProxy 注册 proxy provider。
//
// 参数：
//   - provider: 待注册 provider；nil 会被忽略
func (r *Registry) RegisterProxy(provider ProxyProvider) {
	if provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proxy[provider.Name()] = provider
}

// RegisterDNS 注册 DNS provider。
//
// 参数：
//   - provider: 待注册 provider；nil 会被忽略
func (r *Registry) RegisterDNS(provider DnsProvider) {
	if provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dns[provider.Name()] = provider
}

// RegisterCert 注册 certificate provider。
//
// 参数：
//   - provider: 待注册 provider；nil 会被忽略
func (r *Registry) RegisterCert(provider CertProvider) {
	if provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cert[provider.Name()] = provider
}

// Proxy 按名称解析 proxy provider。
//
// 参数：
//   - name: provider 注册名
//
// 返回：
//   - 命中的 proxy provider
//   - 未命中时返回 ErrProviderNotFound
func (r *Registry) Proxy(name string) (ProxyProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.proxy[name]
	if !ok {
		return nil, fmt.Errorf("proxy provider %s not found: %w", name, ErrProviderNotFound)
	}
	return provider, nil
}

// DNS 按名称解析 DNS provider。
//
// 参数：
//   - name: provider 注册名
//
// 返回：
//   - 命中的 DNS provider
//   - 未命中时返回 ErrProviderNotFound
func (r *Registry) DNS(name string) (DnsProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.dns[name]
	if !ok {
		return nil, fmt.Errorf("dns provider %s not found: %w", name, ErrProviderNotFound)
	}
	return provider, nil
}

// Cert 按名称解析 certificate provider。
//
// 参数：
//   - name: provider 注册名
//
// 返回：
//   - 命中的 certificate provider
//   - 未命中时返回 ErrProviderNotFound
func (r *Registry) Cert(name string) (CertProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.cert[name]
	if !ok {
		return nil, fmt.Errorf("cert provider %s not found: %w", name, ErrProviderNotFound)
	}
	return provider, nil
}
