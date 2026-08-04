// tls_hint_test.go —— mcp 客户端对「明文请求打到 TLS 监听器」失败形态的识别验证。
//
// 职责：
//   - 验证 HTTPAgentClient 与 LocalFileTokenSource 在明文请求被 TLS 监听器
//     拒绝时返回带 https:// 指引的可执行错误，而非解码谜语/裸 400
//
// 边界：
//   - 不覆盖 loopback 明文豁免本身（那是 agent/api 侧
//     server_tls_loopback_test.go 的职责）
package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPureTLSServer 起一个纯 TLS httptest server，返回它的明文形式 URL
// （http://host:port）——模拟「旧版 agent 纯 TLS 监听 + 客户端默认 http://」。
func newPureTLSServer(t *testing.T) string {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return strings.Replace(ts.URL, "https://", "http://", 1)
}

func TestClientReportsTLSRequiredOnPlaintextRejection(t *testing.T) {
	client := NewHTTPAgentClient(newPureTLSServer(t), nil)
	_, err := client.ListProjects(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires TLS")
	assert.Contains(t, err.Error(), "https://")
}

func TestLocalFileTokenSourceReportsTLSRequiredOnPlaintextRejection(t *testing.T) {
	source := NewLocalFileTokenSource(newPureTLSServer(t), nil)
	_, err := source.Token(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires TLS")
	assert.Contains(t, err.Error(), "https://")
}

func TestIsTLSRequiredResponse(t *testing.T) {
	marker := []byte("Client sent an HTTP request to an HTTPS server.\n")
	assert.True(t, isTLSRequiredResponse(http.StatusBadRequest, marker))
	// 同为 400 但不是 TLS 拒绝形态：不能误报，否则真实业务 400 被吞。
	assert.False(t, isTLSRequiredResponse(http.StatusBadRequest, []byte(`{"error":"invalid request body"}`)))
	assert.False(t, isTLSRequiredResponse(http.StatusUnauthorized, marker))
}
