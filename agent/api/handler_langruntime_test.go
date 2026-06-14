// handler_langruntime_test.go 验证 Language Runtime Provider HTTP 契约。
//
// 职责：
//   - 验证 provider 列表接口返回已注册语言
//   - 验证 schema 描述接口返回 Go provider 字段契约
//   - 验证未知语言返回 404
//
// 边界：
//   - 不启动真实进程
//   - 不持久化或修改服务配置
package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListLanguageRuntimeProviders 验证 HTTP 能列出内置语言运行 provider。
func TestListLanguageRuntimeProviders(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp := getJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/providers", http.StatusOK)
	languages, ok := resp["languages"].([]any)
	require.True(t, ok)
	assert.Contains(t, languages, "go")
}

// TestDescribeLanguageRuntimeSchema 验证 Go provider schema 对 AI/前端可见。
func TestDescribeLanguageRuntimeSchema(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp := getJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/go/schema", http.StatusOK)
	assert.Equal(t, "go", resp["language"])
	fields, ok := resp["fields"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, fields)
	first, ok := fields[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "program", first["key"])
}

// TestDescribeUnknownLanguageReturns404 验证未知语言不会被静默降级到默认 provider。
func TestDescribeUnknownLanguageReturns404(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	getJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/cobol/schema", http.StatusNotFound)
}
