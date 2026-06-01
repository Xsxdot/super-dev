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

func TestPreviewDeploymentPipelineRendersRunTempDir(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
name: preview-temp-dir-demo
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
          variables:
            backend_output: ${output}/backend
            backend_artifact: ${artifacts}/backend-${version}.tar.gz
          build:
            - name: Build Go
              type: include
              with:
                template: builtin://go-binary-build
                version: 1.0.0
                vars:
                  work_dir: ${workspace}
                  package: ./...
                  output: ${backend_output}
                  artifact: ${backend_artifact}
                  files:
                    - from: ${backend_output}
                      to: bin/api
                  app_name: api
`), 0o644))
	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(addRR, addReq)
	require.Equal(t, http.StatusOK, addRR.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/deployments/dep-1/pipeline/preview", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "/tmp/super-debug-pipeline-preview/output/backend")
	assert.Contains(t, rr.Body.String(), "/tmp/super-debug-pipeline-preview/artifacts/backend-")
}

func TestPreviewDeploymentPipelinePreservesArchivePackageFiles(t *testing.T) {
	app := newTestAppInstance(t)
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
name: preview-archive-files-demo
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
          variables:
            backend_output: ${output}/api
            backend_artifact: ${artifacts}/api-${version}.tar.gz
          build:
            - name: Package
              type: include
              with:
                template: builtin://archive-package
                version: 1.0.0
                vars:
                  artifact: ${backend_artifact}
                  files:
                    - from: ${backend_output}
                      to: bin/api
                    - from: ${workspace}/config/app.env
                      to: config/app.env
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
		Plan struct {
			Phases map[string][]struct {
				Name string                 `json:"name"`
				Type string                 `json:"type"`
				With map[string]interface{} `json:"with"`
			} `json:"Phases"`
		} `json:"plan"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Len(t, body.Plan.Phases["build"], 1)
	step := body.Plan.Phases["build"][0]
	assert.Equal(t, "Package.Package", step.Name)
	assert.Equal(t, "archive_package", step.Type)
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/artifacts/api-.tar.gz", step.With["artifact"])
	require.IsType(t, []interface{}{}, step.With["files"])
	files := step.With["files"].([]interface{})
	require.Len(t, files, 2)
	require.IsType(t, map[string]interface{}{}, files[0])
	assert.Equal(t, map[string]interface{}{"from": "/tmp/super-debug-pipeline-preview/output/api", "to": "bin/api"}, files[0])
	assert.Equal(t, map[string]interface{}{"from": projectDir + "/config/app.env", "to": "config/app.env"}, files[1])
}

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
