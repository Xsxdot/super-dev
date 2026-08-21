// lease.go —— AI 临时资源租约管理器。
//
// 职责：按项目绑定规划并申请一组资源，执行审批前置、配额保护、持久化、失败回滚与 TTL 生命周期。
// 边界：不认识 PG/Redis 具体实现，不做鉴权，不碰 HTTP/MCP；资源语义全部通过 Provisioner 注入。
package dbprovision

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
)

const (
	absoluteLeaseLifetime = 24 * time.Hour
	defaultQuota          = 3
	defaultTTL            = 30 * time.Minute
	maxSlotRetries        = 3
)

// ErrResourceSlotTaken 表示持久化层发现同一数据源上的资源槽位已被占用。
//
// store 包通过别名暴露同一个哨兵，避免 dbprovision 反向依赖 store。
var ErrResourceSlotTaken = errors.New("provision resource slot taken")

// RegistryReader 是 LeaseManager 需要的数据源读取能力子集。
type RegistryReader interface {
	// GetByName 按资源类型与登记名读取管理连接。
	GetByName(ctx context.Context, kind, name string) (DataSource, error)
}

// LeaseStore 是租约与资源的持久化能力，由 agent/store 实现。
type LeaseStore interface {
	// InsertLease 先持久化一条 active 租约。
	InsertLease(lease Lease) error
	// InsertResource 登记 creating 状态的资源并返回行 ID。
	InsertResource(leaseID, datasourceID string, res Resource) (string, error)
	// MarkResourceActive 将资源登记切换为 active。
	MarkResourceActive(resourceID string) error
	// MarkResourceReclaimed 将资源登记切换为 reclaimed。
	MarkResourceReclaimed(resourceID string) error
	// MarkLeaseReleased 将租约标记为 released。
	MarkLeaseReleased(leaseID string) error
	// UpdateLeaseExpiry 更新到期时间与续租次数。
	UpdateLeaseExpiry(leaseID string, expiresAt time.Time, renewCount int) error
	// GetLeaseWithResources 读取租约及其资源登记，不返回明文 DSN。
	GetLeaseWithResources(leaseID string) (Lease, []StoredResource, error)
	// ListLeases 列出项目的 active 租约，并带上资源名称与元数据。
	ListLeases(projectID string) ([]Lease, error)
	// ListExpiredLeases 列出已到期且仍 active 的租约。
	ListExpiredLeases(now time.Time) ([]Lease, error)
	// CountActiveLeases 统计项目 active 租约数，过期未回收的也必须计入。
	CountActiveLeases(projectID string) (int, error)
	// ListActiveResourceNames 列出指定数据源/kind 的未回收资源名。
	ListActiveResourceNames(datasourceID, kind string) ([]string, error)
	// ListAllActiveResources 列出所有未回收资源，供对账使用。
	ListAllActiveResources() ([]StoredResource, error)
}

// StoredResource 是登记表里的一行资源，含定位回收所需的全部信息（不含明文 DSN）。
type StoredResource struct {
	ID           string
	LeaseID      string
	DataSourceID string
	Kind         string
	Name         string
	Meta         map[string]string
	Status       string
}

// BindingResolver 按项目 ID 解析绑定与项目展示名。
type BindingResolver interface {
	// Binding 返回项目绑定与用于生成资源名的项目展示名。
	Binding(projectID string) (ProjectBinding, string, error)
}

// ApprovalGate 决定一次带副作用的供给是否被放行。
//
// 注意：实现由 api 层注入；返回 nil 表示放行，返回错误表示拒绝或需要审批。
type ApprovalGate interface {
	// Authorize 在任何真实资源创建前统一审批全部计划。
	Authorize(ctx context.Context, projectID string, plans []Plan) error
}

// ManagerDeps 是 Manager 的外部依赖。
type ManagerDeps struct {
	Registry     RegistryReader
	Store        LeaseStore
	Bindings     BindingResolver
	ApprovalGate ApprovalGate
	Now          func() time.Time
	// ListDataSources 供后续 Reconcile 扫描全部数据源；不影响 Acquire/Renew/Release/List。
	ListDataSources func(ctx context.Context) ([]DataSource, error)
}

