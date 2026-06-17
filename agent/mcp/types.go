// types.go 定义 MCP 包内部使用的 agent DTO。
//
// 职责：
//   - 补充 agent API 中未导出的响应类型
//   - 保持 MCP 输出与 HTTP API 解码解耦
//
// 边界：
//   - 已存在于 model 包的领域结构不重复定义
package mcp

import (
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

// LogsResponse 是 deployment 日志分页接口响应。
type LogsResponse struct {
	Items []model.LogEntry `json:"items"`
	Next  struct {
		Time string `json:"time,omitempty"`
		// ID 必须与 API 端 deploymentLogsResponse.Next.ID 一致为 string：
		// 游标 ID 源头是 logbackend.Cursor.ID(string)，此前误标 int64 导致
		// "cannot unmarshal string into Go struct field .next.id of type int64"。
		ID string `json:"id,omitempty"`
	} `json:"next"`
}

// LogSearchResponse 是项目/服务范围日志搜索接口响应。
type LogSearchResponse struct {
	Query            string           `json:"query"`
	Total            int              `json:"total"`
	Items            []model.LogEntry `json:"items"`
	DeploymentCounts map[string]int   `json:"deployment_counts"`
	HasMore          bool             `json:"has_more"`
}

// LogContextResponse 是围绕某条日志拉取跨服务上下文的接口响应。
type LogContextResponse struct {
	TargetID          int64                       `json:"target_id"`
	AnchorTime        time.Time                   `json:"anchor_time"`
	ItemsByDeployment map[string][]model.LogEntry `json:"items_by_deployment"`
}

// HostReference 是 MCP 暴露给 AI 选择远程主机的安全视图。
//
// 注意：
//   - ID 是配置 deployment.host_ids 时唯一可使用的稳定标识
//   - Name 仅用于展示和人工识别，不允许写入 host_ids
//   - 不包含 SSH 密码、私钥等敏感字段
type HostReference struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	PublicIP  string   `json:"public_ip,omitempty"`
	PrivateIP string   `json:"private_ip,omitempty"`
	Tags      []string `json:"tags"`
	IsSelf    bool     `json:"is_self"`
	NodeID    string   `json:"node_id,omitempty"`
}

// PipelineTemplateSummary 是模板导入接口返回的模板摘要。
type PipelineTemplateSummary struct {
	Source      string                            `json:"source"`
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	Category    string                            `json:"category"`
	Version     string                            `json:"version"`
	Digest      string                            `json:"digest"`
	Description string                            `json:"description,omitempty"`
	Inputs      map[string]pipelinetemplate.Input `json:"inputs,omitempty"`
}

// PipelineTemplatePreview 是模板 dry-run preview 结果。
type PipelineTemplatePreview = pipelinetemplate.PreviewResult

