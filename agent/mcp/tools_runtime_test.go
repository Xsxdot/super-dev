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
	"github.com/xsxdot/super-dev/agent/model"
)

type fakeAgentClient struct {
	projects                    []model.Project
	hosts                       []HostReference
	services                    []model.Service
	debugCredentials            []model.MergedDebugCredential
	lastDebugCredentialsQuery   url.Values
	rules                       []model.LogRule
	logs                        LogsResponse
	search                      LogSearchResponse
	contextResp                 LogContextResponse
	contextQuery                url.Values
	debugSessions               []DebugSession
	debugSessionDetail          DebugSessionDetailResponse
	createdDebugSession         DebugSessionCreateRequest
	appendedSessionID           string
	appendedEventRequest        DebugSessionAppendEventRequest
	closedSessionID             string
	stopCalled                  bool
	stopCallCount               int
	stopErrors                  []error
	startedDeploymentID         string
	stoppedDeploymentID         string
	restartedDeploymentID       string
	restartCallCount            int
	restartErrors               []error
	templatePreview             PipelineTemplatePreview
	importedTemplate            PipelineTemplateSummary
	importTemplateErrs          []error
	importTemplateCallCount     int
	importedTemplatePath        string
	operationPlan               OperationPlan
	operationApprovals          []OperationApproval
	operationApprovalDetail     OperationApprovalDetail
	operationApprovalDetails    []OperationApprovalDetail
	getApprovalCallCount        int
	operationAudit              OperationAuditList
	configProject               model.Project
	configPreview               ConfigChangePreview
	configApplyErr              error
	configApplyErrs             []error
	configApplyCallCount        int
	lastConfigChange            ConfigChangeRequest
	lastApprovalToken           string
	pipelineRun                 model.Run
	pipelineDeployErrs          []error
	pipelineDeployCallCount     int
	pipelineRuns                []model.Run
	pipelineArtifacts           []model.ArtifactRef
	pipelineLogs                []model.RunLogLine
	pipelinePreview             ProjectPipelinePreview
	pipelinePreviewErr          error
	lastPipelineDeploy          PipelineDeployRequest
	lastPipelinePreviewRequest  ProjectPipelinePreviewRequest
	lastPipelineLogQuery        url.Values
	debugBrowsers               []DebugBrowser
	browserTargets              []BrowserTarget
	browserSession              BrowserSession
	browserSnapshot             BrowserSnapshot
	browserAction               BrowserActionResult
	browserScreenshot           BrowserScreenshot
	browserScreenshotErr        error
	browserNavigation           BrowserNavigationResult
	browserWait                 BrowserWaitResult
	browserConsoleLogs          BrowserConsoleLogsResult
	browserNetworkRequests      BrowserNetworkRequestsResult
	browserEvaluate             BrowserEvaluateResult
	browserViewport             BrowserViewportResult
	codeDebugTargets            []CodeDebugTarget
	codeDebugActionResult       map[string]any
	codeDebugEvaluateResult     map[string]any
	codeDebugCaptureResult      map[string]any
	codeDebugInspectResult      map[string]any
	languageRuntimeProviders    []string
	languageRuntimeSchema       map[string]any
	languageRuntimeResponse     map[string]any
	lastBrowserOpen             OpenBrowserSessionRequest
	lastBrowserSnapshot         BrowserSnapshotRequest
	lastBrowserClick            BrowserClickRequest
	lastBrowserType             BrowserTypeRequest
	lastBrowserScreenshot       BrowserScreenshotRequest
	lastBrowserNavigate         BrowserNavigateRequest
	lastBrowserReload           BrowserReloadRequest
	lastBrowserWait             BrowserWaitForSelectorRequest
	lastBrowserPress            BrowserPressKeyRequest
	lastBrowserSelect           BrowserSelectOptionRequest
	lastBrowserConsoleLogs      BrowserConsoleLogsRequest
	lastBrowserNetworkRequests  BrowserNetworkRequestsRequest
	lastBrowserEvaluate         BrowserEvaluateRequest
	lastBrowserSetViewport      BrowserSetViewportRequest
	closedBrowserSession        string
	lastBreakpoint              DebugBreakpointRequest
	lastDebugActionDeploymentID string
	lastDebugAction             string
	lastDebugActionBody         map[string]any
	lastEvaluate                DebugEvaluateRequest
	lastCaptureAt               DebugCaptureAtRequest
	lastInspect                 DebugInspectRequest
	lastLanguageRuntimeMethod   string
	lastLanguageRuntimeLanguage string
	lastLanguageRuntimeBody     map[string]any
}

