// transfer_test.go 验证远端执行层的 multipart 文件落盘能力。
//
// 职责：
//   - 验证 multipart 请求能写入 target
//   - 验证 target 缺失会返回错误
//
// 边界：
//   - 不测试 HTTP handler 路由
//   - 不测试 pipeline 传输 runner
package remoteexec

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveMultipartTransferWritesTargetFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "deploy", "app.tar.gz")
	req := multipartTransferRequest(t, target, "file", "payload")

	written, err := SaveMultipartTransfer(req)

	require.NoError(t, err)
	assert.Equal(t, int64(len("payload")), written)
	assert.FileExists(t, target)
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}

func TestSaveMultipartTransferRequiresTarget(t *testing.T) {
	req := multipartTransferRequest(t, "", "file", "payload")

	_, err := SaveMultipartTransfer(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "target is required")
}

func multipartTransferRequest(t *testing.T, target, fieldName, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if target != "" {
		require.NoError(t, writer.WriteField("target", target))
	}
	part, err := writer.CreateFormFile(fieldName, "artifact.tar.gz")
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req, err := http.NewRequest(http.MethodPost, "/api/transfer", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
