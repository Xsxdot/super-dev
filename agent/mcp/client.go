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
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/model"
)

// isTLSRequiredResponse 识别「明文请求打到 TLS 监听器」的固定失败形态：
// Go 标准库 TLS 监听器对明文 HTTP 回明文 400 + 固定提示语。
// 识别它是为了把不可读的解码/状态错误换成「改用 https:// 或升级 agent」的指引。
func isTLSRequiredResponse(status int, body []byte) bool {
	return status == http.StatusBadRequest && bytes.Contains(body, []byte("HTTP request to an HTTPS server"))
}

// AgentClient 描述 MCP 需要的本机 agent 能力。
type AgentClient interface {
	// ListProjects 拉取 agent 当前注册的项目列表。
	ListProjects(context.Context) ([]model.Project, error)
	// ListHosts 拉取 agent 当前可选择的主机安全视图。
	ListHosts(context.Context) ([]HostReference, error)
	// ListServices 拉取所有服务及其 deployment 运行态。
	ListServices(context.Context) ([]model.Service, error)
	// GetDebugCredentials 拉取项目/服务的合并调试凭据(明文，专供 AI 调试取用)。
	GetDebugCredentials(context.Context, url.Values) ([]model.MergedDebugCredential, error)
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
	// ListLanguageRuntimeProviders 返回支持的语言运行 provider。
	ListLanguageRuntimeProviders(context.Context) ([]string, error)
	// DescribeLanguageRuntimeSchema 返回某语言的运行配置 schema。
	DescribeLanguageRuntimeSchema(context.Context, string) (map[string]any, error)
	// SuggestServiceRuntime 返回候选语言运行配置。
	SuggestServiceRuntime(context.Context, string, map[string]any) (map[string]any, error)
	// ValidateServiceRuntime 校验语言运行配置。
	ValidateServiceRuntime(context.Context, string, map[string]any) (map[string]any, error)
	// PreviewServiceExecution 返回语言运行执行计划预览。
	PreviewServiceExecution(context.Context, string, map[string]any) (map[string]any, error)
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
	// ListDebugBrowsers 查询本机调试浏览器配置和可用性。
	ListDebugBrowsers(context.Context) ([]DebugBrowser, error)
	// ListBrowserTargets 查询可打开的本机前端调试目标。
	ListBrowserTargets(context.Context) ([]BrowserTarget, error)
	// OpenBrowserSession 创建本机前端浏览器调试会话。
	OpenBrowserSession(context.Context, OpenBrowserSessionRequest, string) (BrowserSession, error)
	// CloseBrowserSession 关闭由 SuperDev 创建的浏览器调试会话。
	CloseBrowserSession(context.Context, string) error
	// BrowserSnapshot 读取浏览器页面快照。
	BrowserSnapshot(context.Context, BrowserSnapshotRequest) (BrowserSnapshot, error)
	// BrowserClick 点击浏览器页面元素。
	BrowserClick(context.Context, BrowserClickRequest) (BrowserActionResult, error)
	// BrowserType 向浏览器页面元素输入文本。
	BrowserType(context.Context, BrowserTypeRequest) (BrowserActionResult, error)
	// BrowserScreenshot 截取浏览器页面。
	BrowserScreenshot(context.Context, BrowserScreenshotRequest) (BrowserScreenshot, error)
	// BrowserNavigate 对浏览器页面执行同源整页导航。
	BrowserNavigate(context.Context, BrowserNavigateRequest) (BrowserNavigationResult, error)
	// BrowserReload 刷新浏览器页面。
	BrowserReload(context.Context, BrowserReloadRequest) (BrowserNavigationResult, error)
	// BrowserWaitForSelector 等待浏览器页面 selector。
	BrowserWaitForSelector(context.Context, BrowserWaitForSelectorRequest) (BrowserWaitResult, error)
	// BrowserPressKey 向浏览器页面发送按键。
	BrowserPressKey(context.Context, BrowserPressKeyRequest) (BrowserActionResult, error)
	// BrowserSelectOption 选择浏览器页面 select 选项。
	BrowserSelectOption(context.Context, BrowserSelectOptionRequest) (BrowserActionResult, error)
	// BrowserConsoleLogs 读取浏览器页面 console 日志。
	BrowserConsoleLogs(context.Context, BrowserConsoleLogsRequest) (BrowserConsoleLogsResult, error)
	// BrowserNetworkRequests 读取浏览器页面网络请求摘要。
	BrowserNetworkRequests(context.Context, BrowserNetworkRequestsRequest) (BrowserNetworkRequestsResult, error)
	// BrowserEvaluate 执行浏览器页面 JavaScript。
	BrowserEvaluate(context.Context, BrowserEvaluateRequest) (BrowserEvaluateResult, error)
	// BrowserSetViewport 更新浏览器页面 viewport 尺寸。
	BrowserSetViewport(context.Context, BrowserSetViewportRequest) (BrowserViewportResult, error)
	// ListCodeDebugTargets 查询可打开的本机代码调试目标。
	ListCodeDebugTargets(context.Context) ([]CodeDebugTarget, error)
	// SetCodeDebugBreakpoints 按 deployment 设置代码调试断点。
	SetCodeDebugBreakpoints(context.Context, DebugBreakpointRequest) (map[string]any, error)
	// CodeDebugAction 按 deployment 执行非 evaluate 的代码调试动作。
	CodeDebugAction(context.Context, string, string, map[string]any) (map[string]any, error)
	// CodeDebugEvaluate 按 deployment 执行代码调试表达式求值。
	CodeDebugEvaluate(context.Context, DebugEvaluateRequest, string) (map[string]any, error)
	// CodeDebugCaptureAt 按 deployment 执行 stop-at-line 复合采集。
	CodeDebugCaptureAt(context.Context, DebugCaptureAtRequest, string) (map[string]any, error)
	// CodeDebugInspect 按 deployment 执行已暂停现场复合读取。
	CodeDebugInspect(context.Context, DebugInspectRequest) (map[string]any, error)
	// ListPipelineRuns 查询项目级 pipeline 执行历史。
	ListPipelineRuns(context.Context, string, string) ([]model.Run, error)
	// ListPipelineArtifacts 查询项目级 pipeline 制品历史。
	ListPipelineArtifacts(context.Context, string, string) ([]model.ArtifactRef, error)
	// ReadPipelineRunLogs 查询项目级 pipeline 单次执行日志。
	ReadPipelineRunLogs(context.Context, string, string, string, url.Values) ([]model.RunLogLine, error)
	// AcquireTestDatabase 申请一组真实的隔离测试资源；approvalToken 为空表示首次请求。
	AcquireTestDatabase(context.Context, string, TestDatabaseAcquireRequest, string) (dbprovision.Lease, error)
	// ReleaseTestDatabase 回收指定租约；重复调用必须保持幂等。
	ReleaseTestDatabase(context.Context, string) error
	// RenewTestDatabase 延长指定租约的 TTL，ttlMinutes 为零表示使用服务端默认值。
	RenewTestDatabase(context.Context, string, TestDatabaseRenewRequest) (dbprovision.Lease, error)
	// ListTestDatabases 列出当前租约；返回值不含明文 DSN。
	ListTestDatabases(context.Context) ([]dbprovision.Lease, error)
}

