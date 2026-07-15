// Package runtimevalidation 实现跨平台语言运行时与 live MCP 工具的严格验收合同。
//
// 职责：
//   - 定义 campaign、scenario、evidence、coverage、cleanup 与 verdict 的稳定模型
//   - 为 Darwin、Linux、Windows 的 target-native runner 提供共享语义
//   - 保持 package verification 与原生 target PASS 两类结论互不替代
//
// 边界：
//   - 不复用或迁移旧 windowsvalidation campaign 的状态
//   - 不提供通用工作流、灾难恢复或生产 Agent recovery mode
//   - 不允许 mock、策略拒绝或交叉编译结果冒充 strict PASS
package runtimevalidation

import "encoding/json"

const (
	// ScenarioSchemaVersion 是 runtime validation 场景当前唯一接受的 schema 版本。
	ScenarioSchemaVersion = 1
	// ScenarioKind 标识 runtime validation 场景文件。
	ScenarioKind = "superdev.runtime-validation.scenario"
	// CoveragePrimary 表示某一步是该 MCP 工具唯一的主成功证据。
	CoveragePrimary = "primary"
	// CoverageSupporting 表示某一步只负责准备、复查或清理，不自动提升为主证据。
	CoverageSupporting = "supporting"
	// ExpectedOutcomeSuccess 是 strict primary 唯一接受的结果合同。
	ExpectedOutcomeSuccess = "success"
)

// Status 是 strict 验收单元和总 verdict 使用的稳定状态。
type Status string

const (
	// StatusPass 表示该验收面全部硬条件满足。
	StatusPass Status = "PASS"
	// StatusFail 表示产品、协议、断言、完整性或本次 cleanup 存在缺陷。
	StatusFail Status = "FAIL"
	// StatusBlocked 表示外部依赖、宿主或 foundation 条件阻止了安全执行。
	StatusBlocked Status = "BLOCKED"
	// StatusNotRun 表示具名上游 FAIL/BLOCKED 后该验收面未安全执行。
	StatusNotRun Status = "NOT_RUN"
)

// Cause 描述稳定根因代码、可读消息和来源验收面。
type Cause struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}

// CheckResult 是一个 strict 硬门槛的派生结果。
type CheckResult struct {
	ID       string `json:"id"`
	Status   Status `json:"status"`
	Upstream string `json:"upstream,omitempty"`
	Cause    Cause  `json:"cause,omitempty"`
}

