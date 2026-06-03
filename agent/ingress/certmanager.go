// certmanager.go 维护 Ingress 证书续期后台循环。
//
// 职责：
//   - 提供可按固定间隔执行的证书巡检入口
//   - 校验证书管理器运行所需依赖
//   - 在托管证书服务接入后承接自动续期调度
//
// 边界：
//   - 不从 Ingress AppliedState 读取证书材料
//   - 不申请首次证书，首次申请由 SSL 管理页显式触发
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

// CertManager 负责后台证书巡检调度。
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

// RunOnce 执行一次证书续期巡检。
//
// 参数：
//   - ctx: 上下文，用于取消后续证书服务调用
//   - now: 当前时间，测试可注入
//
// 返回：
//   - 任一待续期证书处理失败时返回错误
func (m *CertManager) RunOnce(ctx context.Context, now time.Time) error {
	if err := m.ensureReady(); err != nil {
		return err
	}
	return nil
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
