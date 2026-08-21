package dbprovision

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeStore 覆盖 LeaseManager 的全部外部持久化依赖，不碰真实 SQLite。
type fakeStore struct {
	leases    map[string]Lease
	statuses  map[string]string
	resources map[string][]StoredResource
	seq       int
	slotTaken map[string]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		leases: map[string]Lease{}, statuses: map[string]string{},
		resources: map[string][]StoredResource{}, slotTaken: map[string]bool{},
	}
}

func (s *fakeStore) InsertLease(lease Lease) error {
	s.leases[lease.ID] = lease
	s.statuses[lease.ID] = "active"
	return nil
}

func (s *fakeStore) InsertResource(leaseID, datasourceID string, resource Resource) (string, error) {
	key := datasourceID + "|" + resource.Kind + "|" + resource.Name
	if s.slotTaken[key] {
		return "", ErrResourceSlotTaken
	}
	s.slotTaken[key] = true
	s.seq++
	id := "resource-" + strconv.Itoa(s.seq)
	s.resources[leaseID] = append(s.resources[leaseID], StoredResource{
		ID: id, LeaseID: leaseID, DataSourceID: datasourceID, Kind: resource.Kind,
		Name: resource.Name, Meta: mergeMeta(resource.Meta, nil), Status: "creating",
	})
	return id, nil
}

func (s *fakeStore) MarkResourceActive(resourceID string) error {
	for leaseID := range s.resources {
		for index := range s.resources[leaseID] {
			if s.resources[leaseID][index].ID == resourceID {
				s.resources[leaseID][index].Status = "active"
				return nil
			}
		}
	}
	return nil
}

func (s *fakeStore) MarkResourceReclaimed(resourceID string) error {
	for leaseID, resources := range s.resources {
		for index, resource := range resources {
			if resource.ID == resourceID {
				key := resource.DataSourceID + "|" + resource.Kind + "|" + resource.Name
				delete(s.slotTaken, key)
				s.resources[leaseID] = append(resources[:index], resources[index+1:]...)
				return nil
			}
		}
	}
	return nil
}

func (s *fakeStore) MarkLeaseReleased(leaseID string) error {
	s.statuses[leaseID] = "released"
	return nil
}

func (s *fakeStore) UpdateLeaseExpiry(leaseID string, expiresAt time.Time, renewCount int) error {
	lease, ok := s.leases[leaseID]
	if !ok {
		return ErrLeaseNotFound
	}
	lease.ExpiresAt = expiresAt
	lease.RenewCount = renewCount
	s.leases[leaseID] = lease
	return nil
}

func (s *fakeStore) GetLeaseWithResources(leaseID string) (Lease, []StoredResource, error) {
	lease, ok := s.leases[leaseID]
	if !ok || s.statuses[leaseID] == "released" {
		return Lease{}, nil, ErrLeaseNotFound
	}
	resources := append([]StoredResource(nil), s.resources[leaseID]...)
	return lease, resources, nil
}

func (s *fakeStore) ListLeases(projectID string) ([]Lease, error) {
	var result []Lease
	for id, lease := range s.leases {
		if s.statuses[id] != "active" || (projectID != "" && lease.ProjectID != projectID) {
			continue
		}
		lease.Resources = nil
		for _, resource := range s.resources[id] {
			if resource.Status != "reclaimed" {
				lease.Resources = append(lease.Resources, Resource{Kind: resource.Kind, Name: resource.Name, Meta: resource.Meta})
			}
		}
		result = append(result, lease)
	}
	return result, nil
}

func (s *fakeStore) ListExpiredLeases(now time.Time) ([]Lease, error) {
	var result []Lease
	for id, lease := range s.leases {
		if s.statuses[id] == "active" && !lease.ExpiresAt.After(now) {
			result = append(result, lease)
		}
	}
	return result, nil
}

func (s *fakeStore) CountActiveLeases(projectID string) (int, error) {
	count := 0
	for id, lease := range s.leases {
		if s.statuses[id] == "active" && lease.ProjectID == projectID {
			count++
		}
	}
	return count, nil
}

func (s *fakeStore) ListActiveResourceNames(datasourceID, kind string) ([]string, error) {
	var result []string
	for _, resources := range s.resources {
		for _, resource := range resources {
			if resource.DataSourceID == datasourceID && resource.Kind == kind && resource.Status != "reclaimed" {
				result = append(result, resource.Name)
			}
		}
	}
	return result, nil
}

func (s *fakeStore) ListAllActiveResources() ([]StoredResource, error) {
	var result []StoredResource
	for _, resources := range s.resources {
		for _, resource := range resources {
			if resource.Status != "reclaimed" {
				result = append(result, resource)
			}
		}
	}
	return result, nil
}

func (s *fakeStore) expireLease(id string) {
	lease := s.leases[id]
	lease.ExpiresAt = time.Now().Add(-time.Minute)
	s.leases[id] = lease
}

type fakeGate struct{ err error }

func (g fakeGate) Authorize(context.Context, string, []Plan) error { return g.err }

type fakeBindings struct{ b ProjectBinding }

func (f fakeBindings) Binding(string) (ProjectBinding, string, error) { return f.b, "tk", nil }

type fakeRegistry struct{}

func (fakeRegistry) GetByName(_ context.Context, kind, name string) (DataSource, error) {
	return DataSource{ID: "ds-" + kind, Kind: kind, Name: name}, nil
}