// PipelineDeployRequest 描述项目级 pipeline 部署或回滚请求。
type PipelineDeployRequest struct {
	ProjectID           string            `json:"project_id,omitempty"`
	ProjectName         string            `json:"project_name,omitempty"`
	PipelineID          string            `json:"pipeline_id"`
	EnvName             string            `json:"env_name"`
	HostIDs             []string          `json:"host_ids,omitempty"`
	ArtifactVersion     string            `json:"artifact_version,omitempty"`
	Variables           map[string]string `json:"variables,omitempty"`
	ApprovalToken       string            `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int              `json:"approval_wait_seconds,omitempty"`
	DebugSessionID      string            `json:"debug_session_id,omitempty"`
}

// ProjectPipelinePreviewRequest 描述项目级 pipeline 校验请求。
type ProjectPipelinePreviewRequest struct {
	EnvName      string            `json:"env_name"`
	ServiceNames []string          `json:"service_names,omitempty"`
	Variables    map[string]string `json:"variables,omitempty"`
}

// ProjectPipelinePreview 是 agent 项目级 pipeline preview 响应。
type ProjectPipelinePreview struct {
	Plan pipeline.Plan `json:"plan"`
	Run  model.Run     `json:"run"`
}

// DebugBrowser 描述 MCP 可展示的本机调试浏览器。
type DebugBrowser struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ExecutablePath string `json:"executable_path"`
	Available      bool   `json:"available"`
}

// BrowserTarget 描述 MCP 可打开的本机前端调试目标。
type BrowserTarget struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	ServiceID    string `json:"service_id"`
	ServiceName  string `json:"service_name"`
	DeploymentID string `json:"deployment_id"`
	EnvName      string `json:"env_name"`
	BaseURL      string `json:"base_url"`
	DefaultPath  string `json:"default_path"`
}

// BrowserSession 描述由 SuperDev 创建的浏览器调试会话。
type BrowserSession struct {
	ID           string `json:"session_id"`
	DeploymentID string `json:"deployment_id"`
	TargetURL    string `json:"target_url"`
	BrowserID    string `json:"browser_id"`
	DebugPort    int    `json:"debug_port"`
	BrowserWS    string `json:"browser_ws"`
	PageWS       string `json:"page_ws"`
	DevtoolsURL  string `json:"devtools_url"`
}

// BrowserSnapshot 描述 AI 可读的页面状态快照。
type BrowserSnapshot struct {
	SessionID string                   `json:"session_id"`
	URL       string                   `json:"url"`
	Title     string                   `json:"title"`
	Text      string                   `json:"text"`
	Elements  []BrowserSnapshotElement `json:"elements,omitempty"`
	Focused   *BrowserSnapshotElement  `json:"focused,omitempty"`
}

// BrowserSnapshotElement 描述页面中 AI 可操作的元素摘要。
type BrowserSnapshotElement struct {
	Role     string                 `json:"role"`
	Name     string                 `json:"name,omitempty"`
	Selector string                 `json:"selector"`
	Visible  bool                   `json:"visible"`
	Enabled  bool                   `json:"enabled"`
	Bounds   *BrowserSnapshotBounds `json:"bounds,omitempty"`
}

// BrowserSnapshotBounds 描述页面元素在 viewport 中的位置和尺寸。
type BrowserSnapshotBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// BrowserActionResult 描述无额外负载的浏览器控制动作结果。
type BrowserActionResult struct {
	SessionID string `json:"session_id"`
	OK        bool   `json:"ok"`
}

// BrowserScreenshot 描述页面截图结果。
type BrowserScreenshot struct {
	SessionID  string `json:"session_id"`
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

// BrowserEvaluateResult 描述页面脚本执行结果。
type BrowserEvaluateResult struct {
	SessionID string `json:"session_id"`
	Result    any    `json:"result"`
}

// BrowserViewportResult 描述页面 viewport 更新后的尺寸。
type BrowserViewportResult struct {
	SessionID string `json:"session_id"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// CodeDebugTarget 描述可由 AI 打开的本机代码调试目标。
type CodeDebugTarget struct {
	ProjectID         string `json:"project_id"`
	ProjectName       string `json:"project_name"`
	RootPath          string `json:"root_path"`
	ServiceID         string `json:"service_id"`
	ServiceName       string `json:"service_name"`
	DeploymentID      string `json:"deployment_id"`
	EnvName           string `json:"env_name"`
	Language          string `json:"language,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Experimental      bool   `json:"experimental,omitempty"`
	Command           string `json:"command,omitempty"`
	WorkDir           string `json:"work_dir,omitempty"`
	RuntimeState      string `json:"runtime_state,omitempty"`
	LeaseActive       bool   `json:"lease_active,omitempty"`
	CanOpen           bool   `json:"can_open"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// DebugBreakpointRequest 描述代码调试断点设置请求。
type DebugBreakpointRequest struct {
	DeploymentID string `json:"deployment_id,omitempty"`
	Source       string `json:"source"`
	Lines        []int  `json:"lines"`
}

// DebugEvaluateRequest 描述代码调试表达式求值请求。
type DebugEvaluateRequest struct {
	DeploymentID        string `json:"deployment_id,omitempty"`
	Expression          string `json:"expression"`
	FrameID             int    `json:"frame_id,omitempty"`
	Source              string `json:"source,omitempty"`
	ApprovalToken       string `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int   `json:"approval_wait_seconds,omitempty"`
}

// DebugCaptureAtRequest 描述 stop-at-line 并采集现场的复合请求。
type DebugCaptureAtRequest struct {
	DeploymentID        string   `json:"deployment_id,omitempty"`
	Source              string   `json:"source"`
	Line                int      `json:"line"`
	ThreadID            int      `json:"thread_id,omitempty"`
	TimeoutMilliseconds int      `json:"timeout_ms,omitempty"`
	MaxVariables        int      `json:"max_variables,omitempty"`
	VariableNames       []string `json:"variable_names,omitempty"`
	ApprovalToken       string   `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int     `json:"approval_wait_seconds,omitempty"`
}

// DebugInspectRequest 描述读取已暂停 deployment debug runtime 现场的复合请求。
type DebugInspectRequest struct {
	DeploymentID  string   `json:"deployment_id,omitempty"`
	ThreadID      int      `json:"thread_id,omitempty"`
	FrameID       int      `json:"frame_id,omitempty"`
	MaxVariables  int      `json:"max_variables,omitempty"`
	VariableNames []string `json:"variable_names,omitempty"`
}

// BrowserSnapshotRequest 描述页面快照请求。
type BrowserSnapshotRequest struct {
	SessionID   string `json:"session_id"`
	Selector    string `json:"selector,omitempty"`
	MaxText     int    `json:"max_text,omitempty"`
	MaxElements int    `json:"max_elements,omitempty"`
}

// BrowserClickRequest 描述页面点击请求。
type BrowserClickRequest struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
}

// BrowserTypeRequest 描述页面输入请求。
type BrowserTypeRequest struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	Text      string `json:"text"`
	Fill      bool   `json:"fill,omitempty"`
}