// Manager 管理一组共享生命周期的临时测试资源。
type Manager struct {
	registry     RegistryReader
	store        LeaseStore
	bindings     BindingResolver
	approvalGate ApprovalGate
	nowFunc      func() time.Time
	listSources  func(ctx context.Context) ([]DataSource, error)
}

type plannedResource struct {
	kind        string
	datasource  DataSource
	provisioner Provisioner
	plan        Plan
}

type createdResource struct {
	stored      StoredResource
	datasource  DataSource
	provisioner Provisioner
	resource    Resource
}

// NewManager 创建租约管理器。
//
// 注意：Now 为空时使用 time.Now；ApprovalGate 为空时视为无审批门禁，生产装配应显式注入门禁。
func NewManager(deps ManagerDeps) *Manager {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		registry: deps.Registry, store: deps.Store, bindings: deps.Bindings,
		approvalGate: deps.ApprovalGate, nowFunc: now, listSources: deps.ListDataSources,
	}
}

func (m *Manager) now() time.Time { return m.nowFunc() }

// Acquire 规划并申请项目绑定的临时资源。
//
// 执行顺序保证：先收齐所有 Plan 再统一审批，审批拒绝时尚未创建真实资源；持久化登记先于
// Provision，任何部分失败都会回收已成功资源并释放登记。成功返回的明文 DSN 仅供一次性出口使用。
func (m *Manager) Acquire(ctx context.Context, req AcquireRequest) (Lease, error) {
	log := logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{
		"project_id": req.ProjectID, "purpose": req.Purpose, "kinds": req.Kinds,
		"ttl_seconds": req.TTL.Seconds(),
	})
	log.Info("开始申请临时测试资源")
	if strings.TrimSpace(req.ProjectID) == "" {
		return Lease{}, errors.New("project_id 不能为空")
	}
	if strings.TrimSpace(req.Purpose) == "" {
		return Lease{}, errors.New("purpose 不能为空")
	}
	binding, projectName, err := m.bindings.Binding(req.ProjectID)
	if err != nil {
		return Lease{}, err
	}
	if binding.Postgres == nil && binding.Redis == nil {
		return Lease{}, ErrBindingMissing
	}
	kinds, err := selectedKinds(binding, req.Kinds)
	if err != nil {
		return Lease{}, err
	}
	limit := binding.MaxConcurrentLeases
	if limit <= 0 {
		limit = defaultQuota
	}
	active, err := m.store.CountActiveLeases(req.ProjectID)
	if err != nil {
		return Lease{}, err
	}
	if active >= limit {
		existing, listErr := m.store.ListLeases(req.ProjectID)
		if listErr != nil {
			return Lease{}, listErr
		}
		var details []string
		for _, lease := range existing {
			for _, resource := range lease.Resources {
				details = append(details, fmt.Sprintf("%s(%s, 到期 %s)", resource.Name, resource.Kind, lease.ExpiresAt.Format(time.RFC3339)))
			}
		}
		log.WithFields(map[string]any{"project_id": req.ProjectID, "active": active, "limit": limit}).Warn("项目临时资源配额已满")
		return Lease{}, fmt.Errorf("%w: active=%d limit=%d; 现存资源: %s", ErrQuotaExceeded, active, limit, strings.Join(details, ", "))
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = bindingTTL(binding)
	}
	if ttl <= 0 {
		return Lease{}, errors.New("TTL 必须为正数")
	}
	now := m.now()
	leaseID := uuid.NewString()
	nameSeed, err := NewResourceName(projectName)
	if err != nil {
		return Lease{}, err
	}

	plans := make([]plannedResource, 0, len(kinds))
	for _, kind := range kinds {
		planned, err := m.planResource(ctx, req.ProjectID, projectName, binding, kind, nameSeed)
		if err != nil {
			return Lease{}, err
		}
		plans = append(plans, planned)
	}
	// 先收齐 Plan 再审批，避免建了一半才被拒并留下真实资源。
	if m.approvalGate != nil {
		planValues := make([]Plan, 0, len(plans))
		for _, planned := range plans {
			planValues = append(planValues, planned.plan)
		}
		if err := m.approvalGate.Authorize(ctx, req.ProjectID, planValues); err != nil {
			log.WithField("project_id", req.ProjectID).Warn("临时资源申请被审批门禁拒绝")
			return Lease{}, err
		}
	}

	lease := Lease{ID: leaseID, ProjectID: req.ProjectID, Purpose: req.Purpose, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := m.store.InsertLease(lease); err != nil {
		return Lease{}, err
	}
	var created []createdResource
	fail := func(cause error) (Lease, error) {
		log.WithFields(map[string]any{"lease_id": leaseID, "reclaimed_count": len(created)}).WithErr(cause).Error("临时资源申请部分失败，开始全量回滚")
		m.rollbackCreated(ctx, created)
		if err := m.store.MarkLeaseReleased(leaseID); err != nil {
			log.WithField("lease_id", leaseID).WithErr(err).Error("回滚时释放租约登记失败")
		}
		return Lease{}, cause
	}

	for index := range plans {
		planned := plans[index]
		var storedID string
		var stored Resource
		for attempt := 1; attempt <= maxSlotRetries; attempt++ {
			stored = resourceForReservation(planned)
			storedID, err = m.store.InsertResource(leaseID, planned.datasource.ID, stored)
			if !errors.Is(err, ErrResourceSlotTaken) {
				break
			}
			log.WithFields(map[string]any{"lease_id": leaseID, "kind": planned.kind, "attempt": attempt}).Info("资源槽位冲突，重新规划")
			if attempt == maxSlotRetries {
				return fail(err)
			}
			seed := nameSeed
			if planned.kind == KindPostgres {
				seed, err = NewResourceName(projectName)
				if err != nil {
					return fail(err)
				}
			}
			planned, err = m.planResource(ctx, req.ProjectID, projectName, binding, planned.kind, seed)
			if err != nil {
				return fail(err)
			}
			plans[index] = planned
		}
		if err != nil {
			return fail(err)
		}
		log.WithFields(map[string]any{"lease_id": leaseID, "kind": planned.kind, "resource_name": planned.plan.ResourceName}).Info("开始供给临时资源")
		resource, provisionErr := planned.provisioner.Provision(ctx, planned.datasource, planned.plan)
		if provisionErr != nil {
			_ = m.store.MarkResourceReclaimed(storedID)
			return fail(provisionErr)
		}
		resource.Meta = mergeMeta(resource.Meta, stored.Meta)
		if err := m.store.MarkResourceActive(storedID); err != nil {
			_ = planned.provisioner.Reclaim(ctx, planned.datasource, resource)
			_ = m.store.MarkResourceReclaimed(storedID)
			return fail(err)
		}
		created = append(created, createdResource{
			stored:     StoredResource{ID: storedID, LeaseID: leaseID, DataSourceID: planned.datasource.ID, Kind: planned.kind, Name: resource.Name, Meta: resource.Meta, Status: "active"},
			datasource: planned.datasource, provisioner: planned.provisioner, resource: resource,
		})
		lease.Resources = append(lease.Resources, resource)
	}
	resourceNames := make([]string, 0, len(lease.Resources))
	for _, resource := range lease.Resources {
		resourceNames = append(resourceNames, resource.Name)
	}
	log.WithFields(map[string]any{"lease_id": leaseID, "project_id": req.ProjectID, "resource_names": resourceNames, "expires_at": lease.ExpiresAt}).Info("临时资源申请成功")
	return lease, nil
}

