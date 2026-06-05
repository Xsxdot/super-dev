// cert_deployment_test.go 验证通用证书部署执行器。
//
// 职责：
//   - 验证证书材料按用户指定路径传输
//   - 验证部署后命令为空时不会执行额外命令
//
// 边界：
//   - 不连接真实 SSH
//   - 不验证具体 proxy provider 的 reload 语义
package ingress

import (
	"context"
	"testing"

	"github.com/superdev/agent/model"
)

func TestRemoteCertificateDeployerTransfersToRequestedPathsAndRunsPostCommand(t *testing.T) {
	transport := &recordingCertificateTransport{}
	deployer := NewRemoteCertificateDeployer(transport)

	deployment, err := deployer.DeployCertificate(context.Background(), model.Host{ID: "edge-a", Name: "edge"}, Certificate{
		Domain:  "api.example.com",
		CertPEM: "CERT",
		KeyPEM:  "KEY",
	}, CertificateDeploymentRequest{
		HostID:            "edge-a",
		CertPath:          "/opt/certs/api/fullchain.pem",
		KeyPath:           "/opt/certs/api/privkey.pem",
		PostDeployCommand: "service caddy reload",
	})
	requireNoError(t, err)

	assertEqual(t, deployment.CertPath, "/opt/certs/api/fullchain.pem")
	assertEqual(t, deployment.KeyPath, "/opt/certs/api/privkey.pem")
	assertStringSliceEqual(t, transport.commands, []string{
		"mkdir -p /opt/certs/api /opt/certs/api",
		"service caddy reload",
	})
	assertStringSliceEqual(t, transport.targets, []string{
		"/opt/certs/api/fullchain.pem",
		"/opt/certs/api/privkey.pem",
	})
}

func TestRemoteCertificateDeployerSkipsEmptyPostCommand(t *testing.T) {
	transport := &recordingCertificateTransport{}
	deployer := NewRemoteCertificateDeployer(transport)

	_, err := deployer.DeployCertificate(context.Background(), model.Host{ID: "edge-a"}, Certificate{
		Domain:  "api.example.com",
		CertPEM: "CERT",
		KeyPEM:  "KEY",
	}, CertificateDeploymentRequest{
		HostID:   "edge-a",
		CertPath: "/opt/certs/api/fullchain.pem",
		KeyPath:  "/opt/certs/api/privkey.pem",
	})
	requireNoError(t, err)

	assertStringSliceEqual(t, transport.commands, []string{"mkdir -p /opt/certs/api /opt/certs/api"})
}

func TestRemoteCertificateDeployerQuotesWildcardDefaultPaths(t *testing.T) {
	transport := &recordingCertificateTransport{}
	deployer := NewRemoteCertificateDeployer(transport)

	_, err := deployer.DeployCertificate(context.Background(), model.Host{ID: "edge-a"}, Certificate{
		Domain:  "*.example.com",
		CertPEM: "CERT",
		KeyPEM:  "KEY",
	}, CertificateDeploymentRequest{HostID: "edge-a"})
	requireNoError(t, err)

	assertStringSliceEqual(t, transport.commands, []string{
		"mkdir -p '/etc/superdev/ingress/certs/*.example.com' '/etc/superdev/ingress/certs/*.example.com'",
	})
}

type recordingCertificateTransport struct {
	commands []string
	targets  []string
}

func (t *recordingCertificateTransport) RunRemote(ctx context.Context, host model.Host, cmd string, workDir string, onLine func(string, string)) error {
	t.commands = append(t.commands, cmd)
	return nil
}

func (t *recordingCertificateTransport) Transfer(ctx context.Context, host model.Host, source string, targetPath string, onLine func(string, string)) error {
	t.targets = append(t.targets, targetPath)
	return nil
}
