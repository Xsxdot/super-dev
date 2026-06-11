// Package process tests process exit evidence helpers.
//
// 职责：
//   - 验证 stderr ring buffer 的固定容量保留语义
//
// 边界：
//   - 不启动真实进程，只测试纯数据结构
package process

import "testing"

func TestStderrRing_KeepsLastN(t *testing.T) {
	r := newStderrRing(3)
	for i := 1; i <= 5; i++ {
		r.push("line" + string(rune('0'+i)))
	}
	got := r.tail()
	want := []string{"line3", "line4", "line5"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tail[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestStderrRing_FewerThanCap(t *testing.T) {
	r := newStderrRing(100)
	r.push("only")
	if got := r.tail(); len(got) != 1 || got[0] != "only" {
		t.Fatalf("tail=%v want [only]", got)
	}
}