// AgentError 保留 agent 业务错误中的机器可读 code 和结构化数据。
type AgentError struct {
	Code     string
	Message  string
	Data     any
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
	// tokenSource 提供 Authorization 凭据；nil 表示裸连（测试 fake agent 场景）。
	tokenSource TokenSource
}

const defaultAgentHTTPTimeout = 60 * time.Second

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
		httpClient = &http.Client{Timeout: defaultAgentHTTPTimeout}
	}
	return &HTTPAgentClient{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// NewHTTPAgentClientWithToken 构造带凭据来源的客户端（生产入口 cmd/superdev-mcp 使用）。
func NewHTTPAgentClientWithToken(baseURL string, httpClient *http.Client, source TokenSource) *HTTPAgentClient {
	c := NewHTTPAgentClient(baseURL, httpClient)
	c.tokenSource = source
	return c
}

// applyAuth 把 tokenSource 的当前 token 注入请求头。
// 拿不到凭据不阻断请求：裸发让 agent 返回 401，错误面统一在 do() 出口。
func (c *HTTPAgentClient) applyAuth(req *http.Request) {
	if c.tokenSource == nil {
		return
	}
	token, err := c.tokenSource.Token(req.Context())
	if err != nil {
		log.Printf("[SuperDev] mcp: token source unavailable: %v", err)
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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

// GetDebugCredentials 拉取合并后的调试凭据。
//
// 参数：
//   - ctx: 请求上下文
//   - q: query 参数(project_id/project_name 必填其一,service_id/service_name 可选)
//
// 返回：
//   - 合并后的明文凭据列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) GetDebugCredentials(ctx context.Context, q url.Values) ([]model.MergedDebugCredential, error) {
	var out []model.MergedDebugCredential
	return out, c.get(ctx, withQuery("/api/debug-credentials", q), &out)
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

// ListLanguageRuntimeProviders 返回支持的语言运行 provider。
//
// 参数：
//   - ctx: 请求上下文
//
// 返回：
//   - language provider 标识列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListLanguageRuntimeProviders(ctx context.Context) ([]string, error) {
	var out struct {
		Languages []string `json:"languages"`
	}
	err := c.get(ctx, "/api/language-runtime/providers", &out)
	return out.Languages, err
}

// DescribeLanguageRuntimeSchema 返回某语言的运行配置 schema。
//
// 参数：
//   - ctx: 请求上下文
//   - language: 服务语言标识，例如 go
//
// 返回：
//   - schema JSON 对象
//   - HTTP 或解码错误
func (c *HTTPAgentClient) DescribeLanguageRuntimeSchema(ctx context.Context, language string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/api/language-runtime/"+url.PathEscape(language)+"/schema", &out)
	return out, err
}

// SuggestServiceRuntime 返回候选语言运行配置。
//
// 参数：
//   - ctx: 请求上下文
//   - language: 服务语言标识，例如 go
//   - body: project_root/cwd 等 provider 输入
//
// 返回：
//   - suggestions 响应对象
//   - HTTP 或解码错误
func (c *HTTPAgentClient) SuggestServiceRuntime(ctx context.Context, language string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/api/language-runtime/"+url.PathEscape(language)+"/suggest", body, &out)
	return out, err
}

// ValidateServiceRuntime 校验语言运行配置。
//
// 参数：
//   - ctx: 请求上下文
//   - language: 服务语言标识，例如 go
//   - body: project_root/cwd/env/config 等 provider 输入
//
// 返回：
//   - valid/diagnostics 响应对象
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ValidateServiceRuntime(ctx context.Context, language string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/api/language-runtime/"+url.PathEscape(language)+"/validate", body, &out)
	return out, err
}

// PreviewServiceExecution 返回语言运行执行计划预览。
//
// 参数：
//   - ctx: 请求上下文
//   - language: 服务语言标识，例如 go
//   - body: project_root/cwd/env/config/intent/artifact_dir 等 provider 输入
//
// 返回：
//   - preview/diagnostics 响应对象
//   - HTTP 或解码错误
func (c *HTTPAgentClient) PreviewServiceExecution(ctx context.Context, language string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/api/language-runtime/"+url.PathEscape(language)+"/preview", body, &out)
	return out, err
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

// ListDebugBrowsers 查询本机调试浏览器配置和可用性。
func (c *HTTPAgentClient) ListDebugBrowsers(ctx context.Context) ([]DebugBrowser, error) {
	var out []DebugBrowser
	return out, c.get(ctx, "/api/debug-browsers", &out)
}

// ListBrowserTargets 查询可打开的本机前端调试目标。
func (c *HTTPAgentClient) ListBrowserTargets(ctx context.Context) ([]BrowserTarget, error) {
	var out []BrowserTarget
	return out, c.get(ctx, "/api/browser-targets", &out)
}

// OpenBrowserSession 创建本机前端浏览器调试会话。
func (c *HTTPAgentClient) OpenBrowserSession(ctx context.Context, req OpenBrowserSessionRequest, approvalToken string) (BrowserSession, error) {
	var out BrowserSession
	return out, c.postWithApprovalToken(ctx, "/api/browser-sessions", req, approvalToken, &out)
}

// CloseBrowserSession 关闭由 SuperDev 创建的浏览器调试会话。
func (c *HTTPAgentClient) CloseBrowserSession(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/browser-sessions/"+url.PathEscape(id), nil)
}

// BrowserSnapshot 读取浏览器页面快照。
func (c *HTTPAgentClient) BrowserSnapshot(ctx context.Context, req BrowserSnapshotRequest) (BrowserSnapshot, error) {
	var out BrowserSnapshot
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/snapshot"
	return out, c.post(ctx, path, req, &out)
}

// BrowserClick 点击浏览器页面元素。
func (c *HTTPAgentClient) BrowserClick(ctx context.Context, req BrowserClickRequest) (BrowserActionResult, error) {
	var out BrowserActionResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/click"
	return out, c.post(ctx, path, req, &out)
}

// BrowserType 向浏览器页面元素输入文本。
func (c *HTTPAgentClient) BrowserType(ctx context.Context, req BrowserTypeRequest) (BrowserActionResult, error) {
	var out BrowserActionResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/type"
	return out, c.post(ctx, path, req, &out)
}

// BrowserScreenshot 截取浏览器页面。
func (c *HTTPAgentClient) BrowserScreenshot(ctx context.Context, req BrowserScreenshotRequest) (BrowserScreenshot, error) {
	var out BrowserScreenshot
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/screenshot"
	return out, c.post(ctx, path, req, &out)
}

// BrowserNavigate 对浏览器页面执行同源整页导航。
func (c *HTTPAgentClient) BrowserNavigate(ctx context.Context, req BrowserNavigateRequest) (BrowserNavigationResult, error) {
	var out BrowserNavigationResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/navigate"
	return out, c.post(ctx, path, req, &out)
}

// BrowserReload 刷新浏览器页面。
func (c *HTTPAgentClient) BrowserReload(ctx context.Context, req BrowserReloadRequest) (BrowserNavigationResult, error) {
	var out BrowserNavigationResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/reload"
	return out, c.post(ctx, path, req, &out)
}

// BrowserWaitForSelector 等待浏览器页面 selector。
func (c *HTTPAgentClient) BrowserWaitForSelector(ctx context.Context, req BrowserWaitForSelectorRequest) (BrowserWaitResult, error) {
	var out BrowserWaitResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/wait-for-selector"
	return out, c.post(ctx, path, req, &out)
}

// BrowserPressKey 向浏览器页面发送按键。
func (c *HTTPAgentClient) BrowserPressKey(ctx context.Context, req BrowserPressKeyRequest) (BrowserActionResult, error) {
	var out BrowserActionResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/press-key"
	return out, c.post(ctx, path, req, &out)
}

// BrowserSelectOption 选择浏览器页面 select 选项。
func (c *HTTPAgentClient) BrowserSelectOption(ctx context.Context, req BrowserSelectOptionRequest) (BrowserActionResult, error) {
	var out BrowserActionResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/select-option"
	return out, c.post(ctx, path, req, &out)
}

// BrowserConsoleLogs 读取浏览器页面 console 日志。
func (c *HTTPAgentClient) BrowserConsoleLogs(ctx context.Context, req BrowserConsoleLogsRequest) (BrowserConsoleLogsResult, error) {
	var out BrowserConsoleLogsResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/console-logs"
	return out, c.post(ctx, path, req, &out)
}

// BrowserNetworkRequests 读取浏览器页面网络请求摘要。
func (c *HTTPAgentClient) BrowserNetworkRequests(ctx context.Context, req BrowserNetworkRequestsRequest) (BrowserNetworkRequestsResult, error) {
	var out BrowserNetworkRequestsResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/network-requests"
	return out, c.post(ctx, path, req, &out)
}

// BrowserEvaluate 执行浏览器页面 JavaScript。
func (c *HTTPAgentClient) BrowserEvaluate(ctx context.Context, req BrowserEvaluateRequest) (BrowserEvaluateResult, error) {
	var out BrowserEvaluateResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/evaluate"
	return out, c.post(ctx, path, req, &out)
}

// BrowserSetViewport 更新浏览器页面 viewport 尺寸。
func (c *HTTPAgentClient) BrowserSetViewport(ctx context.Context, req BrowserSetViewportRequest) (BrowserViewportResult, error) {
	var out BrowserViewportResult
	path := "/api/browser-sessions/" + url.PathEscape(req.SessionID) + "/set-viewport"
	return out, c.post(ctx, path, req, &out)
}

// ListCodeDebugTargets 查询可打开的本机代码调试目标。
func (c *HTTPAgentClient) ListCodeDebugTargets(ctx context.Context) ([]CodeDebugTarget, error) {
	var out []CodeDebugTarget
	return out, c.get(ctx, "/api/code-debug-targets", &out)
}

// SetCodeDebugBreakpoints 按 deployment 设置代码调试断点。
func (c *HTTPAgentClient) SetCodeDebugBreakpoints(ctx context.Context, req DebugBreakpointRequest) (map[string]any, error) {
	var out map[string]any
	body := map[string]any{"source": req.Source, "lines": req.Lines}
	return out, c.post(ctx, codeDebugDeploymentPath(req.DeploymentID, "breakpoints"), body, &out)
}

// CodeDebugAction 按 deployment 执行非 evaluate 的代码调试动作。
func (c *HTTPAgentClient) CodeDebugAction(ctx context.Context, deploymentID string, action string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	pathAction, err := codeDebugDeploymentAction(action)
	if err != nil {
		return nil, err
	}
	return out, c.post(ctx, codeDebugDeploymentPath(deploymentID, pathAction), body, &out)
}

// CodeDebugEvaluate 按 deployment 执行代码调试表达式求值。
func (c *HTTPAgentClient) CodeDebugEvaluate(ctx context.Context, req DebugEvaluateRequest, approvalToken string) (map[string]any, error) {
	var out map[string]any
	body := map[string]any{
		"expression": req.Expression,
		"frame_id":   req.FrameID,
		"source":     req.Source,
	}
	return out, c.postWithApprovalToken(ctx, codeDebugDeploymentPath(req.DeploymentID, "evaluate"), body, approvalToken, &out)
}

// CodeDebugCaptureAt 按 deployment 执行 stop-at-line 复合采集。
func (c *HTTPAgentClient) CodeDebugCaptureAt(ctx context.Context, req DebugCaptureAtRequest, approvalToken string) (map[string]any, error) {
	body := map[string]any{
		"source":         req.Source,
		"line":           req.Line,
		"thread_id":      req.ThreadID,
		"timeout_ms":     req.TimeoutMilliseconds,
		"max_variables":  req.MaxVariables,
		"variable_names": req.VariableNames,
	}
	var out map[string]any
	return out, c.postWithApprovalToken(ctx, codeDebugDeploymentPath(req.DeploymentID, "capture"), body, approvalToken, &out)
}

// CodeDebugInspect 按 deployment 执行已暂停现场复合读取。
func (c *HTTPAgentClient) CodeDebugInspect(ctx context.Context, req DebugInspectRequest) (map[string]any, error) {
	var out map[string]any
	body := map[string]any{
		"thread_id":      req.ThreadID,
		"frame_id":       req.FrameID,
		"max_variables":  req.MaxVariables,
		"variable_names": req.VariableNames,
	}
	return out, c.post(ctx, codeDebugDeploymentPath(req.DeploymentID, "inspect"), body, &out)
}

func codeDebugDeploymentPath(deploymentID, action string) string {
	return "/api/deployments/" + url.PathEscape(deploymentID) + "/debug/" + strings.Trim(action, "/")
}

func codeDebugDeploymentAction(action string) (string, error) {
	switch strings.TrimSpace(action) {
	case "continue":
		return "continue-thread", nil
	case "pause":
		return "pause", nil
	case "step_over":
		return "step-over", nil
	case "step_in":
		return "step-in", nil
	case "step_out":
		return "step-out", nil
	case "stack":
		return "stack", nil
	case "scopes":
		return "scopes", nil
	case "variables":
		return "variables", nil
	default:
		return "", fmt.Errorf("unsupported code debug action %q", action)
	}
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

// AcquireTestDatabase 请求 agent 创建真实的临时测试资源。
//
// 参数：
//   - ctx: 请求上下文
//   - projectID: 项目 ID
//   - req: purpose、TTL 与资源类型
//   - approvalToken: 审批通过后的一次性 token，可为空
//
// 返回：
//   - 含明文 DSN 的租约；这是 MCP 明文凭据出口
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) AcquireTestDatabase(ctx context.Context, projectID string, req TestDatabaseAcquireRequest, approvalToken string) (dbprovision.Lease, error) {
	var out dbprovision.Lease
	path := "/api/projects/" + url.PathEscape(projectID) + "/test-database/acquire"
	return out, c.postWithApprovalToken(ctx, path, req, approvalToken, &out)
}

// ReleaseTestDatabase 请求 agent 回收一个临时测试租约。
//
// 参数：
//   - ctx: 请求上下文
//   - leaseID: 租约 ID
//
// 返回：
//   - HTTP 或 agent 业务错误；租约不存在/已释放由 agent 保持幂等
func (c *HTTPAgentClient) ReleaseTestDatabase(ctx context.Context, leaseID string) error {
	return c.delete(ctx, "/api/test-databases/"+url.PathEscape(leaseID), nil)
}

// RenewTestDatabase 请求 agent 延长一个临时测试租约。
//
// 参数：
//   - ctx: 请求上下文
//   - leaseID: 租约 ID
//   - req: 续租时长请求
//
// 返回：
//   - 更新后的租约（不含 DSN）
//   - HTTP 或 agent 业务错误
func (c *HTTPAgentClient) RenewTestDatabase(ctx context.Context, leaseID string, req TestDatabaseRenewRequest) (dbprovision.Lease, error) {
	var out dbprovision.Lease
	path := "/api/test-databases/" + url.PathEscape(leaseID) + "/renew"
	return out, c.post(ctx, path, req, &out)
}

// ListTestDatabases 请求 agent 列出临时测试租约。
//
// 参数：
//   - ctx: 请求上下文
//
// 返回：
//   - 已脱敏租约列表
//   - HTTP 或解码错误
func (c *HTTPAgentClient) ListTestDatabases(ctx context.Context) ([]dbprovision.Lease, error) {
	var out []dbprovision.Lease
	return out, c.get(ctx, "/api/test-databases", &out)
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

func (c *HTTPAgentClient) delete(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
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
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent unavailable: %w", err)
	}
	// 401 且有凭据来源：token 可能因 agent 重启轮换而过期——失效缓存重取一次。
	// 只重试一次：重取后仍 401 说明凭据真的无效，交给统一错误面。
	if resp.StatusCode == http.StatusUnauthorized && c.tokenSource != nil {
		resp.Body.Close()
		c.tokenSource.Invalidate()
		retry, cloneErr := cloneRequestForRetry(req)
		if cloneErr != nil {
			return fmt.Errorf("agent error: agent token required (retry setup failed: %v)", cloneErr)
		}
		c.applyAuth(retry)
		resp, err = c.http.Do(retry)
		if err != nil {
			return fmt.Errorf("agent unavailable: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if isTLSRequiredResponse(resp.StatusCode, raw) {
			// 目标 agent 是纯 TLS 监听（未启用 loopback 明文豁免的旧版本，或远端
			// TLS agent 被误配了 http:// 地址）。不识别这个形态时错误面是一条
			// 不可读的 "400 Bad Request"，在这里换成可执行的指引。
			log.Printf("[SuperDev] mcp: 明文请求被 %s 的 TLS 监听器拒绝，需改用 https:// 或升级 agent", req.URL.Host)
			return fmt.Errorf("agent at %s requires TLS and rejected plaintext HTTP; set SUPERDEV_AGENT_URL to https:// (or upgrade the agent to enable the loopback plaintext exemption)", req.URL.Host)
		}
		var body struct {
			Code     string            `json:"code"`
			Error    string            `json:"error"`
			Data     any               `json:"data"`
			Plan     OperationPlan     `json:"plan"`
			Approval OperationApproval `json:"approval"`
		}
		_ = json.Unmarshal(raw, &body)
		if body.Error == "" {
			body.Error = resp.Status
		}
		if body.Code != "" {
			return AgentError{Code: body.Code, Message: body.Error, Data: body.Data, Plan: body.Plan, Approval: body.Approval}
		}
		return fmt.Errorf("agent error: %s", body.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// cloneRequestForRetry 复制请求用于一次重试。
// http.NewRequestWithContext + bytes.Reader 会自动设置 GetBody，POST 体可重放；
// GET/DELETE 无体直接 Clone。
func cloneRequestForRetry(req *http.Request) (*http.Request, error) {
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	return retry, nil
}

func withQuery(path string, q url.Values) string {
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}
