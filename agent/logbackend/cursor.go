// cursor.go 提供 SQLite 后端的游标编解码。
//
// 职责：
//   - SQLite 用 rowid（int64）作为游标 ID，对外编码成不透明 string
//   - 上层（handler/前端/Federated）只透传该 string，不解释其内容
//
// 边界：
//   - 只服务 SQLite 后端；PG/云后端各自有自己的游标编码
package logbackend

import "strconv"

// encodeSQLiteCursor 把 SQLite rowid 编码为不透明游标 ID 字符串。
func encodeSQLiteCursor(rowid int64) string {
	if rowid == 0 {
		return ""
	}
	return strconv.FormatInt(rowid, 10)
}

// decodeSQLiteCursor 把游标 ID 字符串还原为 rowid。
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
