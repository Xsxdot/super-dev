// escape_hatch_test.go 验证两层启动模型的逃生口解析。
//
// 职责：锁定 runtime_executable/runtime_args 到 CommandStep 的转换行为。
// 边界：不验证具体语言 provider 如何消费逃生口。
package langruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/langruntime"
)

func TestEscapeHatchCommandWhenExecutableSet(t *testing.T) {
	cfg := map[string]any{
		"runtime_executable": "pnpm",
		"runtime_args":       []any{"worker"},
	}
	step, ok := langruntime.EscapeHatchCommand(cfg)
	assert.True(t, ok)
	assert.Equal(t, "pnpm", step.Executable)
	assert.Equal(t, []string{"worker"}, step.Args)
}

func TestEscapeHatchCommandWhenUnset(t *testing.T) {
	_, ok := langruntime.EscapeHatchCommand(map[string]any{"program": "./cmd/x"})
	assert.False(t, ok)
}

func TestEscapeHatchCommandBlankExecutableUnset(t *testing.T) {
	_, ok := langruntime.EscapeHatchCommand(map[string]any{"runtime_executable": "  "})
	assert.False(t, ok)
}
