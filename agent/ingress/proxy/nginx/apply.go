// apply.go 将 nginx 配置和证书落地到目标 host。
//
// 职责：
//   - 通过注入的远端传输能力投递配置和证书
//   - 执行 nginx -t 并 reload
//   - 探测和删除 superdev 管理的孤儿配置
//
// 边界：
//   - 不渲染配置内容
//   - 不直接读取 remote.Store 或建立 SSH
package nginx

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/superdev/agent/ingress"
	"github.com/superdev/agent/model"
)

var _ ingress.ProxyProvider = (*Provider)(nil)

// Apply 将 nginx 配置和证书材料落地到单台 host。
//
// 参数：
//   - ctx: 上下文，用于取消远端命令和传输
//   - host: 目标主机
//   - cfg: 已渲染的 nginx 配置和可选证书
//
// 返回：
//   - host 上实际写入的配置路径和证书路径
//   - 远端命令、传输或 nginx 校验失败时返回错误
//
// 注意：
//   - 只有 nginx -t 成功后才会 reload，避免把坏配置切到线上
func (p *Provider) Apply(ctx context.Context, host model.Host, cfg ingress.RenderedConfig) (ingress.HostState, error) {
	if p.transport == nil {
		return ingress.HostState{}, errTransportRequired()
	}

	target := Target{HostID: host.ID, HostName: host.Name}
	certPath := path.Join(certDir, cfg.Domain)
	if err := p.transport.RunRemote(ctx, target, "mkdir -p "+shellArg(configDir)+" "+shellArg(certPath), "", nil); err != nil {
		return ingress.HostState{}, err
	}

	configTarget := path.Join(configDir, "superdev-"+cfg.Filename)
	configTemp, cleanupConfig, err := writeTempFile("superdev-nginx-*.conf", cfg.Content)
	if err != nil {
		return ingress.HostState{}, err
	}
	defer cleanupConfig()
	if err := p.transport.Transfer(ctx, target, configTemp, configTarget, nil); err != nil {
		return ingress.HostState{}, err
	}

	state := ingress.HostState{HostID: host.ID, ConfigPath: configTarget}
	if cfg.Certificate != nil {
		fullchainTarget, privkeyTarget, err := p.transferCertificate(ctx, target, certPath, cfg.Certificate)
		if err != nil {
			return ingress.HostState{}, err
		}
		state.CertPaths = []string{fullchainTarget, privkeyTarget}
	}

	if err := p.transport.RunRemote(ctx, target, "nginx -t", "", nil); err != nil {
		return ingress.HostState{}, err
	}
	if err := p.transport.RunRemote(ctx, target, "systemctl reload nginx", "", nil); err != nil {
		return ingress.HostState{}, err
	}
	return state, nil
}

// DeployCertificate 只将证书材料落地到 host，并 reload nginx 使已引用路径读取新材料。
//
// 参数：
//   - ctx: 上下文，用于取消远端命令和传输
//   - host: 目标主机
//   - domain: 证书在 host 上使用的目录名
//   - cert: 待部署的证书材料
//
// 返回：
//   - host 上实际写入的证书路径
//   - 远端命令、传输或 nginx 校验失败时返回错误
func (p *Provider) DeployCertificate(ctx context.Context, host model.Host, domain string, cert ingress.Certificate) (ingress.CertDeployment, error) {
	if p.transport == nil {
		return ingress.CertDeployment{}, errTransportRequired()
	}

	target := Target{HostID: host.ID, HostName: host.Name}
	certPath := path.Join(certDir, domain)
	if err := p.transport.RunRemote(ctx, target, "mkdir -p "+shellArg(certPath), "", nil); err != nil {
		return ingress.CertDeployment{}, err
	}
	fullchainTarget, privkeyTarget, err := p.transferCertificate(ctx, target, certPath, &cert)
	if err != nil {
		return ingress.CertDeployment{}, err
	}
	if err := p.transport.RunRemote(ctx, target, "nginx -t", "", nil); err != nil {
		return ingress.CertDeployment{}, err
	}
	if err := p.transport.RunRemote(ctx, target, "systemctl reload nginx", "", nil); err != nil {
		return ingress.CertDeployment{}, err
	}
	return ingress.CertDeployment{HostID: host.ID, CertPath: fullchainTarget, KeyPath: privkeyTarget, DeployedAt: time.Now().UTC()}, nil
}

