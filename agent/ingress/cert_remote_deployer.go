// cert_remote_deployer.go 实现通用证书材料远端部署。
//
// 职责：
//   - 将证书 fullchain 和 private key 写入调用方指定的 host 路径
//   - 在证书材料传输成功后执行可选的 post-deploy 命令
//
// 边界：
//   - 不推断具体 proxy 类型，不内置 nginx/caddy/traefik 命令
//   - 不持久化部署记录，持久化由 CertService 完成
package ingress

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// CertificateRemoteTransport 提供证书部署所需的远端命令和文件传输能力。
type CertificateRemoteTransport interface {
	// RunRemote 在 host 上执行 shell 命令。
	RunRemote(ctx context.Context, host model.Host, cmd string, workDir string, onLine func(string, string)) error
	// Transfer 将本地 source 文件传输到 host 的 targetPath。
	Transfer(ctx context.Context, host model.Host, source string, targetPath string, onLine func(string, string)) error
}

// RemoteCertificateDeployer 通过远端传输能力部署证书材料。
type RemoteCertificateDeployer struct {
	transport CertificateRemoteTransport
}

// NewRemoteCertificateDeployer 创建通用远端证书部署器。
//
// 参数：
//   - transport: 远端命令和文件传输能力
//
// 返回：
//   - 可被 CertService 注入使用的 CertificateDeployer
func NewRemoteCertificateDeployer(transport CertificateRemoteTransport) *RemoteCertificateDeployer {
	return &RemoteCertificateDeployer{transport: transport}
}

// DeployCertificate 将证书材料部署到 host 的指定路径，并执行可选后置命令。
//
// 参数：
//   - ctx: 上下文，用于取消远端命令和传输
//   - host: 目标主机
//   - cert: 待部署的证书材料
//   - request: 目标路径、后置命令和来源信息
//
// 返回：
//   - 实际部署记录
//   - 传输能力缺失、证书材料为空、远端命令或传输失败时返回错误
func (d *RemoteCertificateDeployer) DeployCertificate(ctx context.Context, host model.Host, cert Certificate, request CertificateDeploymentRequest) (CertDeployment, error) {
	if d.transport == nil {
		return CertDeployment{}, errors.New("certificate remote transport is required")
	}
	request = normalizeDeploymentRequest(request, cert.Domain)
	if strings.TrimSpace(cert.CertPEM) == "" || strings.TrimSpace(cert.KeyPEM) == "" {
		return CertDeployment{}, errors.New("certificate material is required")
	}

	if err := d.transport.RunRemote(ctx, host, "mkdir -p "+shellArg(path.Dir(request.CertPath))+" "+shellArg(path.Dir(request.KeyPath)), "", nil); err != nil {
		return CertDeployment{}, err
	}
	certSource, cleanupCert, err := writeCertificateTempFile("superdev-fullchain-*.pem", cert.CertPEM)
	if err != nil {
		return CertDeployment{}, err
	}
	defer cleanupCert()
	keySource, cleanupKey, err := writeCertificateTempFile("superdev-privkey-*.pem", cert.KeyPEM)
	if err != nil {
		return CertDeployment{}, err
	}
	defer cleanupKey()

	if err := d.transport.Transfer(ctx, host, certSource, request.CertPath, nil); err != nil {
		return CertDeployment{}, err
	}
	if err := d.transport.Transfer(ctx, host, keySource, request.KeyPath, nil); err != nil {
		return CertDeployment{}, err
	}
	if request.PostDeployCommand != "" {
		if err := d.transport.RunRemote(ctx, host, request.PostDeployCommand, "", nil); err != nil {
			return CertDeployment{}, err
		}
	}
	return CertDeployment{
		HostID:            host.ID,
		CertPath:          request.CertPath,
		KeyPath:           request.KeyPath,
		PostDeployCommand: request.PostDeployCommand,
		SourceType:        request.SourceType,
		SourceID:          request.SourceID,
		Status:            CertDeploymentSucceeded,
		DeployedAt:        time.Now().UTC(),
	}, nil
}

func writeCertificateTempFile(pattern string, content string) (string, func(), error) {
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
		if strings.ContainsRune("/._-:", r) {
			continue
		}
		return false
	}
	return true
}