func (f *fakeAgentClient) ListProjects(context.Context) ([]model.Project, error) {
	return f.projects, nil
}

func (f *fakeAgentClient) ListHosts(context.Context) ([]HostReference, error) {
	return f.hosts, nil
}

func (f *fakeAgentClient) ListServices(context.Context) ([]model.Service, error) {
	return f.services, nil
}

func (f *fakeAgentClient) GetDebugCredentials(_ context.Context, q url.Values) ([]model.MergedDebugCredential, error) {
	f.lastDebugCredentialsQuery = q
	return f.debugCredentials, nil
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
	f.getApprovalCallCount++
	if len(f.operationApprovalDetails) > 0 {
		index := f.getApprovalCallCount - 1
		if index >= len(f.operationApprovalDetails) {
			index = len(f.operationApprovalDetails) - 1
		}
		return f.operationApprovalDetails[index], nil
	}
	return f.operationApprovalDetail, nil
}

func (f *fakeAgentClient) ListOperationAudit(context.Context, url.Values) (OperationAuditList, error) {
	return f.operationAudit, nil
}

func (f *fakeAgentClient) ProbeProjectConfig(context.Context, string) (model.Project, error) {
	return f.configProject, nil
}

func (f *fakeAgentClient) GetProjectConfig(context.Context, string) (model.Project, error) {
	return f.configProject, nil
}

func (f *fakeAgentClient) PreviewConfigChange(_ context.Context, req ConfigChangeRequest) (ConfigChangePreview, error) {
	f.lastConfigChange = req
	return f.configPreview, nil
}

func (f *fakeAgentClient) ApplyConfigChange(_ context.Context, req ConfigChangeRequest, approvalToken string) (ConfigChangePreview, error) {
	f.configApplyCallCount++
	f.lastConfigChange = req
	f.lastApprovalToken = approvalToken
	if f.configApplyCallCount <= len(f.configApplyErrs) && f.configApplyErrs[f.configApplyCallCount-1] != nil {
		return ConfigChangePreview{}, f.configApplyErrs[f.configApplyCallCount-1]
	}
	if f.configApplyErr != nil {
		return ConfigChangePreview{}, f.configApplyErr
	}
	return f.configPreview, nil
}

func (f *fakeAgentClient) ListLanguageRuntimeProviders(context.Context) ([]string, error) {
	if f.languageRuntimeProviders != nil {
		return f.languageRuntimeProviders, nil
	}
	return []string{"go"}, nil
}

func (f *fakeAgentClient) DescribeLanguageRuntimeSchema(_ context.Context, language string) (map[string]any, error) {
	f.lastLanguageRuntimeMethod = "describe"
	f.lastLanguageRuntimeLanguage = language
	if f.languageRuntimeSchema != nil {
		return f.languageRuntimeSchema, nil
	}
	return map[string]any{"language": language}, nil
}

func (f *fakeAgentClient) SuggestServiceRuntime(_ context.Context, language string, body map[string]any) (map[string]any, error) {
	f.lastLanguageRuntimeMethod = "suggest"
	f.lastLanguageRuntimeLanguage = language
	f.lastLanguageRuntimeBody = body
	return f.languageRuntimeResponse, nil
}

func (f *fakeAgentClient) ValidateServiceRuntime(_ context.Context, language string, body map[string]any) (map[string]any, error) {
	f.lastLanguageRuntimeMethod = "validate"
	f.lastLanguageRuntimeLanguage = language
	f.lastLanguageRuntimeBody = body
	return f.languageRuntimeResponse, nil
}

func (f *fakeAgentClient) PreviewServiceExecution(_ context.Context, language string, body map[string]any) (map[string]any, error) {
	f.lastLanguageRuntimeMethod = "preview"
	f.lastLanguageRuntimeLanguage = language
	f.lastLanguageRuntimeBody = body
	return f.languageRuntimeResponse, nil
}

