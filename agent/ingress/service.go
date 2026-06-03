// service.go 编排 Ingress 声明的预览、收敛、检测和删除确认。
//
// 职责：
//   - 串联 Store、Registry、HostLookup 完成入口声明收敛
//   - 保证 DNS → 证书 → proxy render → 多 host apply 的执行顺序
//   - 聚合孤儿 proxy 配置和 DNS 记录，删除时只处理用户显式传入项
//
// 边界：
//   - 不实现具体 DNS、证书或 proxy provider 协议
//   - 不自动回滚已经完成的外部收敛步骤
package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/superdev/agent/model"
)

// HostLookup 按 host ID 批量解析 host 模型。
type HostLookup func(ids []string) ([]model.Host, error)

// ServiceConfig 描述 Ingress Service 的依赖。
type ServiceConfig struct {
	Store      Store
	Registry   *Registry
	HostLookup HostLookup
}

// Service 是入口配置子系统的应用服务。
type Service struct {
	store      Store
	registry   *Registry
	hostLookup HostLookup
}

// ApplyOptions 描述入口收敛时需要用户确认的输入。
type ApplyOptions struct {
	ConfirmedDNSValue string `json:"confirmed_dns_value,omitempty"`
}

// PreviewResult 描述入口声明收敛前的预演结果。
type PreviewResult struct {
	Ingress              Ingress           `json:"ingress"`
	DNSRecord            Record            `json:"dns_record"`
	DNSRecords           []Record          `json:"dns_records"`
	DNSValueDecision     DNSValueDecision  `json:"dns_value_decision"`
	RenderedConfigByHost map[string]string `json:"rendered_config_by_host"`
	ManualInstructions   []string          `json:"manual_instructions,omitempty"`
}

// NewService 创建入口配置应用服务。
//
// 参数：
//   - cfg: Store、Registry 和 HostLookup 依赖
//
// 返回：
//   - 可执行 preview/apply/detect/remove 的 Service
func NewService(cfg ServiceConfig) *Service {
	hostLookup := cfg.HostLookup
	if hostLookup == nil {
		hostLookup = func(ids []string) ([]model.Host, error) {
			return nil, errors.New("host lookup is required")
		}
	}
	return &Service{
		store:      cfg.Store,
		registry:   cfg.Registry,
		hostLookup: hostLookup,
	}
}

// Preview 预演入口声明会产生的 DNS 记录和 proxy 配置。
//
// 参数：
//   - ctx: 上下文，用于取消 provider 调用
//   - in: 待预演入口声明
//
// 返回：
//   - 预演结果
//   - 声明无效、host 解析失败或 provider 渲染失败时返回错误
//
// 注意：
//   - 非 manual DNS provider 不会在 preview 阶段调用 EnsureRecord，避免外部副作用
func (s *Service) Preview(ctx context.Context, in Ingress) (PreviewResult, error) {
	if err := s.ensureReady(); err != nil {
		return PreviewResult{}, err
	}
	if err := in.Validate(); err != nil {
		return PreviewResult{}, err
	}

	hosts, err := s.hostLookup(in.Proxy.HostIDs)
	if err != nil {
		return PreviewResult{}, err
	}
	records, decision, err := resolveDNSRecords(in, hosts)
	result := PreviewResult{
		Ingress:              in,
		DNSValueDecision:     decision,
		RenderedConfigByHost: map[string]string{},
	}
	if err != nil {
		return result, fmt.Errorf("dns record value is not ready: %s", decision.Message)
	}

	if len(records) > 0 {
		result.DNSRecord = records[0]
	}
	in.DNS.Records = records
	result.Ingress = in
	result.DNSRecords = records

	if in.DNS.Provider == ProviderManual {
		dnsProvider, err := s.registry.DNS(in.DNS.Provider)
		if err != nil {
			return PreviewResult{}, err
		}
		for _, record := range records {
			recordResult, err := dnsProvider.EnsureRecord(ctx, record)
			if err != nil {
				return PreviewResult{}, err
			}
			result.ManualInstructions = append(result.ManualInstructions, recordResult.Instructions...)
		}
	}

	proxyProvider, err := s.registry.Proxy(in.Proxy.Provider)
	if err != nil {
		return PreviewResult{}, err
	}
	rendered, err := proxyProvider.Render(in, nil)
	if err != nil {
		return PreviewResult{}, err
	}
	for _, host := range hosts {
		result.RenderedConfigByHost[host.ID] = rendered.Content
	}
	return result, nil
}

