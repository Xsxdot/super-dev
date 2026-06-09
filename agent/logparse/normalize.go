// normalize.go 提供日志消息归一化，用于折叠判定的签名计算。
//
// 职责：
//   - 剥离时间戳前缀、掩码可变数字/IP，使"内容相同仅可变字段不同"的日志归一为同一签名
//
// 边界：
//   - 是折叠签名的唯一权威实现（前端不再重复计算，只认后端给的 fold_key）
//   - 纯函数，无状态，无 I/O
package logparse

import (
	"regexp"
	"strings"
)

var (
	// clockPrefixRe 匹配行首 HH:MM:SS[.fff]。
	clockPrefixRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d+)?\s*`)
	// isoPrefixRe 匹配行首 ISO 日期时间（T 或空格分隔）。
	isoPrefixRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?\s*`)
	// weekdayPrefixRe 匹配行首星期+日期（如 "Wed May 20 17:20:51 CST 2026"）。
	weekdayPrefixRe = regexp.MustCompile(`^[A-Z][a-z]{2}\s+[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{2,4}\s+\d{4}\s*`)
	// numAssignRe 匹配 =数字，掩码为 =*。
	numAssignRe = regexp.MustCompile(`=\d+`)
	// ipPortRe 匹配 IPv4:port，掩码为 *:*。
	ipPortRe = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)
)

// Normalize 归一化日志行，生成用于折叠判定的稳定签名。
//
// 参数：
//   - line: 原始日志消息
//
// 返回：
//   - 剥离时间戳前缀、掩码可变数字与 IP:端口、去除首尾空白后的签名串
//
// 注意：
//   - 必须与前端历史实现（desktop/src/lib/logEngine.ts normalize）行为对齐；
//     下沉后此函数为唯一权威，前端不再计算。
func Normalize(line string) string {
	result := line
	result = clockPrefixRe.ReplaceAllString(result, "")
	result = isoPrefixRe.ReplaceAllString(result, "")
	result = weekdayPrefixRe.ReplaceAllString(result, "")
	result = numAssignRe.ReplaceAllString(result, "=*")
	result = ipPortRe.ReplaceAllString(result, "*:*")
	return strings.TrimSpace(result)
}
