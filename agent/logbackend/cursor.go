// cursor.go 提供 SQLite 后端的游标编解码。
//
// 职责：
//   - SQLite 单 deployment 读路径用 seq 作为游标 ID，对外编码成不透明 string
//   - Search 跨 deployment，需要全库 tiebreak，仍用 rowid 编码
//   - 上层（handler/前端/Federated）只透传该 string，不解释其内容
//
// 边界：
//   - 只服务 SQLite 后端；PG/云后端各自有自己的游标编码
package logbackend

import "strconv"

// encodeSQLiteCursor 把 SQLite 数值游标编码为不透明游标 ID 字符串。
func encodeSQLiteCursor(n int64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// decodeSQLiteCursor 把游标 ID 字符串还原为数值游标。
// 空串或非数值（如误投的云后端 token）返回 0，表示「从最新开始」。
func decodeSQLiteCursor(id string) int64 {
	if id == "" {
		return 0
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// decodeSQLiteCursorUint 把游标 ID 还原为 uint64；负数视为无效游标。
func decodeSQLiteCursorUint(id string) uint64 {
	if id == "" {
		return 0
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// encodeLogEntrySeqCursor 返回单 deployment 读路径游标；旧数据无 seq 时退回 rowid。
func encodeLogEntrySeqCursor(seq uint64, rowid int64) string {
	if seq > 0 {
		return strconv.FormatUint(seq, 10)
	}
	return encodeSQLiteCursor(rowid)
}
