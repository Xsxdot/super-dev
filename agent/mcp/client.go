// client.go 实现 SuperDev MCP 到本机 agent 的 HTTP 适配。
//
// 职责：
//   - 封装 agent REST API 请求
//   - 将非 2xx 响应转为明确错误
//   - 为 MCP 工具提供可 mock 的 AgentClient 接口
//
// 边界：
//   - 不缓存运行态
//   - 不直接读写文件
//   - 不直接访问 SQLite 或进程管理器
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// AgentClient 描述 MCP 需要的本机 agent 能力。
type AgentClient interface {
	// ListProjects 拉取 agent 当前注册的项目列表。
	ListProjects(context.Context) ([]model.Project, error)
	// ListHosts 拉取 agent 当前可选择的主机安全视图。
	ListHosts(context.Context) ([]HostReference, error)
	// ListServices 拉取所有服务及其 deployment 运行态。
	ListServices(context.Context) ([]model.Service, error)
	// ProjectRules 拉取项目日志过滤规则。
	ProjectRules(context.Context, string) ([]model.LogRule, error)
	// FetchDeploymentLogs 拉取 deployment 日志分页。
	FetchDeploymentLogs(context.Context, string, url.Values) (LogsResponse, error)
	// SearchLogs 搜索项目或 deployment 范围内的历史日志。
	SearchLogs(context.Context, url.Values) (LogSearchResponse, error)
	// FetchLogContext 拉取指定日志附近的跨服务上下文。
	FetchLogContext(context.Context, url.Values) (LogContextResponse, error)
	// CreateDebugSession 创建本机排障会话。
	CreateDebugSession(context.Context, DebugSessionCreateRequest) (DebugSessionCreateResponse, error)
	// ListDebugSessions 查询本机排障会话列表。
	ListDebugSessions(context.Context, url.Values) ([]DebugSession, error)
	// GetDebugSession 读取本机排障会话详情。
	GetDebugSession(context.Context, string, int) (DebugSessionDetailResponse, error)
	// AppendDebugSessionEvent 追加本机排障会话事件。
	AppendDebugSessionEvent(context.Context, string, DebugSessionAppendEventRequest) (DebugSessionEvent, error)
	// CloseDebugSession 关闭本机排障会话。
	CloseDebugSession(context.Context, string, string) (DebugSession, error)
	// PreviewOperation 请求 agent 生成写操作预检计划。
	PreviewOperation(context.Context, OperationRequest) (OperationPlan, error)
	// ListOperationApprovals 查询写操作审批请求。
	ListOperationApprovals(context.Context, url.Values) ([]OperationApproval, error)
	// GetOperationApproval 读取单条审批详情，已批准时可能返回一次性 token。
	GetOperationApproval(context.Context, string) (OperationApprovalDetail, error)
	// ListOperationAudit 查询写操作审计事件。
	ListOperationAudit(context.Context, url.Values) (OperationAuditList, error)
	// ProbeProjectConfig 探测项目目录配置，不写注册表或配置文件。
	ProbeProjectConfig(context.Context, string) (model.Project, error)
	// GetProjectConfig 读取可编辑项目配置快照。
	GetProjectConfig(context.Context, string) (model.Project, error)
	// PreviewConfigChange 预览配置 upsert diff 和 operation plan。
	PreviewConfigChange(context.Context, ConfigChangeRequest) (ConfigChangePreview, error)
	// ApplyConfigChange 经 safe operation 授权后应用配置 upsert。
	ApplyConfigChange(context.Context, ConfigChangeRequest, string) (ConfigChangePreview, error)
	// StartDeployment 请求 agent 启动 deployment。
	StartDeployment(context.Context, string, string) error
	// StopDeployment 请求 agent 停止 deployment。
	StopDeployment(context.Context, string, string) error
	// RestartDeployment 请求 agent 重启 deployment。
	RestartDeployment(context.Context, string, string) error
	// PreviewPipelineTemplate 请求 agent dry-run 解析模板。
	PreviewPipelineTemplate(context.Context, string, string) (PipelineTemplatePreview, error)
	// ImportPipelineTemplate 请求 agent 导入用户模板文件。
	ImportPipelineTemplate(context.Context, string, string) (PipelineTemplateSummary, error)
	// DeployProjectPipeline 通过项目级 pipeline 触发部署或回滚。
	DeployProjectPipeline(context.Context, string, string, PipelineDeployRequest) (model.Run, error)
	// ValidateProjectPipeline 预览并校验已保存的项目级 pipeline。
	ValidateProjectPipeline(context.Context, string, string, ProjectPipelinePreviewRequest) (ProjectPipelinePreview, error)
	// ListPipelineRuns 查询项目级 pipeline 执行历史。
	ListPipelineRuns(context.Context, string, string) ([]model.Run, error)
	// ListPipelineArtifacts 查询项目级 pipeline 制品历史。
	ListPipelineArtifacts(context.Context, string, string) ([]model.ArtifactRef, error)
	// ReadPipelineRunLogs 查询项目级 pipeline 单次执行日志。
	ReadPipelineRunLogs(context.Context, string, string, string, url.Values) ([]model.RunLogLine, error)
}