// Renew 延长租约到期时间；单次续租不超过项目默认 TTL，且租约总寿命不超过 24 小时。
func (m *Manager) Renew(ctx context.Context, leaseID string, ttl time.Duration) (Lease, error) {
	lease, _, err := m.store.GetLeaseWithResources(leaseID)
	if err != nil {
		return Lease{}, err
	}
	now := m.now()
	if now.Sub(lease.CreatedAt) > absoluteLeaseLifetime {
		logger.GetLogger().WithEntryName("DBProvisionLease").WithField("lease_id", leaseID).Warn("租约超过绝对寿命，拒绝续租")
		return Lease{}, ErrLeaseLifetimeExceeded
	}
	binding, _, err := m.bindings.Binding(lease.ProjectID)
	if err != nil {
		return Lease{}, err
	}
	limit := bindingTTL(binding)
	if ttl == 0 || ttl > limit {
		ttl = limit
	}
	if ttl <= 0 {
		return Lease{}, errors.New("TTL 必须为正数")
	}
	lease.ExpiresAt = now.Add(ttl)
	lease.RenewCount++
	if err := m.store.UpdateLeaseExpiry(leaseID, lease.ExpiresAt, lease.RenewCount); err != nil {
		return Lease{}, err
	}
	logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{
		"lease_id": leaseID, "new_expires_at": lease.ExpiresAt, "renew_count": lease.RenewCount,
	}).Info("租约续期成功")
	return lease, nil
}

