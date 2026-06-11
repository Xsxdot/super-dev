// Package operation 提供 MCP 写操作的本机安全门禁模型。
//
// 职责：
//   - 定义 operation plan、approval、audit 的稳定数据结构
//   - 定义风险等级、操作类型和审批状态常量
//   - 提供 fingerprint 与 token 的基础辅助能力
//
// 边界：
//   - 不执行进程、不导入模板、不读写项目配置
//   - 不直接暴露给 MCP，MCP 只能通过 agent HTTP API 使用
package operation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// OperationRuntimeStart 表示启动 deployment 的写操作。
	OperationRuntimeStart = "runtime.start"
	// OperationRuntimeStop 表示停止 deployment 的写操作。
	OperationRuntimeStop = "runtime.stop"
	// OperationRuntimeRestart 表示重启 deployment 的写操作。
	OperationRuntimeRestart = "runtime.restart"
	// OperationRuntimeStartSelected 表示启动项目某环境下已选 deployment 的批量写操作。
	OperationRuntimeStartSelected = "runtime.start_selected"
	// OperationBrowserDebugOpen 表示打开本机浏览器调试会话。
	OperationBrowserDebugOpen = "browser_debug.open"
	// OperationTemplateImport 表示导入流水线模板的写操作。
	OperationTemplateImport = "template.import"
	// OperationConfigProjectUpsert 表示新增或编辑项目基础配置。
	OperationConfigProjectUpsert = "config.project.upsert"
	// OperationConfigServiceUpsert 表示新增或编辑 service 配置。
	OperationConfigServiceUpsert = "config.service.upsert"
	// OperationConfigPipelineUpsert 表示新增或编辑项目级流水线。
	OperationConfigPipelineUpsert = "config.pipeline.upsert"
	// OperationPipelineRun 表示运行项目级流水线（部署或回滚）的写操作。
	OperationPipelineRun = "pipeline.run"

	// RiskLow 表示仅影响开发环境本机目标的低风险操作。
	RiskLow = "low"
	// RiskMedium 表示需要用户确认但通常不直接影响运行态的操作。
	RiskMedium = "medium"
	// RiskHigh 表示可能影响非开发环境运行态的操作。
	RiskHigh = "high"
	// RiskCritical 表示策略禁止执行的高危操作。
	RiskCritical = "critical"

	// ApprovalPending 表示审批请求等待用户决策。
	ApprovalPending = "pending"
	// ApprovalApproved 表示审批请求已批准且可发放 token。
	ApprovalApproved = "approved"
	// ApprovalRejected 表示审批请求已拒绝。
	ApprovalRejected = "rejected"
	// ApprovalExpired 表示审批请求已过期。
	ApprovalExpired = "expired"
	// ApprovalUsed 表示审批 token 已被一次性消费。
	ApprovalUsed = "used"

	// AuditApprovalRequired 记录一次操作需要审批。
	AuditApprovalRequired = "approval_required"
	// AuditApproved 记录一次审批批准。
	AuditApproved = "approved"
	// AuditRejected 记录一次审批拒绝。
	AuditRejected = "rejected"
	// AuditExecuted 记录一次操作已执行。
	AuditExecuted = "executed"
	// AuditFailed 记录一次操作执行失败。
	AuditFailed = "failed"
	// AuditApprovedByGrace 记录一次因项目豁免窗口命中而放行的操作。
	AuditApprovedByGrace = "approved_by_grace"
	// AuditGraceGranted 记录一次项目豁免窗口的开启。
	AuditGraceGranted = "grace_granted"

	// DefaultPlanTTL 是 operation plan 的默认有效期。
	DefaultPlanTTL = 10 * time.Minute
	// DefaultApprovalTTL 是审批请求的默认有效期。
	DefaultApprovalTTL = 10 * time.Minute
	// DefaultTokenTTL 是审批 token 的默认有效期。
	DefaultTokenTTL = 5 * time.Minute
)