// AgentError 保留 agent 业务错误中的机器可读 code 和结构化数据。
type AgentError struct {
	Code     string
	Message  string
	Plan     OperationPlan
	Approval OperationApproval
}

// Error 返回适合日志和工具错误展示的 agent 错误摘要。
func (e AgentError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// HTTPAgentClient 是 AgentClient 的 HTTP 实现。
type HTTPAgentClient struct {
	baseURL string
	http    *http.Client
}

// NewHTTPAgentClient 创建 HTTP agent client。
//
// 参数：
//   - baseURL: 本机 SuperDev agent HTTP 地址
//   - httpClient: 可选 HTTP client，nil 时使用带超时的默认 client
//
// 返回：
//   - 可访问本机 agent 的 HTTPAgentClient
func NewHTTPAgentClient(baseURL string, httpClient *http.Client) *HTTPAgentClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPAgentClient{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// ListProjects 拉取 agent 当前注册的项目列表。
//
// 参数：
//   - ctx: 请求上下文
//
// 返回：
//   - 项目列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListProjects(ctx context.Context) ([]model.Project, error) {
	var out []model.Project
	return out, c.get(ctx, "/api/projects", &out)
}

// ListHosts 拉取 agent 当前可选择的主机安全视图。
//
// 参数：
//   - ctx: 请求上下文
//
// 返回：
//   - 主机安全视图列表，包含本机节点和远程主机
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListHosts(ctx context.Context) ([]HostReference, error) {
	var out []HostReference
	return out, c.get(ctx, "/api/hosts", &out)
}

// ListServices 拉取所有服务及其 deployment 运行态。
//
// 参数：
//   - ctx: 请求上下文
//
// 返回：
//   - 服务列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListServices(ctx context.Context) ([]model.Service, error) {
	var out []model.Service
	return out, c.get(ctx, "/api/services", &out)
}

// ProjectRules 拉取项目日志过滤规则。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//
// 返回：
//   - 日志过滤规则
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ProjectRules(ctx context.Context, projectID string) ([]model.LogRule, error) {
	var out []model.LogRule
	return out, c.get(ctx, "/api/projects/"+url.PathEscape(projectID)+"/rules", &out)
}

// FetchDeploymentLogs 拉取 deployment 日志分页。
//
// 参数：
//   - ctx: 请求上下文
//   - depID: deployment ID
//   - q: 日志查询参数
//
// 返回：
//   - 日志分页响应
//   - HTTP 或解码错误
func (c *HTTPAgentClient) FetchDeploymentLogs(ctx context.Context, depID string, q url.Values) (LogsResponse, error) {
	var out LogsResponse
	path := "/api/deployments/" + url.PathEscape(depID) + "/logs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.get(ctx, path, &out)
}

// SearchLogs 搜索项目或 deployment 范围内的历史日志。
//
// 参数：
//   - ctx: 请求上下文
//   - q: 搜索查询参数
//
// 返回：
//   - 搜索响应
//   - HTTP 或解码错误
func (c *HTTPAgentClient) SearchLogs(ctx context.Context, q url.Values) (LogSearchResponse, error) {
	var out LogSearchResponse
	return out, c.get(ctx, withQuery("/api/log-search", q), &out)
}

// FetchLogContext 拉取指定日志附近的跨服务上下文。
//
// 参数：
//   - ctx: 请求上下文
//   - q: 上下文查询参数
//
// 返回：
//   - 日志上下文响应
//   - HTTP 或解码错误
func (c *HTTPAgentClient) FetchLogContext(ctx context.Context, q url.Values) (LogContextResponse, error) {
	var out LogContextResponse
	return out, c.get(ctx, withQuery("/api/logs/context", q), &out)
}

