// Package api_test 验证流水线模板 HTTP API。
//
// 职责：
//   - 验证模板列表接口包含内置模板
//   - 验证返回模板 digest，供前端锁版本使用
//
// 边界：
//   - 不测试用户模板导入文件内容
//   - 不执行流水线模板
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPipelineTemplatesIncludesBuiltins(t *testing.T) {
	app := newTestAppInstance(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/templates", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		Items []struct {
			Source  string `json:"source"`
			ID      string `json:"id"`
			Version string `json:"version"`
			Digest  string `json:"digest"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.NotEmpty(t, body.Items)
	assert.NotEmpty(t, body.Items[0].Digest)
}

func TestGetPipelineTemplateReturnsBuiltinYAML(t *testing.T) {
	app := newTestAppInstance(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Source   string `json:"source"`
		ID       string `json:"id"`
		Version  string `json:"version"`
		Digest   string `json:"digest"`
		YAML     string `json:"yaml"`
		Template struct {
			Name string `json:"name"`
		} `json:"template"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "builtin", body.Source)
	assert.Equal(t, "go-binary-build", body.ID)
	assert.Equal(t, "1.0.0", body.Version)
	assert.NotEmpty(t, body.Digest)
	assert.Equal(t, "Go Binary Build", body.Template.Name)
	assert.Contains(t, body.YAML, "id: go-binary-build")
}

func TestPreviewPipelineTemplateFromYAML(t *testing.T) {
	app := newTestAppInstance(t)
	body := strings.NewReader(`{"yaml":"id: custom-build\nname: Custom Build\nversion: 1.0.0\nsteps:\n  - name: Build\n    type: local_command\n    with:\n      cmd: go build ./...\n"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/pipeline/templates/preview", body)
	rr := httptest.NewRecorder()

	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Template struct {
			ID string `json:"id"`
		} `json:"template"`
		Digest string   `json:"digest"`
		Errors []string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "custom-build", got.Template.ID)
	assert.NotEmpty(t, got.Digest)
	assert.Empty(t, got.Errors)
}

func TestPreviewPipelineTemplateRejectsMissingSource(t *testing.T) {
	app := newTestAppInstance(t)
	req := httptest.NewRequest(http.MethodPost, "/api/pipeline/templates/preview", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "path or yaml is required")
}