// BrowserScreenshotRequest 描述页面截图请求。
type BrowserScreenshotRequest struct {
	SessionID string `json:"session_id"`
	FullPage  bool   `json:"full_page,omitempty"`
}

// BrowserNavigateRequest 描述页面同源整页导航请求。
type BrowserNavigateRequest struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url,omitempty"`
	Path      string `json:"path,omitempty"`
	WaitUntil string `json:"wait_until,omitempty"`
}

// BrowserReloadRequest 描述页面刷新请求。
type BrowserReloadRequest struct {
	SessionID string `json:"session_id"`
	WaitUntil string `json:"wait_until,omitempty"`
}

// BrowserNavigationResult 描述页面导航后的状态。
type BrowserNavigationResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
}

// BrowserWaitForSelectorRequest 描述等待页面 selector 的请求。
type BrowserWaitForSelectorRequest struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	State     string `json:"state,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// BrowserWaitResult 描述等待页面 selector 的结果。
type BrowserWaitResult struct {
	SessionID string `json:"session_id"`
	Matched   bool   `json:"matched"`
	Text      string `json:"text,omitempty"`
}

// BrowserPressKeyRequest 描述键盘按键请求。
type BrowserPressKeyRequest struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector,omitempty"`
	Key       string `json:"key"`
}

// BrowserSelectOptionRequest 描述 select 选项选择请求。
type BrowserSelectOptionRequest struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	Value     string `json:"value,omitempty"`
	Label     string `json:"label,omitempty"`
}

// BrowserConsoleLog 描述浏览器 console 日志摘要。
type BrowserConsoleLog struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// BrowserNetworkRequest 描述浏览器网络请求摘要。
type BrowserNetworkRequest struct {
	Time   time.Time `json:"time"`
	Method string    `json:"method"`
	URL    string    `json:"url"`
	Status int       `json:"status,omitempty"`
	Error  string    `json:"error,omitempty"`
}