// Release 回收租约的全部资源并标记租约释放；单个资源失败不会中断其余资源回收。
func (m *Manager) Release(ctx context.Context, leaseID string) error {
	lease, resources, err := m.store.GetLeaseWithResources(leaseID)
	if errors.Is(err, ErrLeaseNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	reclaimed := 0
	for _, stored := range resources {
		if stored.Status == "reclaimed" {
			continue
		}
		ds, dsErr := m.datasourceFor(ctx, stored)
		if dsErr != nil {
			errs = append(errs, dsErr)
			logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{"lease_id": leaseID, "kind": stored.Kind, "resource_name": stored.Name}).WithErr(dsErr).Error("定位租约资源数据源失败")
			continue
		}
		provisioner, ok := LookupProvisioner(stored.Kind)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrUnsupportedKind, stored.Kind))
			continue
		}
		resource := Resource{Kind: stored.Kind, Name: stored.Name, Meta: stored.Meta}
		if reclaimErr := provisioner.Reclaim(ctx, ds, resource); reclaimErr != nil {
			errs = append(errs, reclaimErr)
			logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{"lease_id": leaseID, "kind": stored.Kind, "resource_name": stored.Name}).WithErr(reclaimErr).Error("租约资源回收失败")
		}
		if markErr := m.store.MarkResourceReclaimed(stored.ID); markErr != nil {
			errs = append(errs, markErr)
		} else {
			reclaimed++
		}
	}
	if markErr := m.store.MarkLeaseReleased(leaseID); markErr != nil {
		errs = append(errs, markErr)
	}
	logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{"lease_id": leaseID, "reclaimed_count": reclaimed}).Info("租约回收完成")
	_ = lease
	return errors.Join(errs...)
}

// List 列出项目租约及资源名称；返回值始终清空 DSN，供列表接口和日志消费。
func (m *Manager) List(ctx context.Context, projectID string) ([]Lease, error) {
	leases, err := m.store.ListLeases(projectID)
	if err != nil {
		return nil, err
	}
	for index := range leases {
		for resourceIndex := range leases[index].Resources {
			// List 必须清空 DSN，因为它不是明文凭据的一次性出口。
			leases[index].Resources[resourceIndex] = leases[index].Resources[resourceIndex].WithoutSecret()
		}
	}
	return leases, nil
}

// DryRun 规划并短暂创建每种资源以验证供给路径，返回步骤与脱敏 DSN。
//
// 注意：DryRun 刻意跳过配额和审批，不写租约登记；无论中途成功或失败，已创建资源都会在返回前回收。
func (m *Manager) DryRun(ctx context.Context, projectID string) (result DryRunResult, err error) {
	log := logger.GetLogger().WithEntryName("DBProvisionLease").WithField("project_id", projectID)
	log.Info("开始临时资源试跑")
	binding, projectName, err := m.bindings.Binding(projectID)
	if err != nil {
		return DryRunResult{Succeeded: false, Error: err.Error()}, err
	}
	kinds, err := selectedKinds(binding, nil)
	if err != nil {
		return DryRunResult{Succeeded: false, Error: err.Error()}, err
	}
	nameSeed, err := NewResourceName(projectName)
	if err != nil {
		return DryRunResult{Succeeded: false, Error: err.Error()}, err
	}
	plans := make([]plannedResource, 0, len(kinds))
	for _, kind := range kinds {
		planned, planErr := m.planResource(ctx, projectID, projectName, binding, kind, nameSeed)
		if planErr != nil {
			result.Plans = planValues(plans)
			result.Succeeded = false
			result.Error = planErr.Error()
			log.WithErr(planErr).Error("临时资源试跑计划失败")
			return result, planErr
		}
		plans = append(plans, planned)
	}
	result.Plans = planValues(plans)
	var created []createdResource
	defer func() {
		for _, item := range created {
			if reclaimErr := item.provisioner.Reclaim(ctx, item.datasource, item.resource); reclaimErr != nil {
				log.WithField("resource_name", item.resource.Name).WithErr(reclaimErr).Error("试跑资源回收失败")
				result.Succeeded = false
				if result.Error == "" {
					result.Error = reclaimErr.Error()
				}
			}
		}
		log.WithFields(map[string]any{"project_id": projectID, "succeeded": result.Succeeded, "plan_count": len(result.Plans)}).Info("临时资源试跑完成")
	}()
	for _, planned := range plans {
		resource, provisionErr := planned.provisioner.Provision(ctx, planned.datasource, planned.plan)
		if provisionErr != nil {
			result.Succeeded = false
			result.Error = provisionErr.Error()
			return result, provisionErr
		}
		resource.Meta = mergeMeta(resource.Meta, map[string]string{"datasource_name": planned.datasource.Name})
		created = append(created, createdResource{datasource: planned.datasource, provisioner: planned.provisioner, resource: resource})
		result.MaskedDSNs = append(result.MaskedDSNs, maskDSN(resource.DSN))
	}
	result.Succeeded = true
	return result, nil
}