// CreateDebugSession 创建本机排障会话。
//
// 参数：
//   - ctx: 请求上下文
//   - req: 会话归属和问题描述
//
// 返回：
//   - 创建后的会话和初始事件
//   - HTTP 或解码错误
func (c *HTTPAgentClient) CreateDebugSession(ctx context.Context, req DebugSessionCreateRequest) (DebugSessionCreateResponse, error) {
	var out DebugSessionCreateResponse
	return out, c.post(ctx, "/api/debug-sessions", req, &out)
}

// ListDebugSessions 查询本机排障会话列表。
//
// 参数：
//   - ctx: 请求上下文
//   - q: 过滤查询参数
//
// 返回：
//   - 会话列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListDebugSessions(ctx context.Context, q url.Values) ([]DebugSession, error) {
	var out []DebugSession
	return out, c.get(ctx, withQuery("/api/debug-sessions", q), &out)
}

// GetDebugSession 读取本机排障会话详情。
//
// 参数：
//   - ctx: 请求上下文
//   - id: 会话 ID
//   - limit: 事件数量限制，正数时传给 agent
//
// 返回：
//   - 会话详情
//   - HTTP 或解码错误
func (c *HTTPAgentClient) GetDebugSession(ctx context.Context, id string, limit int) (DebugSessionDetailResponse, error) {
	var out DebugSessionDetailResponse
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/debug-sessions/" + url.PathEscape(id)
	return out, c.get(ctx, withQuery(path, q), &out)
}

// AppendDebugSessionEvent 追加本机排障会话事件。
//
// 参数：
//   - ctx: 请求上下文
//   - id: 会话 ID
//   - req: 事件内容
//
// 返回：
//   - 新事件
//   - HTTP 或解码错误
func (c *HTTPAgentClient) AppendDebugSessionEvent(ctx context.Context, id string, req DebugSessionAppendEventRequest) (DebugSessionEvent, error) {
	var out DebugSessionEvent
	return out, c.post(ctx, "/api/debug-sessions/"+url.PathEscape(id)+"/events", req, &out)
}

// CloseDebugSession 关闭本机排障会话。
//
// 参数：
//   - ctx: 请求上下文
//   - id: 会话 ID
//   - summary: 关闭原因摘要
//
// 返回：
//   - 关闭后的会话
//   - HTTP 或解码错误
func (c *HTTPAgentClient) CloseDebugSession(ctx context.Context, id string, summary string) (DebugSession, error) {
	var out DebugSession
	return out, c.post(ctx, "/api/debug-sessions/"+url.PathEscape(id)+"/close", map[string]string{"summary": summary}, &out)
}

// PreviewOperation 请求 agent 生成写操作预检计划。
//
// 参数：
//   - ctx: 请求上下文
//   - req: operation kind 和目标解析参数
//
// 返回：
//   - agent 生成的稳定 operation plan
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) PreviewOperation(ctx context.Context, req OperationRequest) (OperationPlan, error) {
	var out OperationPlan
	return out, c.post(ctx, "/api/operations/preflight", req, &out)
}

// ListOperationApprovals 查询写操作审批请求。
//
// 参数：
//   - ctx: 请求上下文
//   - q: status、project_id、limit 等过滤参数
//
// 返回：
//   - 审批请求列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListOperationApprovals(ctx context.Context, q url.Values) ([]OperationApproval, error) {
	var out []OperationApproval
	return out, c.get(ctx, withQuery("/api/operation-approvals", q), &out)
}

// GetOperationApproval 读取单条审批详情。
//
// 参数：
//   - ctx: 请求上下文
//   - id: 审批请求 ID
//
// 返回：
//   - 审批详情，已批准时可能包含一次性 token
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) GetOperationApproval(ctx context.Context, id string) (OperationApprovalDetail, error) {
	var out OperationApprovalDetail
	return out, c.get(ctx, "/api/operation-approvals/"+url.PathEscape(id), &out)
}

// ListOperationAudit 查询写操作审计事件。
//
// 参数：
//   - ctx: 请求上下文
//   - q: project_id、kind、approval_id、since、limit 等过滤参数
//
// 返回：
//   - 审计事件列表响应
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListOperationAudit(ctx context.Context, q url.Values) (OperationAuditList, error) {
	var out OperationAuditList
	return out, c.get(ctx, withQuery("/api/operation-audit", q), &out)
}