// BrowserConsoleLogsRequest 描述读取 console 日志请求。
type BrowserConsoleLogsRequest struct {
	SessionID string `json:"session_id"`
	Level     string `json:"level,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// BrowserConsoleLogsResult 描述 console 日志读取结果。
type BrowserConsoleLogsResult struct {
	SessionID string              `json:"session_id"`
	Logs      []BrowserConsoleLog `json:"logs"`
}

// BrowserNetworkRequestsRequest 描述读取网络请求摘要请求。
type BrowserNetworkRequestsRequest struct {
	SessionID string `json:"session_id"`
	Filter    string `json:"filter,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// BrowserNetworkRequestsResult 描述网络请求摘要读取结果。
type BrowserNetworkRequestsResult struct {
	SessionID string                  `json:"session_id"`
	Requests  []BrowserNetworkRequest `json:"requests"`
}

// BrowserEvaluateRequest 描述页面 JavaScript 执行请求。
type BrowserEvaluateRequest struct {
	SessionID  string `json:"session_id"`
	Expression string `json:"expression"`
}

// BrowserSetViewportRequest 描述页面 viewport 尺寸更新请求。
type BrowserSetViewportRequest struct {
	SessionID string `json:"session_id"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// OpenBrowserSessionRequest 描述打开浏览器调试会话的 MCP 请求。
type OpenBrowserSessionRequest struct {
	DeploymentID        string `json:"deployment_id"`
	BrowserID           string `json:"browser_id,omitempty"`
	Path                string `json:"path,omitempty"`
	OpenDevtools        *bool  `json:"open_devtools,omitempty"`
	ViewportWidth       int    `json:"viewport_width,omitempty"`
	ViewportHeight      int    `json:"viewport_height,omitempty"`
	ApprovalToken       string `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int   `json:"approval_wait_seconds,omitempty"`
	DebugSessionID      string `json:"debug_session_id,omitempty"`
}

// OperationRequest 描述 MCP 请求 agent 生成 operation plan 的参数。
type OperationRequest struct {
	Kind         string `json:"kind"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	EnvName      string `json:"env_name,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Path         string `json:"path,omitempty"`
	TemplatePath string `json:"template_path,omitempty"`
}

// OperationTarget 描述 operation plan 中绑定的稳定目标。
type OperationTarget struct {
	ProjectID      string `json:"project_id,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	EnvName        string `json:"env_name,omitempty"`
	ServiceID      string `json:"service_id,omitempty"`
	ServiceName    string `json:"service_name,omitempty"`
	DeploymentID   string `json:"deployment_id,omitempty"`
	TemplatePath   string `json:"template_path,omitempty"`
	TemplateDigest string `json:"template_digest,omitempty"`
	PipelineID     string `json:"pipeline_id,omitempty"`
}

// OperationCheck 描述 operation preflight 中的一条检查项。
type OperationCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// OperationPlan 是 agent 返回给 MCP 的写操作预检结果。
type OperationPlan struct {
	ID               string           `json:"id"`
	Kind             string           `json:"kind"`
	Target           OperationTarget  `json:"target"`
	TargetSummary    string           `json:"target_summary"`
	RiskLevel        string           `json:"risk_level"`
	RequiresApproval bool             `json:"requires_approval"`
	Denied           bool             `json:"denied"`
	Reasons          []string         `json:"reasons,omitempty"`
	ExpectedEffects  []string         `json:"expected_effects,omitempty"`
	Checks           []OperationCheck `json:"checks,omitempty"`
	Fingerprint      string           `json:"fingerprint"`
	CreatedAt        time.Time        `json:"created_at"`
	ExpiresAt        time.Time        `json:"expires_at"`
}

// OperationApproval 是 agent 返回给 MCP 的审批请求摘要。
type OperationApproval struct {
	ID             string        `json:"id"`
	Plan           OperationPlan `json:"plan"`
	Status         string        `json:"status"`
	RequestedBy    string        `json:"requested_by"`
	RequesterLabel string        `json:"requester_label"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	ExpiresAt      time.Time     `json:"expires_at"`
	DecidedBy      string        `json:"decided_by,omitempty"`
	DecisionNote   string        `json:"decision_note,omitempty"`
}

// OperationApprovalDetail 是读取单条审批时的详情和一次性 token。
type OperationApprovalDetail struct {
	Approval      OperationApproval `json:"approval"`
	ApprovalToken string            `json:"approval_token,omitempty"`
}

// OperationAuditEvent 是 operation 安全链路中的一条审计事实。
type OperationAuditEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Action     string         `json:"action"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Plan       OperationPlan  `json:"plan"`
	Summary    string         `json:"summary"`
	Data       map[string]any `json:"data,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// OperationAuditList 是 operation 审计查询响应。
type OperationAuditList struct {
	Events []OperationAuditEvent `json:"events"`
	Count  int                   `json:"count"`
}

// ConfigChangeRequest 描述 MCP 侧配置 upsert 请求。
type ConfigChangeRequest struct {
	Kind                string         `json:"kind"`
	ProjectID           string         `json:"project_id,omitempty"`
	ProjectName         string         `json:"project_name,omitempty"`
	RootPath            string         `json:"root_path,omitempty"`
	ApprovalToken       string         `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int           `json:"approval_wait_seconds,omitempty"`
	DebugSessionID      string         `json:"debug_session_id,omitempty"`
	Project             map[string]any `json:"project,omitempty"`
	Service             map[string]any `json:"service,omitempty"`
	Pipeline            map[string]any `json:"pipeline,omitempty"`
	Delete              bool           `json:"delete,omitempty"`
	Remove              bool           `json:"remove,omitempty"`
}

// ConfigChangeValidation 描述 config change preview 的校验结果。
type ConfigChangeValidation struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ConfigChangeDiffEntry 描述配置变更中的一处差异。
type ConfigChangeDiffEntry struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// ConfigChangePreview 是 agent 返回给 MCP 的配置变更预览。
type ConfigChangePreview struct {
	ChangeID      string                  `json:"change_id"`
	Kind          string                  `json:"kind"`
	TargetSummary string                  `json:"target_summary"`
	Diff          []ConfigChangeDiffEntry `json:"diff"`
	Validation    ConfigChangeValidation  `json:"validation"`
	Plan          OperationPlan           `json:"plan"`
	Project       model.Project           `json:"project,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
}

// DebugSession 是 MCP 侧使用的本机排障会话 DTO。
type DebugSession struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	ProjectName  string     `json:"project_name"`
	EnvName      string     `json:"env_name,omitempty"`
	ServiceID    string     `json:"service_id,omitempty"`
	ServiceName  string     `json:"service_name,omitempty"`
	DeploymentID string     `json:"deployment_id,omitempty"`
	Title        string     `json:"title"`
	Question     string     `json:"question"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// DebugSessionEvent 是 MCP 侧使用的排障会话事件 DTO。
type DebugSessionEvent struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Summary   string         `json:"summary"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// DebugSessionCreateRequest 描述 MCP 创建排障会话的请求。
type DebugSessionCreateRequest struct {
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	EnvName      string `json:"env_name,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Title        string `json:"title"`
	Question     string `json:"question"`
}

// DebugSessionAppendEventRequest 描述 MCP 追加排障会话事件的请求。
type DebugSessionAppendEventRequest struct {
	Type    string         `json:"type"`
	Actor   string         `json:"actor"`
	Summary string         `json:"summary"`
	Data    map[string]any `json:"data,omitempty"`
}

// DebugSessionCreateResponse 是创建排障会话后的响应。
type DebugSessionCreateResponse struct {
	Session DebugSession      `json:"session"`
	Event   DebugSessionEvent `json:"event"`
}

// DebugSessionDetailResponse 是读取排障会话详情后的响应。
type DebugSessionDetailResponse struct {
	Session   DebugSession        `json:"session"`
	Events    []DebugSessionEvent `json:"events"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
}