// Detect 探测 host 上由 SuperDev 管理但已不在声明里的 nginx 配置。
//
// 参数：
//   - ctx: 上下文，用于取消远端 find 命令
//   - host: 待检测主机
//   - declared: 当前仍有效的入口声明列表
//
// 返回：
//   - 孤儿 nginx 配置列表
//   - 远端检测失败时返回错误
//
// 注意：
//   - 只扫描 superdev-*.conf，不碰用户手写 nginx 配置
func (p *Provider) Detect(ctx context.Context, host model.Host, declared []ingress.Ingress) ([]ingress.OrphanConfig, error) {
	if p.transport == nil {
		return nil, errTransportRequired()
	}

	declaredDomains := map[string]bool{}
	for _, in := range declared {
		declaredDomains[in.Domain] = true
	}

	var paths []string
	target := Target{HostID: host.ID, HostName: host.Name}
	err := p.transport.RunRemote(ctx, target, "find /etc/nginx/conf.d -maxdepth 1 -name 'superdev-*.conf' -print", "", func(line string, stream string) {
		if stream == "stdout" && strings.TrimSpace(line) != "" {
			paths = append(paths, strings.TrimSpace(line))
		}
	})
	if err != nil {
		return nil, err
	}

	orphan := []ingress.OrphanConfig{}
	for _, cfgPath := range paths {
		domain := domainFromConfigPath(cfgPath)
		if !declaredDomains[domain] {
			orphan = append(orphan, ingress.OrphanConfig{HostID: host.ID, Path: cfgPath, Domain: domain})
		}
	}
	return orphan, nil
}

// Remove 删除人工确认后的孤儿 nginx 配置并 reload。
//
// 参数：
//   - ctx: 上下文，用于取消远端命令
//   - host: 目标主机
//   - orphan: 用户确认删除的孤儿配置
//
// 返回：
//   - 删除、nginx 校验或 reload 失败时返回错误
//
// 注意：
//   - 删除前校验路径必须位于 SuperDev 管理的 nginx 配置范围内
func (p *Provider) Remove(ctx context.Context, host model.Host, orphan ingress.OrphanConfig) error {
	if p.transport == nil {
		return errTransportRequired()
	}

	configPath, err := managedConfigPath(orphan.Path)
	if err != nil {
		return err
	}

	target := Target{HostID: host.ID, HostName: host.Name}
	if err := p.transport.RunRemote(ctx, target, "rm -f "+shellArg(configPath), "", nil); err != nil {
		return err
	}
	if err := p.transport.RunRemote(ctx, target, "nginx -t", "", nil); err != nil {
		return err
	}
	return p.transport.RunRemote(ctx, target, "systemctl reload nginx", "", nil)
}

func (p *Provider) transferCertificate(ctx context.Context, target Target, certPath string, cert *ingress.Certificate) (string, string, error) {
	fullchain, cleanupCert, err := writeTempFile("superdev-fullchain-*.pem", cert.CertPEM)
	if err != nil {
		return "", "", err
	}
	defer cleanupCert()

	privkey, cleanupKey, err := writeTempFile("superdev-privkey-*.pem", cert.KeyPEM)
	if err != nil {
		return "", "", err
	}
	defer cleanupKey()

	fullchainTarget := path.Join(certPath, "fullchain.pem")
	privkeyTarget := path.Join(certPath, "privkey.pem")
	if err := p.transport.Transfer(ctx, target, fullchain, fullchainTarget, nil); err != nil {
		return "", "", err
	}
	if err := p.transport.Transfer(ctx, target, privkey, privkeyTarget, nil); err != nil {
		return "", "", err
	}
	return fullchainTarget, privkeyTarget, nil
}

func writeTempFile(pattern string, content string) (string, func(), error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

func domainFromConfigPath(cfgPath string) string {
	base := path.Base(cfgPath)
	return strings.TrimSuffix(strings.TrimPrefix(base, "superdev-"), ".conf")
}

func managedConfigPath(cfgPath string) (string, error) {
	cleaned := path.Clean(cfgPath)
	base := path.Base(cleaned)
	if !strings.HasPrefix(cleaned, configDir+"/") ||
		!strings.HasPrefix(base, "superdev-") ||
		!strings.HasSuffix(base, ".conf") {
		return "", &os.PathError{Op: "ingress nginx remove", Path: cfgPath, Err: os.ErrInvalid}
	}
	return cleaned, nil
}

func shellArg(value string) string {
	if value != "" && isShellSafe(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func isShellSafe(value string) bool {
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '/', '.', '_', '-', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func errTransportRequired() error {
	return &os.PathError{Op: "ingress nginx", Path: filepath.ToSlash(configDir), Err: os.ErrInvalid}
}
