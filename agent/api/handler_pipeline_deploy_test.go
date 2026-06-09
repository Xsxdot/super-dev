// Package api_test 验证项目级 pipeline 执行 HTTP API。
//
// 职责：
//   - 验证项目级 pipeline 可通过 HTTP 触发部署
//   - 验证部署历史、单次 Run、制品和日志可通过 HTTP 查询
//
// 边界：
//   - 不测试 pipeline 引擎内部调度细节
//   - 不测试前端对历史和日志的展示
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDeployProjectPipelineReturnsRunningThenPersistsTerminalRun(t *testing.T) {
	app := newPipelineDeployTestApp(t, false)
	projectID := addProjectWithArtifactPipeline(t, app)

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/deploy",
		strings.NewReader(`{"env_name":"prod","variables":{"version":"v1"}}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var started model.Run
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&started))
	assert.Equal(t, model.RunStatusRunning, started.Status)
	assert.NotEmpty(t, started.ID)
	assert.Equal(t, "v1", started.ArtifactVersion)

	require.Eventually(t, func() bool {
		runReq := httptest.NewRequest(http.MethodGet,
			"/api/projects/"+projectID+"/pipelines/deploy-prod/runs/"+started.ID, nil)
		runRR := httptest.NewRecorder()
		app.Handler().ServeHTTP(runRR, runReq)
		if runRR.Code != http.StatusOK {
			return false
		}
		var current model.Run
		if err := json.NewDecoder(runRR.Body).Decode(&current); err != nil {
			return false
		}
		return current.Status == model.StatusSuccess
	}, 3*time.Second, 20*time.Millisecond)
}

func TestDeployProjectPipelineRequiresApprovalWhenSwitchOn(t *testing.T) {
	app := newPipelineDeployTestApp(t, true)
	projectID := addProjectWithArtifactPipeline(t, app)

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/deploy",
		strings.NewReader(`{"env_name":"prod","variables":{"version":"v1"}}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "approval_required", body["code"])
}

func TestDeployProjectPipelineSkipsApprovalWhenSwitchOff(t *testing.T) {
	app := newPipelineDeployTestApp(t, false)
	projectID := addProjectWithArtifactPipeline(t, app)

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/deploy",
		strings.NewReader(`{"env_name":"prod","variables":{"version":"v2"}}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	require.NotEqual(t, http.StatusForbidden, rr.Code, rr.Body.String())
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestListProjectPipelineRunsArtifactsAndLogs(t *testing.T) {
	app := newPipelineDeployTestApp(t, false)
	projectID := addProjectWithCompletedRun(t, app)

	runsReq := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/runs", nil)
	runsRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(runsRR, runsReq)

	require.Equal(t, http.StatusOK, runsRR.Code)
	var runsResp struct {
		Items []model.Run `json:"items"`
	}
	require.NoError(t, json.NewDecoder(runsRR.Body).Decode(&runsResp))
	require.Len(t, runsResp.Items, 1)
	assert.Equal(t, "v1", runsResp.Items[0].ArtifactVersion)

	runID := runsResp.Items[0].ID
	runReq := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/runs/"+runID, nil)
	runRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(runRR, runReq)
	require.Equal(t, http.StatusOK, runRR.Code)
	assert.Contains(t, runRR.Body.String(), `"id":"`+runID+`"`)

	artifactsReq := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/artifacts", nil)
	artifactsRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(artifactsRR, artifactsReq)
	require.Equal(t, http.StatusOK, artifactsRR.Code)
	assert.Contains(t, artifactsRR.Body.String(), `"version":"v1"`)

	logsReq := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/runs/"+runID+"/logs?limit=10", nil)
	logsRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(logsRR, logsReq)
	require.Equal(t, http.StatusOK, logsRR.Code)
	assert.Contains(t, logsRR.Body.String(), `"line":"built"`)
}

func newPipelineDeployTestApp(t *testing.T, pipelineRunApproval bool) *api.App {
	t.Helper()
	dataDir := t.TempDir()
	settings := config.DefaultAgentSettings()
	settings.Approval.PipelineRun = pipelineRunApproval
	require.NoError(t, config.NewSettingsStore(dataDir).Save(settings))
	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
}

func addProjectWithArtifactPipeline(t *testing.T, app *api.App) string {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
id: deploy-api-project
name: deploy-api-project
environments:
  - name: prod
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-prod
        env: prod
        location: local
pipelines:
  - id: deploy-prod
    name: Deploy Prod
    services: [api]
    artifact_kind: file
    variables:
      artifact: "${artifacts}/api-${version}.tar.gz"
    pipeline:
      build:
        - name: Build Artifact
          type: local_command
          with:
            cmd: mkdir -p ${artifacts} && printf built > ${artifact} && printf built
      deploy:
        - name: Deploy Local
          type: local_command
          with:
            cmd: test -f ${artifact}
`), 0o644))

	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	return "deploy-api-project"
}

func addProjectWithCompletedRun(t *testing.T, app *api.App) string {
	t.Helper()
	projectID := addProjectWithArtifactPipeline(t, app)
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+projectID+"/pipelines/deploy-prod/deploy",
		strings.NewReader(`{"env_name":"prod","variables":{"version":"v1"}}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var started model.Run
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&started))
	require.Eventually(t, func() bool {
		runReq := httptest.NewRequest(http.MethodGet,
			"/api/projects/"+projectID+"/pipelines/deploy-prod/runs/"+started.ID, nil)
		runRR := httptest.NewRecorder()
		app.Handler().ServeHTTP(runRR, runReq)
		if runRR.Code != http.StatusOK {
			return false
		}
		var current model.Run
		if err := json.NewDecoder(runRR.Body).Decode(&current); err != nil {
			return false
		}
		return current.Status == model.StatusSuccess
	}, 3*time.Second, 20*time.Millisecond)
	return projectID
}
