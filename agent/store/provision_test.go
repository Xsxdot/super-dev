package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/store"
)

func TestInsertAndGetLease(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	lease := dbprovision.Lease{
		ID: "lease-1", ProjectID: "proj-1", Purpose: "跑集成测试",
		CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	if err := s.InsertLease(lease); err != nil {
		t.Fatalf("InsertLease 失败: %v", err)
	}
	resID, err := s.InsertResource("lease-1", "ds-1", dbprovision.Resource{
		Kind: "postgres", Name: "sdev_eph_tk_aabbcc", Meta: map[string]string{"role": "sdev_eph_tk_aabbcc"},
	})
	if err != nil {
		t.Fatalf("InsertResource 失败: %v", err)
	}
	if err := s.MarkResourceActive(resID); err != nil {
		t.Fatalf("MarkResourceActive 失败: %v", err)
	}

	got, rows, err := s.GetLease("lease-1")
	if err != nil {
		t.Fatalf("GetLease 失败: %v", err)
	}
	if got.Purpose != "跑集成测试" || len(rows) != 1 || rows[0].Status != "active" {
		t.Fatalf("读回内容不对: %+v / %+v", got, rows)
	}
}

func TestResourceSlotUniqueBlocksConcurrentRedisDB(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"l1", "l2"} {
		if err := s.InsertLease(dbprovision.Lease{ID: id, ProjectID: "p", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	if _, err := s.InsertResource("l1", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"}); err != nil {
		t.Fatalf("首次占用 db7 应成功: %v", err)
	}
	_, err := s.InsertResource("l2", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"})
	if err == nil {
		t.Fatal("同一 datasource 上重复占用 db7 必须失败")
	}
	if !errors.Is(err, store.ErrResourceSlotTaken) {
		t.Fatalf("应返回 ErrResourceSlotTaken，实际 %v", err)
	}
}

func TestReclaimedResourceFreesSlot(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"l1", "l2"} {
		if err := s.InsertLease(dbprovision.Lease{ID: id, ProjectID: "p", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	resID, err := s.InsertResource("l1", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"})
	if err != nil {
		t.Fatalf("InsertResource 失败: %v", err)
	}
	if err := s.MarkResourceReclaimed(resID); err != nil {
		t.Fatalf("MarkResourceReclaimed 失败: %v", err)
	}
	if _, err := s.InsertResource("l2", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"}); err != nil {
		t.Fatalf("回收后 db7 应可再次分配: %v", err)
	}
}

func TestListExpiredLeasesAndCounts(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	must := func(l dbprovision.Lease) {
		if err := s.InsertLease(l); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	must(dbprovision.Lease{ID: "live", ProjectID: "p1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	must(dbprovision.Lease{ID: "dead", ProjectID: "p1", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	must(dbprovision.Lease{ID: "other", ProjectID: "p2", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	expired, err := s.ListExpiredLeases(now)
	if err != nil {
		t.Fatalf("ListExpiredLeases 失败: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != "dead" {
		t.Fatalf("过期租约筛选不对: %+v", expired)
	}

	n, err := s.CountActiveLeases("p1")
	if err != nil {
		t.Fatalf("CountActiveLeases 失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("p1 活跃租约数应为 2（含已过期未回收），实际 %d", n)
	}
}
