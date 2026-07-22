// main_test.go 验证 auth sidecar 的匿名 stdin 凭据合同。
//
// 职责：锁定单行、非空、有限长度输入，防止多凭据或尾随内容混入。
// 边界：不监听端口，也不把测试凭据交给日志系统。
package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadCredentialAcceptsExactlyOneLine(t *testing.T) {
	t.Parallel()

	value, err := readCredential(strings.NewReader("one-time-value\n"))
	require.NoError(t, err)
	require.Equal(t, "one-time-value", value)

	_, err = readCredential(strings.NewReader("first\nsecond\n"))
	require.ErrorContains(t, err, "multiple")
}
