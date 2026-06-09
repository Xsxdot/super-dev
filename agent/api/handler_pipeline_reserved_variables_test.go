// Package api_test 验证流水线保留变量说明接口。
//
// 职责：
//   - 覆盖保留变量元数据的 HTTP 只读契约
//   - 确认接口返回变量名和用途说明
//
// 边界：
//   - 不执行流水线
//   - 不校验前端展示样式
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListPipelineReservedVariables 验证保留变量接口返回稳定变量名和说明。
func TestListPipelineReservedVariables(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/pipeline/reserved-variables")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var items []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))

	assert.Contains(t, items, struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{
		Name:        "workspace",
		Description: "当前项目根目录，用于在模板中引用仓库工作目录。",
	})
	assert.Contains(t, items, struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{
		Name:        "artifacts",
		Description: "本次运行的产物目录，构建模板应把归档或镜像元数据写到这里。",
	})
}
