// handler_transfer_test.go 验证远端 agent 文件传输 HTTP 接口。
//
// 职责：
//   - 验证 /api/transfer 接收 multipart 文件并写入目标路径
//   - 验证缺失 target 时返回 400
//
// 边界：
//   - 不测试 pipeline AgentRunner
//   - 不建立真实隧道
package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransferHandlerWritesUploadedFile(t *testing.T) {
	app := newTestAppForPackage(t)
	target := filepath.Join(t.TempDir(), "releases", "app.tar.gz")
	body, contentType := transferBody(t, target, "payload")

	req := httptest.NewRequest(http.MethodPost, "/api/transfer", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}

func TestTransferHandlerRejectsMissingTarget(t *testing.T) {
	app := newTestAppForPackage(t)
	body, contentType := transferBody(t, "", "payload")

	req := httptest.NewRequest(http.MethodPost, "/api/transfer", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func transferBody(t *testing.T, target, content string) (*bytes.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if target != "" {
		require.NoError(t, writer.WriteField("target", target))
	}
	part, err := writer.CreateFormFile("file", "artifact.tar.gz")
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}