// Apply 将已保存入口声明收敛到 DNS、证书和目标 host。
//
// 参数：
//   - ctx: 上下文，用于取消 provider 调用
//   - ingressID: 待收敛入口声明 ID
//   - opts: 用户确认的 DNS 推断值等选项
//
// 返回：
//   - 本次落地状态
//   - 任一步失败时返回第一处错误
//
// 注意：
//   - 不做自动回滚，后续 apply 会通过 provider 幂等能力继续收敛
func (s *Service) Apply(ctx context.Context, ingressID string, opts ApplyOptions) (AppliedState, error) {
	if err := s.ensureReady(); err != nil {
		return AppliedState{}, err
	}

	in, err := s.loadIngress(ingressID)
	if err != nil {
		return AppliedState{}, err
	}
	if err := in.Validate(); err != nil {
		return AppliedState{}, err
	}

	hosts, err := s.hostLookup(in.Proxy.HostIDs)
	if err != nil {
		return AppliedState{}, err
	}
	records, decision, err := resolveDNSRecords(in, hosts)
	if err != nil {
		return AppliedState{}, fmt.Errorf("dns record value is not ready: %s", decision.Message)
	}
	if decision.RequiresConfirmation && strings.TrimSpace(opts.ConfirmedDNSValue) != decision.Value {
		return AppliedState{}, fmt.Errorf("confirmed_dns_value must match inferred DNS value %s", decision.Value)
	}
	in.DNS.Records = records

	var cert *Certificate
	var certRecord *ManagedCertificate
	if in.TLS.Enabled {
		if strings.TrimSpace(in.TLS.CertID) == "" {
			return AppliedState{}, errors.New("tls.cert_id is required")
		}
		managed, ok, err := s.store.GetCertificate(in.TLS.CertID)
		if err != nil {
			return AppliedState{}, err
		}
		if !ok {
			return AppliedState{}, fmt.Errorf("certificate %s not found", in.TLS.CertID)
		}
		if managed.Status != CertActive || managed.Material == nil {
			return AppliedState{}, fmt.Errorf("证书 %s 尚未就绪，请先在 SSL 管理页申请", in.TLS.CertID)
		}
		copied := *managed.Material
		cert = &copied
		certRecord = &managed
	}

	dnsProvider, err := s.registry.DNS(in.DNS.Provider)
	if err != nil {
		return AppliedState{}, err
	}
	recordResults := make([]Record, 0, len(records))
	for _, record := range records {
		recordResult, err := dnsProvider.EnsureRecord(ctx, record)
		if err != nil {
			return AppliedState{}, err
		}
		recordResults = append(recordResults, recordResult.Record)
	}

	proxyProvider, err := s.registry.Proxy(in.Proxy.Provider)
	if err != nil {
		return AppliedState{}, err
	}
	rendered, err := proxyProvider.Render(in, cert)
	if err != nil {
		return AppliedState{}, err
	}

	hostStates := make([]HostState, 0, len(hosts))
	for _, host := range hosts {
		state, err := proxyProvider.Apply(ctx, host, rendered)
		if err != nil {
			return AppliedState{}, err
		}
		hostStates = append(hostStates, state)
	}

	if certRecord != nil {
		deployments := map[string]CertDeployment{}
		for _, existing := range certRecord.Deployments {
			deployments[existing.HostID] = existing
		}
		for _, hostState := range hostStates {
			if len(hostState.CertPaths) < 2 {
				continue
			}
			deployments[hostState.HostID] = CertDeployment{
				HostID:     hostState.HostID,
				CertPath:   hostState.CertPaths[0],
				KeyPath:    hostState.CertPaths[1],
				DeployedAt: time.Now().UTC(),
			}
		}
		certRecord.Deployments = sortedDeployments(deployments)
		if _, err := s.store.UpsertCertificate(*certRecord); err != nil {
			return AppliedState{}, err
		}
	}

	state := AppliedState{
		IngressID: in.ID,
		Records:   recordResults,
		Hosts:     hostStates,
	}
	if err := s.store.SaveState(state); err != nil {
		return AppliedState{}, err
	}
	return state, nil
}