// Scenario 是一个 runtime validation MCP 能力场景。
type Scenario struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Requires      []string       `json:"requires,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
	Steps         []ScenarioStep `json:"steps"`
	Cleanup       []ScenarioStep `json:"cleanup,omitempty"`
}

// ScenarioStep 描述一个真实 MCP tools/call 及其证据断言。
type ScenarioStep struct {
	ID        string            `json:"id"`
	Tool      string            `json:"tool"`
	Coverage  string            `json:"coverage"`
	Arguments map[string]any    `json:"arguments,omitempty"`
	Expect    StepExpectation   `json:"expect"`
	Evidence  EvidenceContract  `json:"evidence,omitempty"`
	Capture   map[string]string `json:"capture,omitempty"`
	Poll      *PollContract     `json:"poll,omitempty"`
	RunIf     string            `json:"run_if,omitempty"`
}

// StepExpectation 声明 strict step 必须满足的成功结果与业务断言。
type StepExpectation struct {
	Outcome           string      `json:"outcome"`
	Assertions        []Assertion `json:"assertions"`
	AllowedErrorCodes []string    `json:"allowed_error_codes,omitempty"`
}

// Assertion 对 tools/call 完整结果中的稳定业务字段执行断言。
type Assertion struct {
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
	Variable string `json:"variable,omitempty"`
}

// EvidenceContract 声明持久证据允许记录、需要脱敏和禁止出现的字段。
type EvidenceContract struct {
	Record []string `json:"record,omitempty"`
	Redact []string `json:"redact,omitempty"`
	Forbid []string `json:"forbid,omitempty"`
}

// PollContract 声明只读工具等待真实终态的有界轮询合同。
type PollContract struct {
	IntervalMilliseconds int `json:"interval_ms"`
	TimeoutMilliseconds  int `json:"timeout_ms"`
}

// CoverageAssignment 是一个 live tool 到 scenario primary step 的唯一归属。
type CoverageAssignment struct {
	Tool       string `json:"tool"`
	ScenarioID string `json:"scenario_id"`
	StepID     string `json:"step_id"`
}

// CoverageReport 保存 live tools/list 与 manifest primary 集合的精确比较结果。
type CoverageReport struct {
	Complete          bool                 `json:"complete"`
	LiveToolCount     int                  `json:"live_tool_count"`
	PrimaryCount      int                  `json:"primary_count"`
	MissingPrimary    []string             `json:"missing_primary"`
	UnexpectedPrimary []string             `json:"unexpected_primary"`
	DuplicatePrimary  []string             `json:"duplicate_primary"`
	Assignments       []CoverageAssignment `json:"assignments"`
}

// AssertionResult 保存一个实际执行断言的结果和脱敏失败原因。
type AssertionResult struct {
	Path    string `json:"path"`
	Passed  bool   `json:"passed"`
	Failure string `json:"failure,omitempty"`
}

// ToolEvidence 保存一次真实 tools/call 的关联和严格成功事实。
type ToolEvidence struct {
	CampaignID    string            `json:"campaign_id"`
	ScenarioID    string            `json:"scenario_id"`
	StepID        string            `json:"step_id"`
	Tool          string            `json:"tool"`
	ResourceID    string            `json:"resource_id,omitempty"`
	Outcome       string            `json:"outcome"`
	IsError       bool              `json:"is_error"`
	ApplicationOK *bool             `json:"application_ok,omitempty"`
	Assertions    []AssertionResult `json:"assertions"`
	EvidenceRef   string            `json:"evidence_ref,omitempty"`
}

// Residual 描述 cleanup 后仍存在的 campaign-owned 资源。
type Residual struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Detail string `json:"detail,omitempty"`
}

// CleanupResult 保存 journal、pipeline、远端目录、borrowed topology 与 marker 的终态事实。
type CleanupResult struct {
	JournalComplete        bool       `json:"journal_complete"`
	PipelineTerminal       bool       `json:"pipeline_terminal"`
	RemoteRootAbsent       bool       `json:"remote_root_absent"`
	BorrowedTopologyStable bool       `json:"borrowed_topology_stable"`
	ActiveMarkerRemoved    bool       `json:"active_marker_removed"`
	Residuals              []Residual `json:"residuals"`
}

// ActiveMarker 描述异常中断后阻止下一次 strict run 的具名 campaign 状态。
type ActiveMarker struct {
	CampaignID   string `json:"campaign_id"`
	BundleDigest string `json:"bundle_digest,omitempty"`
	ClonePath    string `json:"clone_path"`
	RemoteRoot   string `json:"remote_root,omitempty"`
	StartedAtUTC string `json:"started_at_utc,omitempty"`
}

// VerdictInput 汇总 strict verdict 唯一允许读取的派生事实。
type VerdictInput struct {
	Checks           []CheckResult  `json:"checks"`
	Coverage         CoverageReport `json:"coverage"`
	PrimaryEvidence  []ToolEvidence `json:"primary_evidence"`
	Cleanup          CleanupResult  `json:"cleanup"`
	ActiveMarker     *ActiveMarker  `json:"active_marker,omitempty"`
	EvidenceComplete bool           `json:"evidence_complete"`
}

// VerdictCounts 记录总 verdict 中各状态的验收面数量。
type VerdictCounts struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Blocked int `json:"blocked"`
	NotRun  int `json:"not_run"`
}

// Verdict 是 CLI 与报告共享的唯一最终判定。
type Verdict struct {
	Status    Status        `json:"status"`
	Complete  bool          `json:"complete"`
	RootCause Cause         `json:"root_cause,omitempty"`
	Counts    VerdictCounts `json:"counts"`
}

// ToolCallResult 是 MCP tools/call 的完整协议结果。
type ToolCallResult struct {
	Content           []map[string]any `json:"content,omitempty"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

// RawMessageMap 把结构化值稳定转换为证据处理所用的 JSON 对象。
//
// 参数：
//   - value: 可被 encoding/json 编码的结构化值
//
// 返回：
//   - 等价的 JSON object；无法转换时返回空 object
//
// 注意：该函数只做内存转换，不负责写盘或脱敏。
func RawMessageMap(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}
