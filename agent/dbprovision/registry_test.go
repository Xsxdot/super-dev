package dbprovision

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newTestRegistry(t *testing.T) (*FileRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "datasources.json")
	reg, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("NewFileRegistry 失败: %v", err)
	}
	RegisterProvisioner(&fakeProvisioner{kind: "fake-reg"})
	return reg, path
}

func sampleDS() DataSource {
	return DataSource{Kind: "fake-reg", Name: "local-fake", Host: "127.0.0.1", Port: 1234, Password: "s3cret"}
}

func TestRegistryAddAssignsIDAndPersists(t *testing.T) {
	reg, path := newTestRegistry(t)
	ctx := context.Background()

	got, err := reg.Add(ctx, sampleDS())
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if got.ID == "" {
		t.Fatal("Add 必须分配 ID")
	}
	if !got.Probe.OK {
		t.Fatal("Add 必须写入探测结果")
	}

	reloaded, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	all, err := reloaded.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(all) != 1 || all[0].Password != "s3cret" {
		t.Fatalf("落盘内容不对: %+v", all)
	}
}

func TestRegistryFilePermissionIs0600(t *testing.T) {
	reg, path := newTestRegistry(t)
	if _, err := reg.Add(context.Background(), sampleDS()); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("凭据文件权限必须是 0600，实际 %o", perm)
	}
}

func TestRegistryRejectsDuplicateNameWithinKind(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	if _, err := reg.Add(ctx, sampleDS()); err != nil {
		t.Fatalf("首次 Add 失败: %v", err)
	}
	if _, err := reg.Add(ctx, sampleDS()); err == nil {
		t.Fatal("同 kind 下重名必须被拒绝")
	}
}

func TestRegistryGetByName(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	if _, err := reg.Add(ctx, sampleDS()); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	got, err := reg.GetByName(ctx, "fake-reg", "local-fake")
	if err != nil {
		t.Fatalf("GetByName 失败: %v", err)
	}
	if got.Name != "local-fake" {
		t.Fatalf("GetByName 返回了错误的记录: %+v", got)
	}
	if _, err := reg.GetByName(ctx, "fake-reg", "nope"); err == nil {
		t.Fatal("找不到的名字必须报错")
	}
}

func TestRegistryRemoveBlockedByActiveLeases(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	added, err := reg.Add(ctx, sampleDS())
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	reg.SetActiveLeaseCounter(func(string) int { return 2 })

	if err := reg.Remove(ctx, added.ID, false); err == nil {
		t.Fatal("有活跃租约时移除必须被拒绝")
	}
	if err := reg.Remove(ctx, added.ID, true); err != nil {
		t.Fatalf("force 移除应成功: %v", err)
	}
}

func TestRegistryAddRejectsUnprobeableSource(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ds := sampleDS()
	ds.Kind = "kind-without-provisioner"
	if _, err := reg.Add(context.Background(), ds); err == nil {
		t.Fatal("没有对应 provisioner 的 kind 必须被拒绝")
	}
}
