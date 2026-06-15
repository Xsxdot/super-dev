// inspector_test.go 验证 Node inspector stderr 端口解析。
//
// 职责：
//   - 覆盖 SIGUSR1 唤醒后 Node 打印的 inspector URL 格式
//   - 确认多次唤醒时采用最新端口
//
// 边界：
//   - 只测试纯文本解析，不启动真实 Node 进程
package codedebug

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseInspectorPort(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  int
	}{
		{
			"standard ws line",
			[]string{"node tick 1", "Debugger listening on ws://127.0.0.1:9229/abc-uuid", "For help, see: https://nodejs.org"},
			9229,
		},
		{
			"non-default port",
			[]string{"Debugger listening on ws://127.0.0.1:39111/x"},
			39111,
		},
		{
			"http inspector form",
			[]string{"Debugger listening on http://127.0.0.1:9230/json"},
			9230,
		},
		{"no inspector line", []string{"node tick 1", "node tick 2"}, 0},
		{"empty", nil, 0},
		{
			"last wins when multiple",
			[]string{"Debugger listening on ws://127.0.0.1:9229/a", "Debugger listening on ws://127.0.0.1:9300/b"},
			9300,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseInspectorPort(tc.lines))
		})
	}
}
