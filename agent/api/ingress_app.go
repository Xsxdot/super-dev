// ingress_app.go 装配 Ingress 子系统在 Agent API 中的运行时依赖。
//
// 职责：
//   - 注册内置 proxy、DNS 和证书 provider
//   - 将持久化 DNS provider 配置恢复到运行时 Registry
//   - 为 ingress.Service 提供 host 查询和远端执行通道适配
//
// 边界：
//   - 不实现具体 provider 协议，只负责组合已有 provider
//   - 不在 API 层执行入口收敛业务逻辑，业务编排交给 ingress.Service
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/superdev/agent/ingress"
	"github.com/superdev/agent/ingress/cert/acme"
	"github.com/superdev/agent/ingress/dns/aliyun"
	"github.com/superdev/agent/ingress/dns/cloudflare"
	"github.com/superdev/agent/ingress/dns/manual"
	"github.com/superdev/agent/ingress/proxy/nginx"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

type ingressPipelineTransport struct {
	runner *pipeline.RoutingRunner
}

func (t ingressPipelineTransport) RunRemote(ctx context.Context, target nginx.Target, cmd string, workDir string, onLine func(string, string)) error {
	if t.runner == nil {
		return errors.New("ingress remote runner is required")
	}
	return t.runner.RunRemote(ctx, pipeline.Target{HostID: target.HostID, HostName: target.HostName}, cmd, workDir, onLine)
}

func (t ingressPipelineTransport) Transfer(ctx context.Context, target nginx.Target, source string, targetPath string, onLine func(string, string)) error {
	if t.runner == nil {
		return errors.New("ingress remote runner is required")
	}
	return t.runner.Transfer(ctx, pipeline.Target{HostID: target.HostID, HostName: target.HostName}, source, targetPath, onLine)
}

func (a *App) initIngress(ctx context.Context) error {
	a.ingressRegistry.RegisterDNS(manual.New())
	a.ingressRegistry.RegisterProxy(nginx.New(a.newIngressRemoteTransport()))
	a.ingressRegistry.RegisterCert(acme.New(func() ingress.ACMEAccount {
		account, err := a.ingressStore.GetACMEAccount()
		if err != nil {
			return ingress.ACMEAccount{}
		}
		return account
	}))
	if err := a.registerStoredIngressDNSProviders(); err != nil {
		return err
	}
	a.ingressService = ingress.NewService(ingress.ServiceConfig{
		Store:      a.ingressStore,
		Registry:   a.ingressRegistry,
		HostLookup: a.lookupIngressHosts,
	})
	certService := ingress.NewCertService(ingress.CertServiceConfig{
		Store:      a.ingressStore,
		Registry:   a.ingressRegistry,
		HostLookup: a.lookupIngressHosts,
	})
	a.ingressCertManager = ingress.NewCertManager(ingress.CertManagerConfig{
		Store:       a.ingressStore,
		CertService: certService,
		RenewBefore: 30 * 24 * time.Hour,
		Interval:    24 * time.Hour,
	})
	go a.ingressCertManager.Run(ctx)
	return nil
}

func (a *App) newIngressRemoteTransport() nginx.RemoteTransport {
	sshExecutor := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		hosts, err := a.remoteStore.ListHosts()
		if err != nil {
			return model.Host{}, false
		}
		for _, host := range hosts {
			if host.ID == hostID {
				return host, true
			}
		}
		return model.Host{}, false
	})
	agentRunner := a.pipelineAgentRunner
	if agentRunner == nil {
		agentRunner = pipeline.NewAgentRunner(a.tunnelResolver)
	}
	return ingressPipelineTransport{runner: pipeline.NewRoutingRunner(a.agentHealth, agentRunner, sshExecutor)}
}

func (a *App) lookupIngressHosts(ids []string) ([]model.Host, error) {
	remoteHosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return nil, err
	}
	remoteByID := map[string]model.Host{}
	for _, host := range remoteHosts {
		remoteByID[host.ID] = host
	}

	hosts := make([]model.Host, 0, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if id == "self" || id == a.identity.NodeID {
			hosts = append(hosts, model.Host{
				ID:      id,
				Name:    a.identity.DisplayName,
				SSHHost: "127.0.0.1",
			})
			continue
		}
		host, ok := remoteByID[id]
		if !ok {
			return nil, fmt.Errorf("host %s not found", id)
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func (a *App) registerStoredIngressDNSProviders() error {
	providers, err := a.ingressStore.ListDNSProviders()
	if err != nil {
		return err
	}
	for _, provider := range providers {
		full, ok, err := a.ingressStore.GetDNSProvider(provider.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := a.registerIngressDNSProvider(full); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) registerIngressDNSProvider(cfg ingress.DNSProviderConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "cloudflare":
		a.ingressRegistry.RegisterDNS(cloudflare.New(cloudflare.Config{
			Name:     cfg.ID,
			ZoneID:   cfg.ZoneID,
			APIToken: secretValue(cfg.Secrets, "api_token", "apiToken", "APIToken"),
		}))
	case "aliyun":
		a.ingressRegistry.RegisterDNS(aliyun.New(aliyun.Config{
			Name:            cfg.ID,
			AccessKeyID:     secretValue(cfg.Secrets, "access_key_id", "accessKeyId", "AccessKeyID"),
			AccessKeySecret: secretValue(cfg.Secrets, "access_key_secret", "accessKeySecret", "AccessKeySecret"),
		}))
	case "manual":
		a.ingressRegistry.RegisterDNS(manual.New())
	default:
		return fmt.Errorf("unsupported DNS provider type %s", cfg.Type)
	}
	return nil
}

func secretValue(secrets map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(secrets[key]); value != "" {
			return value
		}
	}
	return ""
}