func (f *fakeAgentClient) StartDeployment(_ context.Context, id string, approvalToken string) error {
	f.startedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) StopDeployment(_ context.Context, id string, approvalToken string) error {
	f.stopCallCount++
	if f.stopCallCount <= len(f.stopErrors) && f.stopErrors[f.stopCallCount-1] != nil {
		return f.stopErrors[f.stopCallCount-1]
	}
	f.stopCalled = true
	f.stoppedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) RestartDeployment(_ context.Context, id string, approvalToken string) error {
	f.restartCallCount++
	if f.restartCallCount <= len(f.restartErrors) && f.restartErrors[f.restartCallCount-1] != nil {
		return f.restartErrors[f.restartCallCount-1]
	}
	f.restartedDeploymentID = id
	f.lastApprovalToken = approvalToken
	return nil
}

func (f *fakeAgentClient) PreviewPipelineTemplate(context.Context, string, string) (PipelineTemplatePreview, error) {
	return f.templatePreview, nil
}

func (f *fakeAgentClient) ImportPipelineTemplate(_ context.Context, path string, approvalToken string) (PipelineTemplateSummary, error) {
	f.importTemplateCallCount++
	f.importedTemplatePath = path
	f.lastApprovalToken = approvalToken
	if f.importTemplateCallCount <= len(f.importTemplateErrs) && f.importTemplateErrs[f.importTemplateCallCount-1] != nil {
		return PipelineTemplateSummary{}, f.importTemplateErrs[f.importTemplateCallCount-1]
	}
	return f.importedTemplate, nil
}

func (f *fakeAgentClient) DeployProjectPipeline(_ context.Context, _ string, _ string, req PipelineDeployRequest) (model.Run, error) {
	f.pipelineDeployCallCount++
	f.lastPipelineDeploy = req
	if f.pipelineDeployCallCount <= len(f.pipelineDeployErrs) && f.pipelineDeployErrs[f.pipelineDeployCallCount-1] != nil {
		return model.Run{}, f.pipelineDeployErrs[f.pipelineDeployCallCount-1]
	}
	if f.pipelineRun.ID != "" {
		return f.pipelineRun, nil
	}
	version := req.Variables["version"]
	if version == "" {
		version = req.ArtifactVersion
	}
	return model.Run{ID: "run-1", ArtifactVersion: version, Status: model.StatusSuccess}, nil
}

func (f *fakeAgentClient) ValidateProjectPipeline(_ context.Context, _ string, _ string, req ProjectPipelinePreviewRequest) (ProjectPipelinePreview, error) {
	f.lastPipelinePreviewRequest = req
	if f.pipelinePreviewErr != nil {
		return ProjectPipelinePreview{}, f.pipelinePreviewErr
	}
	if f.pipelinePreview.Run.ID != "" || f.pipelinePreview.Plan.Phases != nil {
		return f.pipelinePreview, nil
	}
	return ProjectPipelinePreview{Run: model.Run{ID: "run-1", Status: model.StatusPending}}, nil
}

func (f *fakeAgentClient) ListDebugBrowsers(context.Context) ([]DebugBrowser, error) {
	return f.debugBrowsers, nil
}

func (f *fakeAgentClient) ListBrowserTargets(context.Context) ([]BrowserTarget, error) {
	return f.browserTargets, nil
}

func (f *fakeAgentClient) OpenBrowserSession(_ context.Context, req OpenBrowserSessionRequest, approvalToken string) (BrowserSession, error) {
	f.lastBrowserOpen = req
	f.lastApprovalToken = approvalToken
	return f.browserSession, nil
}

func (f *fakeAgentClient) CloseBrowserSession(_ context.Context, id string) error {
	f.closedBrowserSession = id
	return nil
}

func (f *fakeAgentClient) BrowserSnapshot(_ context.Context, req BrowserSnapshotRequest) (BrowserSnapshot, error) {
	f.lastBrowserSnapshot = req
	return f.browserSnapshot, nil
}

func (f *fakeAgentClient) BrowserClick(_ context.Context, req BrowserClickRequest) (BrowserActionResult, error) {
	f.lastBrowserClick = req
	return f.browserAction, nil
}

func (f *fakeAgentClient) BrowserType(_ context.Context, req BrowserTypeRequest) (BrowserActionResult, error) {
	f.lastBrowserType = req
	return f.browserAction, nil
}

func (f *fakeAgentClient) BrowserScreenshot(_ context.Context, req BrowserScreenshotRequest) (BrowserScreenshot, error) {
	f.lastBrowserScreenshot = req
	if f.browserScreenshotErr != nil {
		return BrowserScreenshot{}, f.browserScreenshotErr
	}
	return f.browserScreenshot, nil
}