var (
	// ErrInvalidOperation 表示操作类型或预检输入不合法。
	ErrInvalidOperation = errors.New("invalid operation")
	// ErrApprovalNotFound 表示找不到审批请求。
	ErrApprovalNotFound = errors.New("operation approval not found")
	// ErrApprovalRejected 表示审批请求已被拒绝。
	ErrApprovalRejected = errors.New("operation approval rejected")
	// ErrApprovalExpired 表示审批或 token 已过期。
	ErrApprovalExpired = errors.New("operation approval expired")
	// ErrApprovalTokenInvalid 表示审批 token 与目标操作不匹配。
	ErrApprovalTokenInvalid = errors.New("operation approval token is invalid")
	// ErrApprovalTokenConsumed 表示审批 token 已经被使用。
	ErrApprovalTokenConsumed = errors.New("operation approval token is already used")
)

// Target 描述一次 operation 解析后的稳定目标。
type Target struct {
	ProjectID      string `json:"project_id,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	EnvName        string `json:"env_name,omitempty"`
	ServiceID      string `json:"service_id,omitempty"`
	ServiceName    string `json:"service_name,omitempty"`
	DeploymentID   string `json:"deployment_id,omitempty"`
	HostID         string `json:"host_id,omitempty"`
	TemplatePath   string `json:"template_path,omitempty"`
	TemplateDigest string `json:"template_digest,omitempty"`
	PipelineID     string `json:"pipeline_id,omitempty"`
}

// Check 描述预检中的一个可解释检查项。
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Plan 是 agent 对一次写操作的稳定预检结果。
type Plan struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	Target           Target    `json:"target"`
	TargetSummary    string    `json:"target_summary"`
	RiskLevel        string    `json:"risk_level"`
	RequiresApproval bool      `json:"requires_approval"`
	Denied           bool      `json:"denied"`
	Reasons          []string  `json:"reasons,omitempty"`
	ExpectedEffects  []string  `json:"expected_effects,omitempty"`
	Checks           []Check   `json:"checks,omitempty"`
	Fingerprint      string    `json:"fingerprint"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// TemplateSummary 是 template import plan 中需要记录的模板摘要。
type TemplateSummary struct {
	Source  string `json:"source"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// TemplateImportRequest 描述模板导入预检所需的稳定输入。
type TemplateImportRequest struct {
	Path    string
	Digest  string
	Summary TemplateSummary
}

// Approval 记录一条需要用户决策的 operation 请求。
type Approval struct {
	ID             string     `json:"id"`
	Plan           Plan       `json:"plan"`
	Status         string     `json:"status"`
	RequestedBy    string     `json:"requested_by"`
	RequesterLabel string     `json:"requester_label"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecisionNote   string     `json:"decision_note,omitempty"`
	TokenHash      string     `json:"token_hash,omitempty"`
	TokenIssuedAt  *time.Time `json:"token_issued_at,omitempty"`
	TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`
}

// AuditEvent 记录 operation 安全链路中的事实事件。
type AuditEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Action     string         `json:"action"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Plan       Plan           `json:"plan"`
	Summary    string         `json:"summary"`
	Data       map[string]any `json:"data,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// GraceGrant 记录一次项目级审批豁免授权。
//
// 注意：
//   - 同项目重复授权会续期，不堆叠
//   - 仅对带 ProjectID 的 plan 生效
type GraceGrant struct {
	ProjectID   string    `json:"project_id"`
	GrantedBy   string    `json:"granted_by"`
	GrantedFrom string    `json:"granted_from"` // 触发豁免的 approval ID，便于审计追溯
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// ApprovalFilter 描述审批列表过滤条件。
type ApprovalFilter struct {
	Status    string
	ProjectID string
	Limit     int
}

// AuditFilter 描述审计查询过滤条件。
type AuditFilter struct {
	ProjectID  string
	Kind       string
	ApprovalID string
	Since      time.Time
	Limit      int
}

func newID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("generate operation id: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("generate approval token: %v", err))
	}
	return hex.EncodeToString(b[:])
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stableFingerprint(parts any) string {
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(fmt.Sprintf("fingerprint operation: %v", err))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func trim(s string) string {
	return strings.TrimSpace(s)
}
