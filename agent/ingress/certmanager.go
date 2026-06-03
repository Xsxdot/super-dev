// certmanager.go 巡检并续期 Ingress 托管证书。
//
// 职责：
//   - 定期扫描已落地状态中的托管证书
//   - 对进入续期窗口的证书执行 renew
//   - 将续期后的证书重新渲染并分发到入口声明的 host
//
// 边界：
//   - 不申请首次证书，首次申请由 Service.Apply 负责
//   - 不处理已删除声明对应的历史状态
package ingress

import (
	"context"
	"errors"
	"time"

	"github.com/superdev/agent/model"
)

// CertManagerConfig 描述证书续期管理器依赖和时间参数。
type CertManagerConfig struct {
	Store       Store
	Registry    *Registry
	HostLookup  HostLookup
	RenewBefore time.Duration
	Interval    time.Duration
}

// CertManager 负责后台巡检和续期托管证书。
type CertManager struct {
	store       Store
	registry    *Registry
	hostLookup  HostLookup
	renewBefore time.Duration
	interval    time.Duration
}

// NewCertManager 创建证书续期管理器。
//
// 参数：
//   - cfg: Store、Registry、HostLookup 和续期时间参数
//
// 返回：
//   - 可执行 Run/RunOnce 的 CertManager
func NewCertManager(cfg CertManagerConfig) *CertManager {
	hostLookup := cfg.HostLookup
	if hostLookup == nil {
		hostLookup = func(ids []string) ([]model.Host, error) {
			return nil, errors.New("host lookup is required")
		}
	}
	renewBefore := cfg.RenewBefore
	if renewBefore == 0 {
		renewBefore = 30 * 24 * time.Hour
	}
	interval := cfg.Interval
	if interval == 0 {
		interval = 24 * time.Hour
	}
	return &CertManager{
		store:       cfg.Store,
		registry:    cfg.Registry,
		hostLookup:  hostLookup,
		renewBefore: renewBefore,
		interval:    interval,
	}
}

// Run 按固定间隔持续巡检证书续期。
//
// 参数：
//   - ctx: 上下文，用于停止后台循环
//
// 注意：
//   - 单次 RunOnce 失败不会退出循环，下一轮继续尝试
func (m *CertManager) Run(ctx context.Context) {
	_ = m.RunOnce(ctx, time.Now())
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = m.RunOnce(ctx, now)
		}
	}
}

// RunOnce 执行一次托管证书续期巡检。
//
// 参数：
//   - ctx: 上下文，用于取消 provider 调用
//   - now: 当前时间，测试可注入
//
// 返回：
//   - 任一待续期证书处理失败时返回错误
func (m *CertManager) RunOnce(ctx context.Context, now time.Time) error {
	if err := m.ensureReady(); err != nil {
		return err
	}
	states, err := m.store.ListStates()
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.Cert == nil {
			continue
		}
		if state.Cert.ExpiresAt.Sub(now) >= m.renewBefore {
			continue
		}
		if err := m.renewState(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

func (m *CertManager) renewState(ctx context.Context, state AppliedState) error {
	in, ok, err := m.store.GetIngress(state.IngressID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	dnsProvider, err := m.registry.DNS(in.DNS.Provider)
	if err != nil {
		return err
	}
	certProviderName := state.Cert.Provider
	if certProviderName == "" {
		certProviderName = in.TLS.CertProvider
	}
	if certProviderName == "" {
		certProviderName = ProviderACME
	}
	certProvider, err := m.registry.Cert(certProviderName)
	if err != nil {
		return err
	}
	renewed, err := certProvider.Renew(ctx, *state.Cert, dnsProvider)
	if err != nil {
		return err
	}

	proxyProvider, err := m.registry.Proxy(in.ProxyProvider)
	if err != nil {
		return err
	}
	rendered, err := proxyProvider.Render(in, &renewed)
	if err != nil {
		return err
	}
	hosts, err := m.hostLookup(in.HostIDs)
	if err != nil {
		return err
	}
	hostStates := make([]HostState, 0, len(hosts))
	for _, host := range hosts {
		hostState, err := proxyProvider.Apply(ctx, host, rendered)
		if err != nil {
			return err
		}
		hostStates = append(hostStates, hostState)
	}

	state.Cert = &renewed
	state.Hosts = hostStates
	return m.store.SaveState(state)
}

func (m *CertManager) ensureReady() error {
	if m.store == nil {
		return errors.New("ingress store is required")
	}
	if m.registry == nil {
		return errors.New("ingress registry is required")
	}
	return nil
}
