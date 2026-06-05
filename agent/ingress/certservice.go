// certservice.go 编排全局托管证书的申请、续期、部署和匹配。
//
// 职责：
//   - 读取全局 ACME 账号和 DNS provider，申请或续期证书
//   - 将证书材料部署到目标 host，并记录部署路径
//   - 为入口表单提供 active 证书匹配能力
//
// 边界：
//   - 不暴露私钥脱敏策略，脱敏由 Store/API 层处理
//   - 不管理 Ingress 声明 CRUD
package ingress

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/superdev/agent/model"
)

// CertServiceConfig 描述全局证书服务的依赖。
type CertServiceConfig struct {
	Store      Store
	Registry   *Registry
	HostLookup HostLookup
	Deployer   CertificateDeployer
}

// CertService 编排托管证书申请、续期、部署和匹配。
type CertService struct {
	store      Store
	registry   *Registry
	hostLookup HostLookup
	deployer   CertificateDeployer
}

// NewCertService 创建全局证书服务。
//
// 参数：
//   - cfg: Store、Registry 和 HostLookup 依赖
//
// 返回：
//   - 可执行 issue/renew/deploy/match 的 CertService
func NewCertService(cfg CertServiceConfig) *CertService {
	hostLookup := cfg.HostLookup
	if hostLookup == nil {
		hostLookup = func(ids []string) ([]model.Host, error) {
			return nil, errors.New("host lookup is required")
		}
	}
	return &CertService{store: cfg.Store, registry: cfg.Registry, hostLookup: hostLookup, deployer: cfg.Deployer}
}

