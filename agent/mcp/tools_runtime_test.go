// Package mcp 验证运行态 MCP 工具。
//
// 职责：
//   - 验证项目、服务、运行态快照和启停工具
//   - 提供后续 MCP 工具测试复用的 fakeAgentClient
//
// 边界：
//   - 不访问真实 agent HTTP 服务
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

type fakeAgentClient struct {
	projects                []model.Project
	services                []model.Service
	rules                   []model.LogRule
	logs                    LogsResponse
	search                  LogSearchResponse
	contextResp             LogContextResponse
	contextQuery            url.Values
	debugSessions           []DebugSession
	debugSessionDetail      DebugSessionDetailResponse
	createdDebugSession     DebugSessionCreateRequest
	appendedSessionID       string
	appendedEventRequest    DebugSessionAppendEventRequest
	closedSessionID         string
	stopCalled              bool
	startedDeploymentID     string
	stoppedDeploymentID     string
	restartedDeploymentID   string
	templatePreview         PipelineTemplatePreview
	importedTemplate        PipelineTemplateSummary
	importedTemplatePath    string
	operationPlan           OperationPlan
	operationApprovals      []OperationApproval
	operationApprovalDetail OperationApprovalDetail
	operationAudit          OperationAuditList
	lastApprovalToken       string
}

func (f *fakeAgentClient) ListProjects(context.Context) ([]model.Project, error) {
	return f.projects, nil
}

func (f *fakeAgentClient) ListServices(context.Context) ([]model.Service, error) {
	return f.services, nil
}

func (f *fakeAgentClient) ProjectRules(context.Context, string) ([]model.LogRule, error) {
	return f.rules, nil
}

func (f *fakeAgentClient) FetchDeploymentLogs(context.Context, string, url.Values) (LogsResponse, error) {
	return f.logs, nil
}

func (f *fakeAgentClient) SearchLogs(context.Context, url.Values) (LogSearchResponse, error) {
	return f.search, nil
}

func (f *fakeAgentClient) FetchLogContext(_ context.Context, q url.Values) (LogContextResponse, error) {
	f.contextQuery = q
	return f.contextResp, nil
}

func (f *fakeAgentClient) CreateDebugSession(_ context.Context, req DebugSessionCreateRequest) (DebugSessionCreateResponse, error) {
	f.createdDebugSession = req
	session := DebugSession{
		ID:           "dbg_1",
		ProjectID:    req.ProjectID,
		ProjectName:  req.ProjectName,
		EnvName:      req.EnvName,
		ServiceID:    req.ServiceID,
		ServiceName:  req.ServiceName,
		DeploymentID: req.DeploymentID,
		Title:        req.Title,
		Question:     req.Question,
		Status:       "open",
	}
	return DebugSessionCreateResponse{
		Session: session,
		Event:   DebugSessionEvent{ID: "ev_1", SessionID: session.ID, Type: "status_change"},
	}, nil
}

func (f *fakeAgentClient) ListDebugSessions(context.Context, url.Values) ([]DebugSession, error) {
	return f.debugSessions, nil
}

func (f *fakeAgentClient) GetDebugSession(context.Context, string, int) (DebugSessionDetailResponse, error) {
	return f.debugSessionDetail, nil
}

func (f *fakeAgentClient) AppendDebugSessionEvent(_ context.Context, id string, req DebugSessionAppendEventRequest) (DebugSessionEvent, error) {
	f.appendedSessionID = id
	f.appendedEventRequest = req
	return DebugSessionEvent{ID: "ev_2", SessionID: id, Type: req.Type, Actor: req.Actor, Summary: req.Summary, Data: req.Data}, nil
}

func (f *fakeAgentClient) CloseDebugSession(_ context.Context, id string, summary string) (DebugSession, error) {
	f.closedSessionID = id
	return DebugSession{ID: id, Status: "closed", Title: summary}, nil
}

func (f *fakeAgentClient) PreviewOperation(context.Context, OperationRequest) (OperationPlan, error) {
	return f.operationPlan, nil
}

func (f *fakeAgentClient) ListOperationApprovals(context.Context, url.Values) ([]OperationApproval, error) {
	return f.operationApprovals, nil
}

func (f *fakeAgentClient) GetOperationApproval(context.Context, string) (OperationApprovalDetail, error) {
	return f.operationApprovalDetail, nil
}

func (f *fakeAgentClient) ListOperationAudit(context.Context, url.Values) (OperationAuditList, error) {
	return f.operationAudit, nil
}

func (f *fakeAgentClient) StartDeployment(_ context.Context, id string, approvalToken string) error {
	f.startedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) StopDeployment(_ context.Context, id string, approvalToken string) error {
	f.stopCalled = true
	f.stoppedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) RestartDeployment(_ context.Context, id string, approvalToken string) error {
	f.restartedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) PreviewPipelineTemplate(context.Context, string, string) (PipelineTemplatePreview, error) {
	return f.templatePreview, nil
}

func (f *fakeAgentClient) ImportPipelineTemplate(_ context.Context, path string, approvalToken string) (PipelineTemplateSummary, error) {
	f.importedTemplatePath = path
	f.lastApprovalToken = approvalToken
	return f.importedTemplate, nil
}

func (s *Server) callToolForTest(ctx context.Context, name string, args string) (CallToolResult, error) {
	return s.tools[name].Handler(ctx, json.RawMessage(args))
}

func sampleService(name string, status model.ServiceStatus, depID string) model.Service {
	return model.Service{
		ID:     "svc-" + name,
		Name:   name,
		Status: status,
		Deployments: []model.Deployment{{
			ID:      depID,
			EnvName: "dev",
			Status:  status,
		}},
	}
}

func TestRuntimeSnapshotSummarizesProjectsAndServices(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		services: []model.Service{sampleService("api", model.StatusRunning, "dep-api-dev")},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_runtime_snapshot", `{}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	assert.Contains(t, body.Summary, "1 project")
}

func TestStopServiceDoesNotRunWhenTargetIsAmbiguous(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "stop_service", `{"project_name":"demo","service_name":"api"}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.False(t, client.stopCalled)
}

func TestRestartServiceCallsResolvedDeployment(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "restart_service", `{"deployment_id":"dep-api-prod"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "dep-api-prod", client.restartedDeploymentID)
}