// Reconcile 回收过期租约并对已登记数据源执行安全对账。
//
// 注意：单个租约、数据源或孤儿回收失败只记录到报告，不中断同一轮的其余回收动作。
func (m *Manager) Reconcile(ctx context.Context) (ReconcileReport, error) {
	var report ReconcileReport
	expired, err := m.store.ListExpiredLeases(m.now())
	if err != nil {
		return report, err
	}
	for _, lease := range expired {
		if releaseErr := m.Release(ctx, lease.ID); releaseErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("lease %s: %v", lease.ID, releaseErr))
			logger.GetLogger().WithEntryName("DBProvisionReaper").WithField("lease_id", lease.ID).WithErr(releaseErr).Error("回收过期租约失败")
			continue
		}
		report.ExpiredReclaimed++
	}
	if m.listSources == nil {
		return report, nil
	}
	sources, err := m.listSources(ctx)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, nil
	}
	active, err := m.store.ListAllActiveResources()
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, nil
	}
	for _, ds := range sources {
		provisioner, ok := LookupProvisioner(ds.Kind)
		if !ok {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", ds.Kind, ErrUnsupportedKind))
			continue
		}
		var known []Resource
		for _, resource := range active {
			if resource.DataSourceID == ds.ID && resource.Kind == ds.Kind && resource.Status != "reclaimed" {
				known = append(known, Resource{Kind: resource.Kind, Name: resource.Name, Meta: resource.Meta})
			}
		}
		orphans, reconcileErr := provisioner.Reconcile(ctx, ds, known)
		if reconcileErr != nil {
			report.Errors = append(report.Errors, reconcileErr.Error())
			continue
		}
		for _, orphan := range orphans {
			resource := Resource{Kind: orphan.Kind, Name: orphan.Name}
			if reclaimErr := provisioner.Reclaim(ctx, ds, resource); reclaimErr != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("orphan %s: %v", orphan.Name, reclaimErr))
				logger.GetLogger().WithEntryName("DBProvisionReaper").WithField("orphan_name", orphan.Name).WithErr(reclaimErr).Error("回收对账孤儿失败")
				continue
			}
			report.OrphansReclaimed = append(report.OrphansReclaimed, orphan)
		}
	}
	return report, nil
}

func planValues(plans []plannedResource) []Plan {
	result := make([]Plan, 0, len(plans))
	for _, planned := range plans {
		result = append(result, planned.plan)
	}
	return result
}

var credentialPattern = regexp.MustCompile(`^([^:]+://[^/@:]*):([^@/]*)@`)

func maskDSN(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && parsed.User != nil {
		username := parsed.User.Username()
		parsed.User = url.UserPassword(username, "***")
		// url.UserPassword 会把星号编码成 %2A；这里的星号是对外界面约定的
		// 脱敏占位符，不是原始凭据，恢复成可读形式便于 AI/桌面端识别。
		return strings.ReplaceAll(parsed.String(), "%2A%2A%2A", "***")
	}
	if replaced := credentialPattern.ReplaceAllString(dsn, `${1}:***@`); replaced != dsn {
		return replaced
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&password=***"
	}
	return dsn + "?password=***"
}