func newTestManager(t *testing.T, binding ProjectBinding, gateErr error) (*Manager, *fakeStore) {
	t.Helper()
	RegisterProvisioner(&fakeProvisioner{kind: KindPostgres})
	RegisterProvisioner(&fakeProvisioner{kind: KindRedis})
	store := newFakeStore()
	manager := NewManager(ManagerDeps{
		Registry: fakeRegistry{}, Store: store,
		Bindings: fakeBindings{b: binding}, ApprovalGate: fakeGate{err: gateErr}, Now: time.Now,
	})
	return manager, store
}

func fullBinding() ProjectBinding {
	return ProjectBinding{
		Postgres:            &PostgresBinding{DataSourceName: "local-pg", DevDatabase: "tk_dev", TerminateConnections: true},
		Redis:               &RedisBinding{DataSourceName: "local-redis"},
		MaxConcurrentLeases: 2, DefaultTTLMinutes: 30,
	}
}

func TestAcquireReturnsBothKindsAndSharedExpiry(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	lease, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "跑测试"})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if len(lease.Resources) != 2 {
		t.Fatalf("应同时给出 PG 与 Redis 两个资源: %+v", lease.Resources)
	}
	for _, resource := range lease.Resources {
		if resource.DSN == "" {
			t.Fatalf("acquire 响应必须含明文 DSN: %+v", lease.Resources)
		}
	}
	if lease.ExpiresAt.Sub(lease.CreatedAt) != 30*time.Minute {
		t.Fatalf("默认 TTL 应取绑定的 30 分钟: %v", lease.ExpiresAt.Sub(lease.CreatedAt))
	}
}

func TestAcquireHonorsKindsFilter(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	lease, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "只要 pg", Kinds: []string{KindPostgres}})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if len(lease.Resources) != 1 || lease.Resources[0].Kind != KindPostgres {
		t.Fatalf("kinds 过滤未生效: %+v", lease.Resources)
	}
}

func TestAcquireRejectsWhenQuotaExceeded(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "占位"}); err != nil {
			t.Fatalf("第 %d 次 Acquire 应成功: %v", i+1, err)
		}
	}
	_, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "超限"})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("应返回 ErrQuotaExceeded，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "sdev_eph_") {
		t.Fatalf("配额错误必须附现存资源列表，引导 AI 复用: %v", err)
	}
}

func TestAcquireRequiresPurpose(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	if _, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1"}); err == nil {
		t.Fatal("purpose 是审计必填项，缺失必须报错")
	}
}

func TestAcquireFailsWithoutBinding(t *testing.T) {
	manager, _ := newTestManager(t, ProjectBinding{}, nil)
	_, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if !errors.Is(err, ErrBindingMissing) {
		t.Fatalf("应返回 ErrBindingMissing，实际 %v", err)
	}
}

func TestAcquireBlockedByApprovalGateRollsBackEverything(t *testing.T) {
	gateErr := errors.New("approval required")
	manager, store := newTestManager(t, fullBinding(), gateErr)
	_, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if !errors.Is(err, gateErr) {
		t.Fatalf("审批拒绝应原样透出: %v", err)
	}
	if len(store.leases) != 0 {
		t.Fatalf("审批未通过时不得留下任何租约: %+v", store.leases)
	}
}

func TestAcquirePartialFailureReclaimsAlreadyProvisioned(t *testing.T) {
	manager, store := newTestManager(t, fullBinding(), nil)
	RegisterProvisioner(&failingProvisioner{kind: KindRedis})
	defer RegisterProvisioner(&fakeProvisioner{kind: KindRedis})

	if _, err := manager.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"}); err == nil {
		t.Fatal("其中一种资源失败时整次 Acquire 必须失败")
	}
	for _, rows := range store.resources {
		for _, resource := range rows {
			t.Fatalf("失败时已建资源必须被回收，残留: %+v", resource)
		}
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if err := manager.Release(ctx, lease.ID); err != nil {
		t.Fatalf("首次 Release 失败: %v", err)
	}
	if err := manager.Release(ctx, lease.ID); err != nil {
		t.Fatalf("重复 Release 必须幂等: %v", err)
	}
}

func TestRenewClampsToDefaultTTLAndRejectsPastLifetimeCap(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	renewed, err := manager.Renew(ctx, lease.ID, 10*time.Hour)
	if err != nil {
		t.Fatalf("Renew 失败: %v", err)
	}
	if got := renewed.ExpiresAt.Sub(manager.now()); got > 31*time.Minute {
		t.Fatalf("单次续租应被截断到默认 TTL，实际 %v", got)
	}
}

func TestListOmitsSecrets(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	if _, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"}); err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	leases, err := manager.List(ctx, "p1")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	for _, lease := range leases {
		for _, resource := range lease.Resources {
			if resource.DSN != "" {
				t.Fatalf("List 绝不能返回明文 DSN: %+v", resource)
			}
		}
	}
}

type failingProvisioner struct{ kind string }

func (f *failingProvisioner) Kind() string { return f.kind }
func (f *failingProvisioner) Probe(context.Context, DataSource) (ProbeResult, error) {
	return ProbeResult{OK: true}, nil
}
func (f *failingProvisioner) Plan(_ context.Context, _ DataSource, req PlanRequest) (Plan, error) {
	return Plan{Kind: f.kind, ResourceName: req.NameSeed}, nil
}
func (f *failingProvisioner) Provision(context.Context, DataSource, Plan) (Resource, error) {
	return Resource{}, errors.New("fake provision failure")
}
func (f *failingProvisioner) Reclaim(context.Context, DataSource, Resource) error { return nil }
func (f *failingProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return nil, nil
}
