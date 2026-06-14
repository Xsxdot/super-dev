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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkGoMain(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
}

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

// TestSuggestServiceRuntime 验证 HTTP suggest 会委托 provider 扫描项目入口。
func TestSuggestServiceRuntime(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	root := t.TempDir()
	mkGoMain(t, filepath.Join(root, "cmd", "server"))

	resp := postJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/go/suggest", map[string]any{
		"project_root": root,
		"cwd":          ".",
	}, http.StatusOK)
	suggestions, ok := resp["suggestions"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, suggestions)
	first, ok := suggestions[0].(map[string]any)
	require.True(t, ok)
	config, ok := first["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "./cmd/server", config["program"])
}

// TestValidateServiceRuntimeRejectsBadProgram 验证 validate 暴露 provider diagnostics。
func TestValidateServiceRuntimeRejectsBadProgram(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp := postJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/go/validate", map[string]any{
		"project_root": "/repo",
		"config":       map[string]any{"program": 42},
	}, http.StatusOK)
	assert.Equal(t, false, resp["valid"])
	diagnostics, ok := resp["diagnostics"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, diagnostics)
}

// TestPreviewServiceExecution 验证 preview 返回可读的执行计划预览。
func TestPreviewServiceExecution(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp := postJSONForTest[map[string]any](t, srv.URL+"/api/language-runtime/go/preview", map[string]any{
		"project_root": "/repo",
		"cwd":          "./server",
		"env":          map[string]string{"ENABLE": "true"},
		"config":       map[string]any{"program": "./cmd/server"},
		"intent":       "start_dev",
		"artifact_dir": "/data/run-bin/x",
	}, http.StatusOK)
	assert.NotEmpty(t, resp["preview"])
}