func (f *fakeAgentClient) BrowserNavigate(_ context.Context, req BrowserNavigateRequest) (BrowserNavigationResult, error) {
	f.lastBrowserNavigate = req
	return f.browserNavigation, nil
}

func (f *fakeAgentClient) BrowserReload(_ context.Context, req BrowserReloadRequest) (BrowserNavigationResult, error) {
	f.lastBrowserReload = req
	return f.browserNavigation, nil
}

func (f *fakeAgentClient) BrowserWaitForSelector(_ context.Context, req BrowserWaitForSelectorRequest) (BrowserWaitResult, error) {
	f.lastBrowserWait = req
	return f.browserWait, nil
}

func (f *fakeAgentClient) BrowserPressKey(_ context.Context, req BrowserPressKeyRequest) (BrowserActionResult, error) {
	f.lastBrowserPress = req
	return f.browserAction, nil
}

func (f *fakeAgentClient) BrowserSelectOption(_ context.Context, req BrowserSelectOptionRequest) (BrowserActionResult, error) {
	f.lastBrowserSelect = req
	return f.browserAction, nil
}

func (f *fakeAgentClient) BrowserConsoleLogs(_ context.Context, req BrowserConsoleLogsRequest) (BrowserConsoleLogsResult, error) {
	f.lastBrowserConsoleLogs = req
	return f.browserConsoleLogs, nil
}

func (f *fakeAgentClient) BrowserNetworkRequests(_ context.Context, req BrowserNetworkRequestsRequest) (BrowserNetworkRequestsResult, error) {
	f.lastBrowserNetworkRequests = req
	return f.browserNetworkRequests, nil
}

func (f *fakeAgentClient) BrowserEvaluate(_ context.Context, req BrowserEvaluateRequest) (BrowserEvaluateResult, error) {
	f.lastBrowserEvaluate = req
	return f.browserEvaluate, nil
}

func (f *fakeAgentClient) BrowserSetViewport(_ context.Context, req BrowserSetViewportRequest) (BrowserViewportResult, error) {
	f.lastBrowserSetViewport = req
	return f.browserViewport, nil
}

func (f *fakeAgentClient) ListCodeDebugTargets(context.Context) ([]CodeDebugTarget, error) {
	return f.codeDebugTargets, nil
}

func (f *fakeAgentClient) SetCodeDebugBreakpoints(_ context.Context, req DebugBreakpointRequest) (map[string]any, error) {
	f.lastBreakpoint = req
	return f.codeDebugActionResult, nil
}

func (f *fakeAgentClient) CodeDebugAction(_ context.Context, deploymentID string, action string, body map[string]any) (map[string]any, error) {
	f.lastDebugActionDeploymentID = deploymentID
	f.lastDebugAction = action
	f.lastDebugActionBody = body
	return f.codeDebugActionResult, nil
}

func (f *fakeAgentClient) CodeDebugEvaluate(_ context.Context, req DebugEvaluateRequest, approvalToken string) (map[string]any, error) {
	f.lastEvaluate = req
	f.lastApprovalToken = approvalToken
	return f.codeDebugEvaluateResult, nil
}

func (f *fakeAgentClient) CodeDebugCaptureAt(_ context.Context, req DebugCaptureAtRequest, approvalToken string) (map[string]any, error) {
	f.lastCaptureAt = req
	f.lastApprovalToken = approvalToken
	return f.codeDebugCaptureResult, nil
}

func (f *fakeAgentClient) CodeDebugInspect(_ context.Context, req DebugInspectRequest) (map[string]any, error) {
	f.lastInspect = req
	return f.codeDebugInspectResult, nil
}

func (f *fakeAgentClient) ListPipelineRuns(context.Context, string, string) ([]model.Run, error) {
	if f.pipelineRuns != nil {
		return f.pipelineRuns, nil
	}
	return []model.Run{{ID: "run-1", Status: model.StatusSuccess}}, nil
}

func (f *fakeAgentClient) ListPipelineArtifacts(context.Context, string, string) ([]model.ArtifactRef, error) {
	if f.pipelineArtifacts != nil {
		return f.pipelineArtifacts, nil
	}
	return []model.ArtifactRef{{Version: "v1", Kind: model.ArtifactKindFile}}, nil
}

