package dbprovision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReconcileReclaimsExpiredLeases(t *testing.T) {
	manager, store := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, err := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x", TTL: time.Minute})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	store.expireLease(lease.ID)

	report, err := manager.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if report.ExpiredReclaimed != 1 {
		t.Fatalf("应回收 1 个过期租约: %+v", report)
	}
	if n, _ := store.CountActiveLeases("p1"); n != 0 {
		t.Fatalf("回收后活跃租约应为 0，实际 %d", n)
	}
}

func TestReconcileReclaimsProvisionerOrphans(t *testing.T) {
	manager, _ := newTestManager(t, fullBinding(), nil)
	RegisterProvisioner(&orphanReportingProvisioner{kind: KindPostgres, orphan: "sdev_eph_tk_ghost1"})
	defer RegisterProvisioner(&fakeProvisioner{kind: KindPostgres})
	manager.listSources = func(context.Context) ([]DataSource, error) {
		return []DataSource{{ID: "ds-postgres", Kind: KindPostgres, Name: "local-pg"}}, nil
	}
	report, err := manager.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if len(report.OrphansReclaimed) != 1 || report.OrphansReclaimed[0].Name != "sdev_eph_tk_ghost1" {
		t.Fatalf("应回收 provisioner 报告的孤儿: %+v", report)
	}
}

func TestDryRunLeavesNothingBehindAndMasksSecrets(t *testing.T) {
	manager, store := newTestManager(t, fullBinding(), nil)
	result, err := manager.DryRun(context.Background(), "p1")
	if err != nil {
		t.Fatalf("DryRun 失败: %v", err)
	}
	if !result.Succeeded || len(result.Plans) != 2 {
		t.Fatalf("试跑应覆盖两种资源: %+v", result)
	}
	for _, dsn := range result.MaskedDSNs {
		if !strings.Contains(dsn, "***") {
			t.Fatalf("试跑返回的 DSN 必须脱敏: %s", dsn)
		}
	}
	if n, _ := store.CountActiveLeases("p1"); n != 0 {
		t.Fatalf("试跑不得占用配额或留下租约，实际 %d", n)
	}
}

func TestReaperTicksAndStops(t *testing.T) {
	manager, store := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, _ := manager.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x", TTL: time.Minute})
	store.expireLease(lease.ID)

	reaper := NewReaper(manager, 10*time.Millisecond)
	reaper.Start(ctx)
	defer reaper.Stop()

	deadline := time.After(2 * time.Second)
	for {
		if n, _ := store.CountActiveLeases("p1"); n == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("巡检未在预期时间内回收过期租约")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type orphanReportingProvisioner struct {
	kind   string
	orphan string
	called bool
}

func (p *orphanReportingProvisioner) Kind() string { return p.kind }
func (p *orphanReportingProvisioner) Probe(context.Context, DataSource) (ProbeResult, error) {
	return ProbeResult{OK: true}, nil
}
func (p *orphanReportingProvisioner) Plan(context.Context, DataSource, PlanRequest) (Plan, error) {
	return Plan{Kind: p.kind}, nil
}
func (p *orphanReportingProvisioner) Provision(context.Context, DataSource, Plan) (Resource, error) {
	return Resource{}, errors.New("orphan fake does not provision")
}
func (p *orphanReportingProvisioner) Reclaim(context.Context, DataSource, Resource) error {
	p.called = true
	return nil
}
func (p *orphanReportingProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return []Orphan{{Kind: p.kind, Name: p.orphan, Reason: "test orphan"}}, nil
}
