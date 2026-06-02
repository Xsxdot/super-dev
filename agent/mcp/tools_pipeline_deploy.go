// Package mcp 中的 tools_pipeline_deploy.go 实现项目级 pipeline 部署 MCP 工具。
//
// 职责：
//   - 将 MCP 参数转换为项目级 pipeline HTTP client 调用
//   - 支持部署、回滚、历史、制品和日志查询
//   - 支持按 project_name 解析 project_id
//
// 边界：
//   - 不直接执行 pipeline 引擎
//   - 不直接访问 store 或 artifact 文件
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type pipelineReferenceArgs struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	PipelineID  string `json:"pipeline_id"`
}

func (s *Server) deployProjectPipelineTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req PipelineDeployRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.PipelineID = strings.TrimSpace(req.PipelineID)
	req.EnvName = strings.TrimSpace(req.EnvName)
	if req.PipelineID == "" {
		return toolError("invalid_arguments", "pipeline_id is required", nil), nil
	}
	if req.EnvName == "" {
		return toolError("invalid_arguments", "env_name is required", nil), nil
	}
	projectID, result, ok := s.resolvePipelineProjectID(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return result, nil
	}
	req.ProjectID = projectID
	req.ProjectName = ""

	run, err := s.client.DeployProjectPipeline(ctx, projectID, req.PipelineID, req)
	operationKind := "pipeline.deploy"
	if strings.TrimSpace(req.ArtifactVersion) != "" {
		operationKind = "pipeline.rollback"
	}
	if err != nil {
		s.appendOperationToolObservation(ctx, req.DebugSessionID, operationKind, "project pipeline deploy failed", err)
		return clientToolError(err), nil
	}
	s.appendOperationToolObservation(ctx, req.DebugSessionID, operationKind, "project pipeline deploy completed", nil)
	return toolSuccess("project pipeline executed", map[string]any{"run": run}, nil, nil), nil
}

func (s *Server) listPipelineRunsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	ref, result, ok := s.decodePipelineReference(ctx, args)
	if !ok {
		return result, nil
	}
	runs, err := s.client.ListPipelineRuns(ctx, ref.ProjectID, ref.PipelineID)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("pipeline runs loaded", map[string]any{"runs": runs, "count": len(runs)}, nil, nil), nil
}

func (s *Server) listPipelineArtifactsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	ref, result, ok := s.decodePipelineReference(ctx, args)
	if !ok {
		return result, nil
	}
	artifacts, err := s.client.ListPipelineArtifacts(ctx, ref.ProjectID, ref.PipelineID)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("pipeline artifacts loaded", map[string]any{"artifacts": artifacts, "count": len(artifacts)}, nil, nil), nil
}

func (s *Server) readPipelineRunLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		pipelineReferenceArgs
		RunID    string `json:"run_id"`
		StepName string `json:"step_name"`
		HostID   string `json:"host_id"`
		Limit    int    `json:"limit"`
		Before   int64  `json:"before"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.RunID = strings.TrimSpace(req.RunID)
	if req.RunID == "" {
		return toolError("invalid_arguments", "run_id is required", nil), nil
	}
	ref, result, ok := s.resolvePipelineReference(ctx, req.pipelineReferenceArgs)
	if !ok {
		return result, nil
	}
	q := url.Values{}
	if strings.TrimSpace(req.StepName) != "" {
		q.Set("step_name", strings.TrimSpace(req.StepName))
	}
	if strings.TrimSpace(req.HostID) != "" {
		q.Set("host_id", strings.TrimSpace(req.HostID))
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Before > 0 {
		q.Set("before", strconv.FormatInt(req.Before, 10))
	}
	logs, err := s.client.ReadPipelineRunLogs(ctx, ref.ProjectID, ref.PipelineID, req.RunID, q)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("pipeline run logs loaded", map[string]any{"logs": logs, "count": len(logs)}, nil, nil), nil
}

func (s *Server) decodePipelineReference(ctx context.Context, args json.RawMessage) (pipelineReferenceArgs, CallToolResult, bool) {
	var req pipelineReferenceArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return pipelineReferenceArgs{}, toolError("invalid_arguments", err.Error(), nil), false
	}
	return s.resolvePipelineReference(ctx, req)
}

func (s *Server) resolvePipelineReference(ctx context.Context, req pipelineReferenceArgs) (pipelineReferenceArgs, CallToolResult, bool) {
	req.PipelineID = strings.TrimSpace(req.PipelineID)
	if req.PipelineID == "" {
		return pipelineReferenceArgs{}, toolError("invalid_arguments", "pipeline_id is required", nil), false
	}
	projectID, result, ok := s.resolvePipelineProjectID(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return pipelineReferenceArgs{}, result, false
	}
	req.ProjectID = projectID
	req.ProjectName = ""
	return req, CallToolResult{}, true
}

func (s *Server) resolvePipelineProjectID(ctx context.Context, projectID string, projectName string) (string, CallToolResult, bool) {
	q, result, err := s.operationQueryWithProject(ctx, projectID, projectName)
	if err != nil {
		return "", result, false
	}
	if result.IsError {
		return "", result, false
	}
	resolvedID := strings.TrimSpace(projectID)
	if resolvedID == "" {
		resolvedID = q.Get("project_id")
	}
	if resolvedID == "" {
		return "", toolError("invalid_arguments", "project_id or project_name is required", nil), false
	}
	return resolvedID, CallToolResult{}, true
}
