// normalize_test.go 验证日志折叠签名归一化规则。
//
// 职责：
//   - 覆盖时间戳剥离、可变字段掩码和首尾空白处理
//   - 防止前端折叠逻辑下沉到 agent 后出现签名行为回退
//
// 边界：
//   - 仅验证纯函数行为，不依赖日志存储、缓冲或实时推送链路
package logparse

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"strip clock prefix", "12:30:45 connection failed", "connection failed"},
		{"strip clock with millis", "12:30:45.123 connection failed", "connection failed"},
		{"strip iso prefix", "2026-06-08T12:30:45 boot ok", "boot ok"},
		{"strip iso space prefix", "2026-06-08 12:30:45 boot ok", "boot ok"},
		{"strip weekday prefix", "Wed May 20 17:20:51 CST 2026 started", "started"},
		{"mask number assign", "retry count=123 done", "retry count=* done"},
		{"mask ip port", "dial 192.168.1.10:8080 refused", "dial *:* refused"},
		{"trim", "  spaced  ", "spaced"},
		{"no change", "plain message", "plain message"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