// ProbeProjectConfig 探测项目目录配置，不产生副作用。
//
// 参数：
//   - ctx: 请求上下文
//   - rootPath: 项目根目录
//
// 返回：
//   - 探测到的项目配置快照
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ProbeProjectConfig(ctx context.Context, rootPath string) (model.Project, error) {
	var out model.Project
	q := url.Values{}
	q.Set("root_path", rootPath)
	return out, c.get(ctx, withQuery("/api/projects/probe", q), &out)
}

// GetProjectConfig 读取可编辑项目配置快照。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//
// 返回：
//   - 可编辑项目配置快照
//   - HTTP 或解码错误
func (c *HTTPAgentClient) GetProjectConfig(ctx context.Context, projectID string) (model.Project, error) {
	var out model.Project
	return out, c.get(ctx, "/api/projects/"+url.PathEscape(projectID)+"/config", &out)
}

// PreviewConfigChange 预览配置 upsert。
//
// 参数：
//   - ctx: 请求上下文
//   - req: 配置 upsert 请求
//
// 返回：
//   - diff、validation 和 operation plan
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) PreviewConfigChange(ctx context.Context, req ConfigChangeRequest) (ConfigChangePreview, error) {
	var out ConfigChangePreview
	return out, c.post(ctx, "/api/config-changes/preview", req, &out)
}

// ApplyConfigChange 应用配置 upsert。
//
// 参数：
//   - ctx: 请求上下文
//   - req: 配置 upsert 请求
//   - approvalToken: 用户批准后发放的一次性 token，可为空
//
// 返回：
//   - 应用后的 preview 结果
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) ApplyConfigChange(ctx context.Context, req ConfigChangeRequest, approvalToken string) (ConfigChangePreview, error) {
	var out ConfigChangePreview
	return out, c.postWithApprovalToken(ctx, "/api/config-changes/apply", req, approvalToken, &out)
}

// StartDeployment 请求 agent 启动 deployment。
//
// 参数：
//   - ctx: 请求上下文
//   - depID: deployment ID
//   - approvalToken: 用户批准后发放的一次性 token，可为空
//
// 返回：
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) StartDeployment(ctx context.Context, depID string, approvalToken string) error {
	return c.postWithApprovalToken(ctx, "/api/deployments/"+url.PathEscape(depID)+"/start", nil, approvalToken, nil)
}

// StopDeployment 请求 agent 停止 deployment。
//
// 参数：
//   - ctx: 请求上下文
//   - depID: deployment ID
//   - approvalToken: 用户批准后发放的一次性 token，可为空
//
// 返回：
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) StopDeployment(ctx context.Context, depID string, approvalToken string) error {
	return c.postWithApprovalToken(ctx, "/api/deployments/"+url.PathEscape(depID)+"/stop", nil, approvalToken, nil)
}

// RestartDeployment 请求 agent 重启 deployment。
//
// 参数：
//   - ctx: 请求上下文
//   - depID: deployment ID
//   - approvalToken: 用户批准后发放的一次性 token，可为空
//
// 返回：
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) RestartDeployment(ctx context.Context, depID string, approvalToken string) error {
	return c.postWithApprovalToken(ctx, "/api/deployments/"+url.PathEscape(depID)+"/restart", nil, approvalToken, nil)
}

// PreviewPipelineTemplate 请求 agent dry-run 解析模板。
//
// 参数：
//   - ctx: 请求上下文
//   - path: 模板文件路径，与 yamlText 二选一
//   - yamlText: 模板 YAML 字符串，与 path 二选一
//
// 返回：
//   - 模板 preview 结果
//   - HTTP 或解码错误
func (c *HTTPAgentClient) PreviewPipelineTemplate(ctx context.Context, path, yamlText string) (PipelineTemplatePreview, error) {
	var out PipelineTemplatePreview
	return out, c.post(ctx, "/api/pipeline/templates/preview", map[string]string{"path": path, "yaml": yamlText}, &out)
}

