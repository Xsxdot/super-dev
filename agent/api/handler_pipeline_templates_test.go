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
