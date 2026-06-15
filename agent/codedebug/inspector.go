// inspector.go 从 Node debuggee 的 stderr 解析 SIGUSR1 唤醒的 inspector 端口。
//
// 职责：
//   - 识别 `Debugger listening on ws://host:port/...` 或 http 形式
//   - 提取 Node inspector 端口供 js-debug attach 使用
//
// 边界：
//   - 只做纯文本解析，不读进程、不连网络
//   - 多条匹配时取最后一条，代表最近一次 inspector 唤醒
package codedebug

import (
	"regexp"
	"strconv"
)

// inspectorLineRE 匹配 Node inspector 就绪行中的端口。
// Node SIGUSR1 后会向 stderr 打印 `Debugger listening on ws://host:port/...`。
var inspectorLineRE = regexp.MustCompile(`Debugger listening on (?:ws|http)://[^:/]+:(\d+)`)

// parseInspectorPort 在 stderr 行集合中找 inspector 端口；找不到返回 0。
// 多条匹配时取最后一条，对应最近一次 SIGUSR1 唤醒。
func parseInspectorPort(lines []string) int {
	port := 0
	for _, line := range lines {
		matches := inspectorLineRE.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		if p, err := strconv.Atoi(matches[1]); err == nil && p > 0 {
			port = p
		}
	}
	return port
}
