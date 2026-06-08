// cursor_test.go 验证 SQLite 游标编解码 helper。
//
// 职责：
//   - 覆盖 SQLite rowid 到不透明游标字符串的往返
//   - 覆盖空串和异常字符串的安全解码
//
// 边界：
//   - 不测试 store 查询语义，只验证 logbackend 层游标桥接规则
package logbackend

import "testing"

func TestSQLiteCursorRoundTrip(t *testing.T) {
	got := decodeSQLiteCursor(encodeSQLiteCursor(12345))
	if got != 12345 {
		t.Fatalf("round trip: want 12345, got %d", got)
	}
}

func TestSQLiteCursorEmptyDecodesToZero(t *testing.T) {
	if got := decodeSQLiteCursor(""); got != 0 {
		t.Fatalf("empty cursor: want 0, got %d", got)
	}
}

func TestSQLiteCursorNonNumericDecodesToZero(t *testing.T) {
	// 不透明游标被上层透传，若 sqlite 后端收到非数值（如云后端 token 误投），
	// 解码回 0（表示从最新开始），避免错误游标导致 panic。
	if got := decodeSQLiteCursor("not-a-number"); got != 0 {
		t.Fatalf("non-numeric cursor: want 0, got %d", got)
	}
}
