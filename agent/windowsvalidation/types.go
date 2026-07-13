// Package windowsvalidation 实现一次性 Windows 真实验证包的固定合同。
//
// 职责：
//   - 校验冻结构建、固定 MCP 场景与七语言夹具
//   - 驱动已安装的 superdev-mcp.exe 并保存脱敏证据
//   - 从 macOS 构建确定性的 Windows x64 可复制归档
//
// 边界：
//   - 不提供通用任务 Runner、守护进程、调度器或恢复平台
//   - 不在非 Windows 主机产生 Windows 功能 verdict
//   - 不绕过 SuperDev MCP 直接管理受管服务
package windowsvalidation

import "encoding/json"

const (
	// ScenarioKind 标识本包唯一接受的固定 MCP 场景格式。
	ScenarioKind = "superdev.windows-validation.scenario"
	// CoveragePrimary 表示工具的唯一主覆盖归属。
	CoveragePrimary = "primary"
	// CoverageSupporting 表示为同一真实场景提供状态准备或复查的辅助调用。
	CoverageSupporting = "supporting"
)

// FrozenBuild 是便携包绑定的源码、安装器和运行面身份。
type FrozenBuild struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Target        struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"target"`
	Build struct {
		GitCommit      string `json:"git_commit"`
		ProductVersion string `json:"product_version"`
	} `json:"build"`
	Installers    []InstallerIdentity `json:"installers"`
	SourceSurface struct {
		LanguageRuntimeProviders FrozenNameSet `json:"language_runtime_providers"`
		MCPTools                 FrozenNameSet `json:"mcp_tools"`
	} `json:"source_surface"`
	KnownBaselineExceptions []map[string]any `json:"known_baseline_exceptions,omitempty"`
}

// FrozenNameSet 描述冻结名称集合及其规范摘要。
type FrozenNameSet struct {
	Count               int      `json:"count"`
	CanonicalJSONSHA256 string   `json:"canonical_json_sha256"`
	Names               []string `json:"names"`
}

// InstallerIdentity 描述归档外部提供的一个 Windows 安装器。
type InstallerIdentity struct {
	Filename       string `json:"filename"`
	Format         string `json:"format"`
	ValidationRole string `json:"validation_role"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
}

// Scenario 是固定的一次性 MCP 能力场景。
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

// ScenarioStep 描述一个固定 MCP 调用和其证据断言。
type ScenarioStep struct {
	ID        string            `json:"id"`
	Tool      string            `json:"tool"`
	Coverage  string            `json:"coverage"`
	Arguments map[string]any    `json:"arguments,omitempty"`
	Expect    StepExpectation   `json:"expect"`
	Evidence  EvidenceContract  `json:"evidence"`
	Capture   map[string]string `json:"capture,omitempty"`
	Poll      *PollContract     `json:"poll,omitempty"`
	SettleMS  int               `json:"settle_ms,omitempty"`
	RunIf     string            `json:"run_if,omitempty"`
	Comment   string            `json:"_comment,omitempty"`
}

// StepExpectation 固定成功或真实策略拒绝的可接受结果。
type StepExpectation struct {
	Outcome    string      `json:"outcome"`
	Assertions []Assertion `json:"assertions,omitempty"`
}

// Assertion 对 MCP 完整 tools/call result 的一个稳定字段做断言。
type Assertion struct {
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
	Variable string `json:"variable,omitempty"`
}

// EvidenceContract 声明需要保存、脱敏和禁止出现的证据字段。
type EvidenceContract struct {
	Record []string `json:"record,omitempty"`
	Redact []string `json:"redact,omitempty"`
	Forbid []string `json:"forbid,omitempty"`
}

// PollContract 允许固定只读查询等待一个外部状态达到终态。
type PollContract struct {
	IntervalSeconds int `json:"interval_seconds"`
	TimeoutSeconds  int `json:"timeout_seconds"`
}

// CoverageAssignment 是一个冻结工具到场景步骤的唯一主归属。
type CoverageAssignment struct {
	Tool       string `json:"tool"`
	ScenarioID string `json:"scenario_id"`
	StepID     string `json:"step_id"`
}

// ToolCallResult 是 MCP tools/call 的完整协议结果。
type ToolCallResult struct {
	Content           []map[string]string `json:"content,omitempty"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError,omitempty"`
}

// RawMessageMap 将结构化值稳定转换成便于证据处理的 JSON 对象。
func RawMessageMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
