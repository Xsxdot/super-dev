// cert_deployment.go 定义通用证书部署契约。
//
// 职责：
//   - 描述证书材料部署到 host 时的目标路径和可选后置命令
//   - 定义 CertService 依赖的部署执行器接口
//
// 边界：
//   - 不实现 SSH、SCP 或具体 proxy reload 逻辑
//   - 不读取证书存储，部署编排由 CertService 完成
package ingress

import (
	"context"
	"path"
	"strings"

	"github.com/superdev/agent/model"
)

const defaultCertDeploymentRoot = "/etc/superdev/ingress/certs"

const (
	// CertDeploymentSucceeded 表示最近一次证书部署成功。
	CertDeploymentSucceeded = "succeeded"
	// CertDeploymentFailed 表示最近一次证书部署失败。
	CertDeploymentFailed = "failed"
)

// CertificateDeploymentRequest 描述一次证书部署的目标路径和可选后置命令。
type CertificateDeploymentRequest struct {
	HostID            string `json:"host_id"`
	CertPath          string `json:"cert_path,omitempty"`
	KeyPath           string `json:"key_path,omitempty"`
	PostDeployCommand string `json:"post_deploy_command,omitempty"`
	SourceType        string `json:"source_type,omitempty"`
	SourceID          string `json:"source_id,omitempty"`
}

// CertificateDeployer 将证书材料投递到单台 host。
type CertificateDeployer interface {
	// DeployCertificate 将 cert 写入 request 指定路径，并执行可选后置命令。
	DeployCertificate(ctx context.Context, host model.Host, cert Certificate, request CertificateDeploymentRequest) (CertDeployment, error)
}

func normalizeDeploymentRequest(request CertificateDeploymentRequest, domain string) CertificateDeploymentRequest {
	request.HostID = strings.TrimSpace(request.HostID)
	request.CertPath = strings.TrimSpace(request.CertPath)
	request.KeyPath = strings.TrimSpace(request.KeyPath)
	request.PostDeployCommand = strings.TrimSpace(request.PostDeployCommand)
	request.SourceType = strings.TrimSpace(request.SourceType)
	request.SourceID = strings.TrimSpace(request.SourceID)
	if request.CertPath == "" || request.KeyPath == "" {
		defaultCertPath, defaultKeyPath := DefaultCertificateDeploymentPaths(domain)
		if request.CertPath == "" {
			request.CertPath = defaultCertPath
		}
		if request.KeyPath == "" {
			request.KeyPath = defaultKeyPath
		}
	}
	return request
}

func deploymentRequestFromRecord(deployment CertDeployment) CertificateDeploymentRequest {
	return CertificateDeploymentRequest{
		HostID:            deployment.HostID,
		CertPath:          deployment.CertPath,
		KeyPath:           deployment.KeyPath,
		PostDeployCommand: deployment.PostDeployCommand,
		SourceType:        deployment.SourceType,
		SourceID:          deployment.SourceID,
	}
}

// DefaultCertificateDeploymentPaths 返回指定域名的默认证书和私钥部署路径。
func DefaultCertificateDeploymentPaths(domain string) (string, string) {
	certDir := path.Join(defaultCertDeploymentRoot, strings.TrimSpace(domain))
	return path.Join(certDir, "fullchain.pem"), path.Join(certDir, "privkey.pem")
}