// DetectOrphans 聚合指定入口相关 host 和 DNS provider 上的孤儿资源。
//
// 参数：
//   - ctx: 上下文，用于取消 provider 调用
//   - ingressID: 作为检测入口的声明 ID
//
// 返回：
//   - 孤儿 proxy 配置和 DNS 记录报告
//   - 声明、host 或 provider 解析失败时返回错误
func (s *Service) DetectOrphans(ctx context.Context, ingressID string) (OrphanReport, error) {
	if err := s.ensureReady(); err != nil {
		return OrphanReport{}, err
	}
	in, err := s.loadIngress(ingressID)
	if err != nil {
		return OrphanReport{}, err
	}
	declared, err := s.store.ListIngress()
	if err != nil {
		return OrphanReport{}, err
	}
	hosts, err := s.operationHosts(in)
	if err != nil {
		return OrphanReport{}, err
	}

	proxyProvider, err := s.registry.Proxy(in.Proxy.Provider)
	if err != nil {
		return OrphanReport{}, err
	}
	report := OrphanReport{Configs: []OrphanConfig{}, Records: []Record{}}
	for _, host := range hosts {
		configs, err := proxyProvider.Detect(ctx, host, declared)
		if err != nil {
			return OrphanReport{}, err
		}
		report.Configs = append(report.Configs, configs...)
	}

	dnsProvider, err := s.registry.DNS(in.DNS.Provider)
	if err != nil {
		return OrphanReport{}, err
	}
	records, err := dnsProvider.ListRecords(ctx, in.Domain)
	if err != nil {
		return OrphanReport{}, err
	}
	declaredRecords := declaredRecordKeys(declared)
	for _, record := range records {
		if !declaredRecords[recordKey(record)] {
			report.Records = append(report.Records, record)
		}
	}
	return report, nil
}

// RemoveOrphans 删除用户显式确认的孤儿 proxy 配置和 DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 provider 调用
//   - ingressID: 用于解析 provider 和 host 范围的入口声明 ID
//   - report: 用户确认删除的孤儿资源列表
//
// 返回：
//   - 任一删除动作失败时返回错误
//
// 注意：
//   - 空 report 不会触发任何删除；不会重新探测后自动补充删除项
func (s *Service) RemoveOrphans(ctx context.Context, ingressID string, report OrphanReport) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	in, err := s.loadIngress(ingressID)
	if err != nil {
		return err
	}
	hosts, err := s.operationHosts(in)
	if err != nil {
		return err
	}
	hostsByID := map[string]model.Host{}
	for _, host := range hosts {
		hostsByID[host.ID] = host
	}

	proxyProvider, err := s.registry.Proxy(in.Proxy.Provider)
	if err != nil {
		return err
	}
	for _, orphan := range report.Configs {
		host, ok := hostsByID[orphan.HostID]
		if !ok {
			return fmt.Errorf("host %s not found for orphan config", orphan.HostID)
		}
		if err := proxyProvider.Remove(ctx, host, orphan); err != nil {
			return err
		}
	}

	dnsProvider, err := s.registry.DNS(in.DNS.Provider)
	if err != nil {
		return err
	}
	for _, record := range report.Records {
		if err := dnsProvider.RemoveRecord(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func resolveDNSRecords(in Ingress, hosts []model.Host) ([]Record, DNSValueDecision, error) {
	records := append([]Record(nil), in.DNS.Records...)
	if len(records) == 0 {
		decision := DNSValueDecision{RequiresInput: true, Message: "at least one dns record is required"}
		return nil, decision, errors.New(decision.Message)
	}

	out := make([]Record, 0, len(records))
	decision := DNSValueDecision{OK: true}
	for i, record := range records {
		value := strings.TrimSpace(record.Value)
		if value == "" {
			inferred := ResolveDNSRecordValue(in, hosts)
			decision = inferred
			if !inferred.OK {
				return nil, inferred, errors.New(inferred.Message)
			}
			value = inferred.Value
		}
		record = record.WithDefaults(in.Domain, value)
		if i == 0 && decision.Value == "" {
			decision.Value = record.Value
		}
		out = append(out, record)
	}
	return out, decision, nil
}

func (s *Service) ensureReady() error {
	if s.store == nil {
		return errors.New("ingress store is required")
	}
	if s.registry == nil {
		return errors.New("ingress registry is required")
	}
	return nil
}

func (s *Service) loadIngress(ingressID string) (Ingress, error) {
	in, ok, err := s.store.GetIngress(ingressID)
	if err != nil {
		return Ingress{}, err
	}
	if !ok {
		return Ingress{}, fmt.Errorf("ingress %s not found", ingressID)
	}
	return in, nil
}

func (s *Service) operationHosts(in Ingress) ([]model.Host, error) {
	ids := append([]string(nil), in.Proxy.HostIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	state, ok, err := s.store.GetState(in.ID)
	if err != nil {
		return nil, err
	}
	if ok {
		for _, host := range state.Hosts {
			if host.HostID == "" || seen[host.HostID] {
				continue
			}
			ids = append(ids, host.HostID)
			seen[host.HostID] = true
		}
	}
	return s.hostLookup(ids)
}

func declaredRecordKeys(declared []Ingress) map[string]bool {
	keys := map[string]bool{}
	for _, in := range declared {
		for _, record := range in.DNS.Records {
			record = record.WithDefaults(in.Domain, record.Value)
			keys[recordKey(record)] = true
		}
	}
	return keys
}

func recordKey(record Record) string {
	return string(record.Type) + "\x00" + record.Name
}