func (f *fakeAgentClient) ReadPipelineRunLogs(_ context.Context, _ string, _ string, _ string, q url.Values) ([]model.RunLogLine, error) {
	f.lastPipelineLogQuery = q
	if f.pipelineLogs != nil {
		return f.pipelineLogs, nil
	}
	return []model.RunLogLine{{RunID: "run-1", StepName: q.Get("step_name"), HostID: q.Get("host_id"), Line: "ok"}}, nil
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

func TestRuntimeSnapshotAddsDebugCredentialGuidance(t *testing.T) {
	project := sampleProject()
	project.DebugCredentials = []model.DebugCredential{{Name: "login", Value: "secret", Desc: "登录"}}
	client := &fakeAgentClient{
		projects: []model.Project{project},
		services: []model.Service{sampleService("api", model.StatusRunning, "dep-api-dev")},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_runtime_snapshot", `{}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	assert.Contains(t, payload.NextActions, "Debug credentials are configured. For authenticated API checks, call get_debug_credentials with project_id/project_name and optional service_id/service_name instead of fabricating tokens.")
}

func TestListServicesIncludesMergedDebugCredentialHints(t *testing.T) {
	project := sampleProject()
	project.DebugCredentials = []model.DebugCredential{
		{Name: "shared_login", Value: "project-secret", Desc: "项目默认登录"},
		{Name: "project_only", Value: "project-only-secret", Desc: "项目专用"},
	}
	service := sampleService("api", model.StatusRunning, "dep-api-dev")
	service.ProjectID = project.ID
	service.DebugCredentials = []model.DebugCredential{
		{Name: "shared_login", Value: "service-secret", Desc: "服务覆盖登录"},
		{Name: "service_only", Value: "service-only-secret", Desc: "服务专用"},
	}
	client := &fakeAgentClient{
		projects: []model.Project{project},
		services: []model.Service{service},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "list_services", `{"project_id":"p1"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	services := data["services"].([]model.Service)
	require.Len(t, services, 1)
	got := services[0]
	assert.Nil(t, got.DebugCredentials)
	assert.True(t, got.HasDebugCredentials)
	require.Len(t, got.DebugCredentialHints, 3)
	assert.Equal(t, []model.DebugCredentialHint{
		{Name: "shared_login", Desc: "服务覆盖登录", Source: "service"},
		{Name: "project_only", Desc: "项目专用", Source: "project"},
		{Name: "service_only", Desc: "服务专用", Source: "service"},
	}, got.DebugCredentialHints)
	for _, hint := range got.DebugCredentialHints {
		assert.NotContains(t, hint.Name, "secret")
		assert.NotContains(t, hint.Desc, "secret")
	}
}

func TestListHostsToolReturnsCanonicalHostIDs(t *testing.T) {
	client := &fakeAgentClient{
		hosts: []HostReference{
			{ID: "host-uuid-1", Name: "prod-a", PrivateIP: "10.0.0.1", Tags: []string{"prod"}},
			{ID: "superdev-local", Name: "MacBook-Pro.local", IsSelf: true, NodeID: "superdev-local"},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "list_hosts", `{}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	hosts := data["hosts"].([]HostReference)
	remoteHosts := data["remote_hosts"].([]HostReference)
	require.Len(t, hosts, 2)
	require.Len(t, remoteHosts, 1)
	assert.Equal(t, "host-uuid-1", hosts[0].ID)
	assert.Equal(t, "prod-a", hosts[0].Name)
	assert.Equal(t, "host-uuid-1", remoteHosts[0].ID)
	assert.Equal(t, 1, data["remote_count"])
	assert.Contains(t, data["host_id_contract"], "hosts[].id")
	assert.Contains(t, data["host_id_contract"], "is_self=false")
	assert.Contains(t, data["host_id_contract"], "never use hosts[].name")
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

func TestStartServiceAmbiguousTargetSanitizesCredentialCandidates(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProjectWithDebugCredentials()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "start_service", `{"project_name":"demo","service_name":"api"}`)

	require.NoError(t, err)
	assertAmbiguousCredentialCandidatesSanitized(t, result)
}

func TestRestartServiceWaitsForApprovalAndRetriesWithToken(t *testing.T) {
	approval := OperationApproval{
		ID:     "opa_1",
		Status: "pending",
		Plan: OperationPlan{
			ID:               "op_1",
			Kind:             "runtime.restart",
			Target:           OperationTarget{DeploymentID: "dep-api-prod"},
			RequiresApproval: true,
			Fingerprint:      "fp_1",
		},
	}
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		restartErrors: []error{AgentError{
			Code:     "approval_required",
			Message:  "approval required",
			Plan:     approval.Plan,
			Approval: approval,
		}},
		operationApprovalDetails: []OperationApprovalDetail{{
			Approval:      OperationApproval{ID: "opa_1", Status: "approved", Plan: approval.Plan},
			ApprovalToken: "tok_1",
		}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "restart_service", `{"deployment_id":"dep-api-prod"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 2, client.restartCallCount)
	assert.Equal(t, 1, client.getApprovalCallCount)
	assert.Equal(t, "dep-api-prod", client.restartedDeploymentID)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
}

func TestStopServiceWaitsForApprovalAndRetriesWithToken(t *testing.T) {
	approval := OperationApproval{
		ID:     "opa_1",
		Status: "pending",
		Plan: OperationPlan{
			ID:               "op_1",
			Kind:             "runtime.stop",
			Target:           OperationTarget{DeploymentID: "dep-api-prod"},
			RequiresApproval: true,
			Fingerprint:      "fp_1",
		},
	}
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		stopErrors: []error{AgentError{
			Code:     "approval_required",
			Message:  "approval required",
			Plan:     approval.Plan,
			Approval: approval,
		}},
		operationApprovalDetails: []OperationApprovalDetail{{
			Approval:      OperationApproval{ID: "opa_1", Status: "approved", Plan: approval.Plan},
			ApprovalToken: "tok_1",
		}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "stop_service", `{"deployment_id":"dep-api-prod"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 2, client.stopCallCount)
	assert.Equal(t, "dep-api-prod", client.stoppedDeploymentID)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
}

func TestRestartServiceCanDisableApprovalWait(t *testing.T) {
	approval := OperationApproval{
		ID:     "opa_1",
		Status: "pending",
		Plan: OperationPlan{
			ID:               "op_1",
			Kind:             "runtime.restart",
			Target:           OperationTarget{DeploymentID: "dep-api-prod"},
			RequiresApproval: true,
			Fingerprint:      "fp_1",
		},
	}
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		restartErrors: []error{AgentError{
			Code:     "approval_required",
			Message:  "approval required",
			Plan:     approval.Plan,
			Approval: approval,
		}},
		operationApprovalDetail: OperationApprovalDetail{
			Approval:      OperationApproval{ID: "opa_1", Status: "approved", Plan: approval.Plan},
			ApprovalToken: "tok_1",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "restart_service", `{"deployment_id":"dep-api-prod","approval_wait_seconds":0}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "approval_required", payload.Code)
	assert.Equal(t, 1, client.restartCallCount)
	assert.Equal(t, 0, client.getApprovalCallCount)
	assert.Equal(t, "", client.lastApprovalToken)
}

func sampleProjectWithDebugCredentials() model.Project {
	project := sampleProject()
	project.DebugCredentials = []model.DebugCredential{
		{Name: "shared_login", Value: "project-secret", Desc: "项目默认登录"},
	}
	project.Services[0].DebugCredentials = []model.DebugCredential{
		{Name: "shared_login", Value: "service-secret", Desc: "服务覆盖登录"},
		{Name: "service_only", Value: "service-only-secret", Desc: "服务专用"},
	}
	return project
}

func assertAmbiguousCredentialCandidatesSanitized(t *testing.T, result CallToolResult) {
	t.Helper()

	require.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "env_required", payload.Code)
	resolveErr := payload.Data.(*resolveError)
	require.Len(t, resolveErr.Candidates, 2)
	for _, candidate := range resolveErr.Candidates {
		assert.Nil(t, candidate.Project.DebugCredentials)
		assert.True(t, candidate.Project.HasDebugCredentials)
		assert.Equal(t, []model.DebugCredentialHint{
			{Name: "shared_login", Desc: "项目默认登录", Source: "project"},
		}, candidate.Project.DebugCredentialHints)
		assert.Nil(t, candidate.Service.DebugCredentials)
		assert.True(t, candidate.Service.HasDebugCredentials)
		assert.Equal(t, []model.DebugCredentialHint{
			{Name: "shared_login", Desc: "服务覆盖登录", Source: "service"},
			{Name: "service_only", Desc: "服务专用", Source: "service"},
		}, candidate.Service.DebugCredentialHints)
		for _, hint := range candidate.Service.DebugCredentialHints {
			assert.NotContains(t, hint.Name, "secret")
			assert.NotContains(t, hint.Desc, "secret")
		}
	}
}
