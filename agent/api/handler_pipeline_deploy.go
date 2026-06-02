// Package api 中的 handler_pipeline_deploy.go 暴露项目级 pipeline 执行与历史接口。
//
// 职责：
//   - POST /deploy 触发正常部署或回滚
//   - GET /runs 和 /artifacts 返回历史
//   - GET /runs/{runId}/logs 支持 step/host 过滤
//
// 边界：
//   - 不展开模板或执行引擎，业务编排交给 pipeline_deploy_service
//   - 不做展示层过滤
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

type projectPipelineRunsResponse struct {
	Items []model.Run `json:"items"`
}

type projectPipelineArtifactsResponse struct {
	Items []model.ArtifactRef `json:"items"`
}

type projectPipelineRunLogsResponse struct {
	Items []model.RunLogLine `json:"items"`
}

// deployProjectPipeline 处理 POST /api/projects/{id}/pipelines/{pipelineId}/deploy。
func (a *App) deployProjectPipeline(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	var req projectPipelineDeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	run, err := a.executeProjectPipeline(r.Context(), projectID, pipelineID, req)
	if err != nil {
		writeProjectPipelineDeployError(w, err)
		return
	}
	jsonOK(w, run)
}

// listProjectPipelineRuns 处理 GET /api/projects/{id}/pipelines/{pipelineId}/runs。
func (a *App) listProjectPipelineRuns(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	runs, err := a.store.ListRuns(projectID, pipelineID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list pipeline runs: "+err.Error())
		return
	}
	if runs == nil {
		runs = []model.Run{}
	}
	jsonOK(w, projectPipelineRunsResponse{Items: runs})
}

// getProjectPipelineRun 处理 GET /api/projects/{id}/pipelines/{pipelineId}/runs/{runId}。
func (a *App) getProjectPipelineRun(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	runID := r.PathValue("runId")
	run, ok, err := a.store.GetRun(runID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get pipeline run: "+err.Error())
		return
	}
	if !ok || run.ProjectID != projectID || run.PipelineID != pipelineID {
		jsonError(w, http.StatusNotFound, "pipeline run not found")
		return
	}
	jsonOK(w, run)
}

// listProjectArtifactsForPipeline 处理 GET /api/projects/{id}/pipelines/{pipelineId}/artifacts。
func (a *App) listProjectArtifactsForPipeline(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	refs, err := a.store.ListArtifacts(r.Context(), projectID, pipelineID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list pipeline artifacts: "+err.Error())
		return
	}
	if refs == nil {
		refs = []model.ArtifactRef{}
	}
	jsonOK(w, projectPipelineArtifactsResponse{Items: refs})
}

// readProjectPipelineRunLogs 处理 GET /api/projects/{id}/pipelines/{pipelineId}/runs/{runId}/logs。
func (a *App) readProjectPipelineRunLogs(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	runID := r.PathValue("runId")
	run, ok, err := a.store.GetRun(runID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get pipeline run: "+err.Error())
		return
	}
	if !ok || run.ProjectID != projectID || run.PipelineID != pipelineID {
		jsonError(w, http.StatusNotFound, "pipeline run not found")
		return
	}

	query := store.RunLogQuery{
		RunID:    runID,
		StepName: r.URL.Query().Get("step_name"),
		HostID:   r.URL.Query().Get("host_id"),
		Limit:    parseBoundedInt(r.URL.Query().Get("limit"), 1000, maxLimit),
	}
	if rawBefore := r.URL.Query().Get("before"); rawBefore != "" {
		before, err := strconv.ParseInt(rawBefore, 10, 64)
		if err != nil || before <= 0 {
			jsonError(w, http.StatusBadRequest, "before is invalid")
			return
		}
		query.BeforeID = before
	}

	lines, err := a.store.ReadRunLogs(query)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to read pipeline run logs: "+err.Error())
		return
	}
	if lines == nil {
		lines = []model.RunLogLine{}
	}
	jsonOK(w, projectPipelineRunLogsResponse{Items: lines})
}

func writeProjectPipelineDeployError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrArtifactNotFound) || err.Error() == "project not found" {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonError(w, http.StatusBadRequest, err.Error())
}
