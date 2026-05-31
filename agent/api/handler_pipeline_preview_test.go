// Package api_test 验证流水线预览 HTTP API。
//
// 职责：
//   - 验证 deployment pipeline 可构造成 Run skeleton
//   - 验证响应中包含阶段和 step_run 信息
//
// 边界：
//   - 不执行插件命令
//   - 不测试前端 DAG 展示
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewPipelineBuildsRunSkeleton(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
name: preview-demo
environments:
  - name: dev
    is_dev: true
services:
  - id: svc-1
    name: api
    deployments:
      - id: dep-1
        env: dev
        location: local
        pipeline:
          build:
            - name: Build
              type: local_command
              with:
                cmd: echo ok
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/deployments/dep-1/pipeline/preview", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		Run struct {
			DeploymentID string `json:"deployment_id"`
			StepRuns     []struct {
				StepName string `json:"step_name"`
				Type     string `json:"type"`
				Phase    string `json:"phase"`
			} `json:"step_runs"`
		} `json:"run"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "dep-1", body.Run.DeploymentID)
	require.Len(t, body.Run.StepRuns, 1)
	assert.Equal(t, "Build", body.Run.StepRuns[0].StepName)
	assert.Equal(t, "build", body.Run.StepRuns[0].Phase)
}
