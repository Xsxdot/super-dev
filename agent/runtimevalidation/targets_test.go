// targets_test.go 验证 Desktop 与 validation builder 共享的五 target 合同。
//
// 职责：锁定精确集合、顺序、重复和非法 architecture 拒绝。
// 边界：不证明任何 target 已完成真机执行。
package runtimevalidation

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTargetsFileMatchesStrictFiveTargetContract(t *testing.T) {
	t.Parallel()

	targets, err := LoadTargetsFile(filepath.Join("..", "..", "validation", "runtime", "targets.txt"))
	require.NoError(t, err)
	require.Equal(t, SupportedTargets(), targets)
	require.Equal(t, "x86_64-pc-windows-msvc", targets[4].RustTriple())
}

func TestParseTargetsRejectsDuplicateOrUnsupportedTarget(t *testing.T) {
	t.Parallel()

	_, err := ParseTargets("darwin arm64\ndarwin arm64\n")
	require.ErrorContains(t, err, "duplicate")
	_, err = ParseTargets("freebsd amd64\n")
	require.ErrorContains(t, err, "unsupported")
}
