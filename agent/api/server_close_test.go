package api

import "testing"

// TestCloseRunsOnce 验证 App.Close 多次调用时清理逻辑只执行一次。
func TestCloseRunsOnce(t *testing.T) {
	var count int
	a := &App{}
	a.closeFn = func() { count++ }

	a.Close()
	a.Close()

	if count != 1 {
		t.Fatalf("期望 Close 内部清理只执行 1 次，实际执行 %d 次", count)
	}
}
