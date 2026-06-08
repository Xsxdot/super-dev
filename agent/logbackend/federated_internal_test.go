// federated_internal_test.go 验证 FederatedBackend 包内排序细节。
//
// 职责：
//   - 覆盖同时间戳时的游标 ID 字符串兜底排序
//
// 边界：
//   - 只测试包内 lessLogEntry，不构造完整 FederatedBackend
package logbackend

import (
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

func TestFederated_LessLogEntryStringTiebreak(t *testing.T) {
	ts := time.Now()
	a := model.LogEntry{Timestamp: ts, ID: 9}   // encode -> "9"
	b := model.LogEntry{Timestamp: ts, ID: 100} // encode -> "100"
	// 字典序："100" < "9"，故 b 应排在 a 前；若仍是数值序则会失败。
	if !lessLogEntry(b, a) {
		t.Fatalf("expected lexical order '100' < '9' (b before a)")
	}
	if lessLogEntry(a, b) {
		t.Fatalf("expected a('9') NOT before b('100') under lexical order")
	}
}