// ImportPipelineTemplate 请求 agent 导入用户模板文件。
//
// 参数：
//   - ctx: 请求上下文
//   - path: 模板 YAML 文件路径
//   - approvalToken: 用户批准后发放的一次性 token，可为空
//
// 返回：
//   - 导入后的模板摘要
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) ImportPipelineTemplate(ctx context.Context, path string, approvalToken string) (PipelineTemplateSummary, error) {
	var out PipelineTemplateSummary
	return out, c.postWithApprovalToken(ctx, "/api/pipeline/templates/import", map[string]string{"path": path}, approvalToken, &out)
}

// DeployProjectPipeline 通过项目级 pipeline 触发部署或回滚。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - pipelineID: 项目级 pipeline ID
//   - req: 部署或回滚请求体
//
// 返回：
//   - agent 返回的 Run 终态
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) DeployProjectPipeline(ctx context.Context, projectID, pipelineID string, req PipelineDeployRequest) (model.Run, error) {
	var out model.Run
	path := "/api/projects/" + url.PathEscape(projectID) + "/pipelines/" + url.PathEscape(pipelineID) + "/deploy"
	return out, c.postWithApprovalToken(ctx, path, req, req.ApprovalToken, &out)
}

// ValidateProjectPipeline 预览并校验已保存的项目级 pipeline。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - pipelineID: 项目级 pipeline ID
//   - req: env、变量和服务选择
//
// 返回：
//   - agent 返回的 preview plan/run
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) ValidateProjectPipeline(ctx context.Context, projectID, pipelineID string, req ProjectPipelinePreviewRequest) (ProjectPipelinePreview, error) {
	var out ProjectPipelinePreview
	path := "/api/projects/" + url.PathEscape(projectID) + "/pipelines/" + url.PathEscape(pipelineID) + "/preview"
	return out, c.post(ctx, path, req, &out)
}

// ListPipelineRuns 查询项目级 pipeline 执行历史。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - pipelineID: 项目级 pipeline ID
//
// 返回：
//   - 最近的 Run 列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListPipelineRuns(ctx context.Context, projectID, pipelineID string) ([]model.Run, error) {
	var out struct {
		Items []model.Run `json:"items"`
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/pipelines/" + url.PathEscape(pipelineID) + "/runs"
	return out.Items, c.get(ctx, path, &out)
}

// ListPipelineArtifacts 查询项目级 pipeline 制品历史。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - pipelineID: 项目级 pipeline ID
//
// 返回：
//   - 制品引用列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListPipelineArtifacts(ctx context.Context, projectID, pipelineID string) ([]model.ArtifactRef, error) {
	var out struct {
		Items []model.ArtifactRef `json:"items"`
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/pipelines/" + url.PathEscape(pipelineID) + "/artifacts"
	return out.Items, c.get(ctx, path, &out)
}

// ReadPipelineRunLogs 查询项目级 pipeline 单次执行日志。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - pipelineID: 项目级 pipeline ID
//   - runID: Run ID
//   - q: step_name、host_id、limit、before 等查询参数
//
// 返回：
//   - 日志行列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ReadPipelineRunLogs(ctx context.Context, projectID, pipelineID, runID string, q url.Values) ([]model.RunLogLine, error) {
	var out struct {
		Items []model.RunLogLine `json:"items"`
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/pipelines/" + url.PathEscape(pipelineID) + "/runs/" + url.PathEscape(runID) + "/logs"
	return out.Items, c.get(ctx, withQuery(path, q), &out)
}

func (c *HTTPAgentClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *HTTPAgentClient) post(ctx context.Context, path string, body any, out any) error {
	return c.postWithApprovalToken(ctx, path, body, "", out)
}

func (c *HTTPAgentClient) postWithApprovalToken(ctx context.Context, path string, body any, approvalToken string, out any) error {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(approvalToken) != "" {
		req.Header.Set("X-SuperDev-Approval-Token", strings.TrimSpace(approvalToken))
	}
	return c.do(req, out)
}

func (c *HTTPAgentClient) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body struct {
			Code     string            `json:"code"`
			Error    string            `json:"error"`
			Plan     OperationPlan     `json:"plan"`
			Approval OperationApproval `json:"approval"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body.Error == "" {
			body.Error = resp.Status
		}
		if body.Code != "" {
			return AgentError{Code: body.Code, Message: body.Error, Plan: body.Plan, Approval: body.Approval}
		}
		return fmt.Errorf("agent error: %s", body.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func withQuery(path string, q url.Values) string {
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}
