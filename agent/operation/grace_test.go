// grace_test.go 验证项目级审批豁免窗口存储。
//
// 职责：
//   - 覆盖豁免窗口创建、续期、过期和未命中行为
//   - 验证同项目重复授权不会堆叠多条活动窗口
//
// 边界：
//   - 不测试 API 层是否应用豁免
//   - 不测试审批决策接口
package operation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGrantGraceCreatesActiveWindow(t *testing.T) {
	store := NewGraceFileStore(filepath.Join(t.TempDir(), "grace.json"))
	ctx := context.Background()
	if _, err := store.GrantGrace(ctx, "proj1", "user", "appr1", time.Minute); err != nil {
		t.Fatalf("grant: %v", err)
	}
	got, ok, err := store.ActiveGrace(ctx, "proj1")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if !ok {
		t.Fatal("expected active grace")
	}
	if got.GrantedFrom != "appr1" {
		t.Fatalf("granted_from = %q, want appr1", got.GrantedFrom)
	}
}

func TestGrantGraceRenewsWithoutStacking(t *testing.T) {
	store := NewGraceFileStore(filepath.Join(t.TempDir(), "grace.json"))
	ctx := context.Background()
	first, _ := store.GrantGrace(ctx, "proj1", "u", "a1", time.Minute)
	second, _ := store.GrantGrace(ctx, "proj1", "u", "a2", 2*time.Minute)
	if !second.ExpiresAt.After(first.ExpiresAt) {
		t.Fatal("second grant must extend expiry")
	}
	// 续期不堆叠：仍只有一个项目的活动窗口
	got, ok, _ := store.ActiveGrace(ctx, "proj1")
	if !ok || got.GrantedFrom != "a2" {
		t.Fatalf("renewed grant must carry latest source, got %+v ok=%v", got, ok)
	}
}

func TestActiveGraceExpired(t *testing.T) {
	store := NewGraceFileStore(filepath.Join(t.TempDir(), "grace.json"))
	ctx := context.Background()
	if _, err := store.GrantGrace(ctx, "proj1", "u", "a1", -time.Second); err != nil {
		t.Fatalf("grant: %v", err)
	}
	_, ok, err := store.ActiveGrace(ctx, "proj1")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if ok {
		t.Fatal("expired grace must not be active")
	}
}

func TestActiveGraceMiss(t *testing.T) {
	store := NewGraceFileStore(filepath.Join(t.TempDir(), "grace.json"))
	_, ok, err := store.ActiveGrace(context.Background(), "nope")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if ok {
		t.Fatal("unknown project must miss")
	}
}