// Issue 申请托管证书并持久化状态流转。
//
// 参数：
//   - ctx: 上下文，用于取消 DNS 和 ACME 调用
//   - certID: 待申请的托管证书 ID
//
// 返回：
//   - 更新后的托管证书
//   - 申请失败时返回错误，并将证书状态持久化为 failed
func (s *CertService) Issue(ctx context.Context, certID string) (ManagedCertificate, error) {
	if err := s.ensureStore(); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.ensureRegistry(); err != nil {
		return ManagedCertificate{}, err
	}
	cert, err := s.loadCertificate(certID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	cert.Status = CertPending
	cert.LastError = ""
	cert, err = s.store.UpsertCertificate(cert)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if cert.Issuer != CertificateIssuerACME {
		return cert, errors.New("only acme certificates can be issued")
	}
	domains := normalizedDomains(cert.Domains)
	if len(domains) == 0 {
		return s.markCertificateFailed(cert, errors.New("certificate domains are required"))
	}
	dnsProvider, err := s.registry.DNS(cert.DNSProvider)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	certProvider, err := s.registry.Cert(ProviderACME)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	material, err := certProvider.Obtain(ctx, domains, dnsProvider)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	cert.Material = &material
	cert.Status = CertActive
	cert.LastError = ""
	return s.store.UpsertCertificate(cert)
}

// Renew 续期托管证书，并重新部署到已记录的 host。
//
// 参数：
//   - ctx: 上下文，用于取消 DNS、ACME 和远端部署调用
//   - certID: 待续期的托管证书 ID
//
// 返回：
//   - 更新后的托管证书
//   - 续期或重新部署失败时返回错误
func (s *CertService) Renew(ctx context.Context, certID string) (ManagedCertificate, error) {
	if err := s.ensureStore(); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.ensureRegistry(); err != nil {
		return ManagedCertificate{}, err
	}
	cert, err := s.loadCertificate(certID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if cert.Issuer != CertificateIssuerACME {
		return ManagedCertificate{}, errors.New("only acme certificates can be renewed")
	}
	if cert.Material == nil {
		return ManagedCertificate{}, fmt.Errorf("certificate %s has no material", certID)
	}
	domains := normalizedDomains(cert.Domains)
	if len(domains) == 0 {
		return s.markCertificateFailed(cert, errors.New("certificate domains are required"))
	}
	dnsProvider, err := s.registry.DNS(cert.DNSProvider)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	certProvider, err := s.registry.Cert(ProviderACME)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	material, err := certProvider.Renew(ctx, *cert.Material, domains, dnsProvider)
	if err != nil {
		return s.markCertificateFailed(cert, err)
	}
	cert.Material = &material
	cert.Status = CertActive
	cert.LastError = ""
	cert, err = s.store.UpsertCertificate(cert)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if len(cert.Deployments) == 0 {
		return cert, nil
	}
	requests := make([]CertificateDeploymentRequest, 0, len(cert.Deployments))
	for _, deployment := range cert.Deployments {
		requests = append(requests, deploymentRequestFromRecord(deployment))
	}
	return s.Deploy(ctx, cert.ID, requests)
}

// Deploy 将证书材料部署到指定 host。
//
// 参数：
//   - ctx: 上下文，用于取消远端部署调用
//   - certID: 待部署的托管证书 ID
//   - requests: 目标 host、证书路径、私钥路径和可选部署后命令
//
// 返回：
//   - 更新部署记录后的托管证书
//   - 证书未就绪、host 解析或部署失败时返回错误
func (s *CertService) Deploy(ctx context.Context, certID string, requests []CertificateDeploymentRequest) (ManagedCertificate, error) {
	if err := s.ensureStore(); err != nil {
		return ManagedCertificate{}, err
	}
	if s.deployer == nil {
		return ManagedCertificate{}, errors.New("certificate deployer is required")
	}
	cert, err := s.loadCertificate(certID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if cert.Status != CertActive || cert.Material == nil {
		return ManagedCertificate{}, fmt.Errorf("certificate %s is not active", certID)
	}
	requests = normalizedDeploymentRequests(requests, primaryDomain(cert))
	if len(requests) == 0 {
		return ManagedCertificate{}, errors.New("certificate deployment targets are required")
	}
	hostIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		hostIDs = append(hostIDs, request.HostID)
	}
	hosts, err := s.hostLookup(hostIDs)
	if err != nil {
		return ManagedCertificate{}, err
	}
	hostsByID := map[string]model.Host{}
	for _, host := range hosts {
		hostsByID[host.ID] = host
	}
	deploymentsByHost := map[string]CertDeployment{}
	for _, existing := range cert.Deployments {
		deploymentsByHost[existing.HostID] = existing
	}
	for _, request := range requests {
		host, ok := hostsByID[request.HostID]
		if !ok {
			return ManagedCertificate{}, fmt.Errorf("host %s not found", request.HostID)
		}
		deployment, err := s.deployer.DeployCertificate(ctx, host, *cert.Material, request)
		if err != nil {
			deploymentsByHost[request.HostID] = failedDeployment(request, err)
			cert.Deployments = sortedDeployments(deploymentsByHost)
			saved, saveErr := s.store.UpsertCertificate(cert)
			if saveErr != nil {
				return saved, saveErr
			}
			return saved, err
		}
		deploymentsByHost[host.ID] = normalizeSuccessfulDeployment(host.ID, request, deployment)
	}
	cert.Deployments = sortedDeployments(deploymentsByHost)
	return s.store.UpsertCertificate(cert)
}

// Match 按域名匹配 active 托管证书。
//
// 参数：
//   - domain: 入口域名
//
// 返回：
//   - 命中的证书；无匹配时为 nil
//   - 是否命中
//   - 读取存储失败时返回错误
func (s *CertService) Match(domain string) (*ManagedCertificate, bool, error) {
	if err := s.ensureStore(); err != nil {
		return nil, false, err
	}
	certs, err := s.store.ListCertificates()
	if err != nil {
		return nil, false, err
	}
	active := make([]ManagedCertificate, 0, len(certs))
	for _, cert := range certs {
		if cert.Status == CertActive {
			active = append(active, cert)
		}
	}
	matched, ok := MatchCertificate(domain, active)
	return matched, ok, nil
}

func (s *CertService) loadCertificate(certID string) (ManagedCertificate, error) {
	cert, ok, err := s.store.GetCertificate(certID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !ok {
		return ManagedCertificate{}, fmt.Errorf("certificate %s not found", certID)
	}
	return cert, nil
}

func (s *CertService) markCertificateFailed(cert ManagedCertificate, cause error) (ManagedCertificate, error) {
	cert.Status = CertFailed
	cert.LastError = cause.Error()
	saved, err := s.store.UpsertCertificate(cert)
	if err != nil {
		return saved, err
	}
	return saved, cause
}

func (s *CertService) ensureStore() error {
	if s.store == nil {
		return errors.New("certificate store is required")
	}
	return nil
}

func (s *CertService) ensureRegistry() error {
	if s.registry == nil {
		return errors.New("certificate registry is required")
	}
	return nil
}

func normalizedDomains(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		trimmed := strings.TrimSpace(domain)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func primaryDomain(cert ManagedCertificate) string {
	domains := normalizedDomains(cert.Domains)
	if len(domains) > 0 {
		return domains[0]
	}
	if cert.Material != nil {
		return strings.TrimSpace(cert.Material.Domain)
	}
	return ""
}

func normalizedDeploymentRequests(requests []CertificateDeploymentRequest, domain string) []CertificateDeploymentRequest {
	out := make([]CertificateDeploymentRequest, 0, len(requests))
	seen := map[string]bool{}
	for _, request := range requests {
		normalized := normalizeDeploymentRequest(request, domain)
		if normalized.HostID == "" || seen[normalized.HostID] {
			continue
		}
		seen[normalized.HostID] = true
		out = append(out, normalized)
	}
	return out
}

func normalizeSuccessfulDeployment(hostID string, request CertificateDeploymentRequest, deployment CertDeployment) CertDeployment {
	deployment.HostID = hostID
	if deployment.CertPath == "" {
		deployment.CertPath = request.CertPath
	}
	if deployment.KeyPath == "" {
		deployment.KeyPath = request.KeyPath
	}
	deployment.PostDeployCommand = request.PostDeployCommand
	deployment.SourceType = request.SourceType
	deployment.SourceID = request.SourceID
	deployment.Status = CertDeploymentSucceeded
	deployment.LastError = ""
	if deployment.DeployedAt.IsZero() {
		deployment.DeployedAt = time.Now().UTC()
	}
	return deployment
}

func failedDeployment(request CertificateDeploymentRequest, cause error) CertDeployment {
	return CertDeployment{
		HostID:            request.HostID,
		CertPath:          request.CertPath,
		KeyPath:           request.KeyPath,
		PostDeployCommand: request.PostDeployCommand,
		SourceType:        request.SourceType,
		SourceID:          request.SourceID,
		Status:            CertDeploymentFailed,
		LastError:         cause.Error(),
		DeployedAt:        time.Now().UTC(),
	}
}

func sortedDeployments(items map[string]CertDeployment) []CertDeployment {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]CertDeployment, 0, len(keys))
	for _, key := range keys {
		out = append(out, items[key])
	}
	return out
}