func (m *Manager) planResource(ctx context.Context, projectID, projectName string, binding ProjectBinding, kind, nameSeed string) (plannedResource, error) {
	datasourceName, err := bindingDataSourceName(binding, kind)
	if err != nil {
		return plannedResource{}, err
	}
	ds, err := m.registry.GetByName(ctx, kind, datasourceName)
	if err != nil {
		return plannedResource{}, err
	}
	provisioner, ok := LookupProvisioner(kind)
	if !ok {
		return plannedResource{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
	taken, err := m.store.ListActiveResourceNames(ds.ID, kind)
	if err != nil {
		return plannedResource{}, err
	}
	plan, err := provisioner.Plan(ctx, ds, PlanRequest{ProjectID: projectID, NameSeed: nameSeed, Binding: binding, TakenHints: taken})
	if err != nil {
		return plannedResource{}, err
	}
	_ = projectName
	return plannedResource{kind: kind, datasource: ds, provisioner: provisioner, plan: plan}, nil
}

func (m *Manager) rollbackCreated(ctx context.Context, resources []createdResource) {
	for _, created := range resources {
		if err := created.provisioner.Reclaim(ctx, created.datasource, created.resource); err != nil {
			logger.GetLogger().WithEntryName("DBProvisionLease").WithFields(map[string]any{"kind": created.stored.Kind, "resource_name": created.stored.Name}).WithErr(err).Error("回滚资源回收失败")
		}
		if err := m.store.MarkResourceReclaimed(created.stored.ID); err != nil {
			logger.GetLogger().WithEntryName("DBProvisionLease").WithField("resource_id", created.stored.ID).WithErr(err).Error("回滚资源登记失败")
		}
	}
}

func (m *Manager) datasourceFor(ctx context.Context, resource StoredResource) (DataSource, error) {
	name := resource.Meta["datasource_name"]
	if name == "" {
		return DataSource{}, fmt.Errorf("资源 %s 缺少 datasource_name 元数据", resource.Name)
	}
	return m.registry.GetByName(ctx, resource.Kind, name)
}

func selectedKinds(binding ProjectBinding, requested []string) ([]string, error) {
	if len(requested) == 0 {
		result := make([]string, 0, 2)
		if binding.Postgres != nil {
			result = append(result, KindPostgres)
		}
		if binding.Redis != nil {
			result = append(result, KindRedis)
		}
		return result, nil
	}
	seen := make(map[string]struct{}, len(requested))
	result := make([]string, 0, len(requested))
	for _, kind := range requested {
		if _, ok := seen[kind]; ok {
			continue
		}
		if (kind == KindPostgres && binding.Postgres == nil) || (kind == KindRedis && binding.Redis == nil) {
			return nil, ErrBindingMissing
		}
		if kind != KindPostgres && kind != KindRedis {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
		}
		seen[kind] = struct{}{}
		result = append(result, kind)
	}
	if len(result) == 0 {
		return nil, ErrBindingMissing
	}
	return result, nil
}

func bindingDataSourceName(binding ProjectBinding, kind string) (string, error) {
	switch kind {
	case KindPostgres:
		if binding.Postgres == nil || strings.TrimSpace(binding.Postgres.DataSourceName) == "" {
			return "", ErrBindingMissing
		}
		return binding.Postgres.DataSourceName, nil
	case KindRedis:
		if binding.Redis == nil || strings.TrimSpace(binding.Redis.DataSourceName) == "" {
			return "", ErrBindingMissing
		}
		return binding.Redis.DataSourceName, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
}

func bindingTTL(binding ProjectBinding) time.Duration {
	if binding.DefaultTTLMinutes <= 0 {
		return defaultTTL
	}
	return time.Duration(binding.DefaultTTLMinutes) * time.Minute
}

func resourceForReservation(planned plannedResource) Resource {
	meta := mergeMeta(planned.plan.Detail, map[string]string{"datasource_name": planned.datasource.Name})
	return Resource{Kind: planned.kind, Name: planned.plan.ResourceName, Meta: meta}
}

func mergeMeta(primary, fallback map[string]string) map[string]string {
	result := make(map[string]string, len(primary)+len(fallback))
	for key, value := range fallback {
		result[key] = value
	}
	for key, value := range primary {
		result[key] = value
	}
	return result
}
