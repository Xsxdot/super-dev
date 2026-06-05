// Package api_test 验证流水线预览 HTTP API。
//
// 职责：
//   - 验证项目级 pipeline 可构造成 Run skeleton
//   - 验证响应中包含阶段和 step_run 信息
//
// 边界：
//   - 不执行插件命令
//   - 不测试前端 DAG 展示
package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewProjectPipelineBuildsRunSkeleton(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
id: preview-project-pipeline
name: preview-project-pipeline
variables:
  app_name: api
environments:
  - name: dev
    is_dev: true
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-dev
        env: dev
        location: remote
        hosts: [h1]
        runtime:
          type: systemd
          service_name: api-dev
        logs:
          type: journalctl
          target: api-dev.service
pipelines:
  - id: deploy-dev
    name: Deploy Dev
    services: [api]
    variables:
      target_api: api_targets
    roles:
      api_targets:
        from_service: api
    pipeline:
      deploy:
        - name: Restart
          type: remote_command
          roles: ["${target_api}"]
          with:
            cmd: "systemctl restart api-dev"
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/preview-project-pipeline/pipelines/deploy-dev/preview", strings.NewReader(`{"env_name":"dev"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"deployment_id":"project:preview-project-pipeline:pipeline:deploy-dev:env:dev"`)
	assert.Contains(t, rr.Body.String(), `"host_id":"h1"`)
}

func TestPreviewProjectPipelineRejectsMissingPluginParameter(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
id: preview-missing-param
name: preview-missing-param
environments:
  - name: dev
    is_dev: true
pipelines:
  - id: deploy-dev
    name: Deploy Dev
    pipeline:
      build:
        - name: Build
          type: local_command
          with: {}
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/preview-missing-param/pipelines/deploy-dev/preview", strings.NewReader(`{"env_name":"dev"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "pipeline validation failed")
	assert.Contains(t, rr.Body.String(), "local_command requires with.cmd")
}

func TestPreviewProjectPipelineRejectsUnknownPlugin(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
id: preview-unknown-plugin
name: preview-unknown-plugin
environments:
  - name: dev
    is_dev: true
pipelines:
  - id: deploy-dev
    name: Deploy Dev
    pipeline:
      build:
        - name: Mystery
          type: missing_plugin
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/preview-unknown-plugin/pipelines/deploy-dev/preview", strings.NewReader(`{"env_name":"dev"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing_plugin")
}

func TestPreviewProjectPipelineRejectsRemoteCommandWithoutTargets(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
id: preview-remote-no-targets
name: preview-remote-no-targets
environments:
  - name: dev
    is_dev: true
pipelines:
  - id: deploy-dev
    name: Deploy Dev
    pipeline:
      deploy:
        - name: Restart
          type: remote_command
          with:
            cmd: "systemctl restart api"
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/preview-remote-no-targets/pipelines/deploy-dev/preview", strings.NewReader(`{"env_name":"dev"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "remote_command requires targets")
}
