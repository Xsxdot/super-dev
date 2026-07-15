// redactor_test.go 验证 secret 跨 chunk 时仍在任何持久 sink 前被替换。
//
// 职责：
//   - 锁定 streaming redactor 的跨写入边界和统计
//   - 拒绝注册空 secret 或在关闭后继续写入
//
// 边界：
//   - 不读取 credential API，也不把 secret 写入真实文件
package runtimevalidation

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactingWriterRedactsSecretAcrossChunks(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	redactor := NewRedactingWriter(&output)
	require.NoError(t, redactor.RegisterSecret("campaign-super-secret"))

	_, err := redactor.Write([]byte("before campaign-super-"))
	require.NoError(t, err)
	_, err = redactor.Write([]byte("secret after"))
	require.NoError(t, err)
	require.NoError(t, redactor.Close())

	require.Equal(t, "before [REDACTED] after", output.String())
	require.Equal(t, int64(1), redactor.RedactionCount())
}

func TestRedactingWriterRejectsUnsafeLifecycle(t *testing.T) {
	t.Parallel()

	redactor := NewRedactingWriter(&bytes.Buffer{})
	require.Error(t, redactor.RegisterSecret(""))
	require.NoError(t, redactor.Close())
	_, err := redactor.Write([]byte("late"))
	require.Error(t, err)
}
