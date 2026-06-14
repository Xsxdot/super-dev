// Package langruntime 提供 Language Runtime Provider 契约与核心 provider 实现。
//
// 职责：
//   - 定义语言 provider 契约：RuntimeSchema、SuggestConfig、Normalize、BuildPlan
//   - 由同一份语言运行配置（cwd/env/config）生成 start_dev/start_normal/debug_launch/attach 执行计划
//   - 声明各语言的 debugger-ready 策略（attach/signal/prearm/none）
//
// 边界：
//   - 不管理进程生命周期（process 包负责）
//   - 不管理 DAP session、lease、审批（codedebug/operation 包负责）
//   - 不做配置持久化（config 包负责）
//   - 只覆盖 runtime.type=language 的本地 managed 进程，不碰 systemd/docker 等其他基座
package langruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// BuildIntent 是执行计划的意图。
//
// 刻意不存在的 intent：
//   - investigate：join/attach/重启的决策链属于编排层（codedebug.Manager / 工具层）
//   - preview：preview 是消费方式不是计划种类，任何 intent 的 plan 都带 Preview 字段
type BuildIntent string

const (
	// IntentStartDev 是默认 Start：按 provider 的 DebugReadyStrategy 达成 debugger-ready。
	IntentStartDev BuildIntent = "start_dev"
	// IntentStartNormal 是高级逃生舱：不附加任何调试预埋。
	IntentStartNormal BuildIntent = "start_normal"
	// IntentDebugLaunch 显式在调试器下启动进程，stop_on_entry 可用。
	IntentDebugLaunch BuildIntent = "debug_launch"
	// IntentAttach 附加到已运行的进程。
	IntentAttach BuildIntent = "attach"
)

// DebugReadyStrategy 声明该语言如何达成 debugger-ready 契约。
type DebugReadyStrategy string

const (
	// DebugReadyByAttach 进程天然可事后 attach（Go：dlv pid-attach），start_dev 零额外动作。
	DebugReadyByAttach DebugReadyStrategy = "attach"
	// DebugReadyBySignal 进程可在运行中按需开启调试端口（Node：SIGUSR1）。
	DebugReadyBySignal DebugReadyStrategy = "signal"
	// DebugReadyByPrearm 必须在启动时预埋调试适配器（Python：debugpy listen）。
	DebugReadyByPrearm DebugReadyStrategy = "prearm"
	// DebugReadyNone 暂无不重启即可调试的手段，start_dev 照常启动并外显 Normal。
	DebugReadyNone DebugReadyStrategy = "none"
)

// Capabilities 描述 provider 的能力声明。
type Capabilities struct {
	DebugReady  DebugReadyStrategy `json:"debug_ready"`
	DebugLaunch bool               `json:"debug_launch"`
	StopOnEntry bool               `json:"stop_on_entry"`
}

// FieldType 是 schema 字段类型。v1 刻意最小：不支持 enum/secret/嵌套。
type FieldType string

const (
	FieldTypeString      FieldType = "string"
	FieldTypeBoolean     FieldType = "boolean"
	FieldTypeNumber      FieldType = "number"
	FieldTypeStringArray FieldType = "string_array"
)

// LocalizedText 是可国际化文案：稳定 i18n key + fallback + 可选内置翻译。
type LocalizedText struct {
	Key     string            `json:"key"`
	Default string            `json:"default"`
	Values  map[string]string `json:"values,omitempty"`
}

// RuntimeSchemaField 描述一个 provider 配置字段；key/name/desc 必填。
type RuntimeSchemaField struct {
	Key      string        `json:"key"`
	Name     LocalizedText `json:"name"`
	Desc     LocalizedText `json:"desc"`
	Type     FieldType     `json:"type"`
	Required bool          `json:"required"`
	Default  any           `json:"default,omitempty"`
	Group    string        `json:"group,omitempty"` // basic | advanced
	Order    int           `json:"order,omitempty"`
}

// RuntimeSchema 是前端表单与 MCP 配置指导的共同契约。
type RuntimeSchema struct {
	Language    model.ServiceLanguage `json:"language"`
	Version     int                   `json:"version"`
	Title       LocalizedText         `json:"title"`
	Description LocalizedText         `json:"description,omitempty"`
	Fields      []RuntimeSchemaField  `json:"fields"`
}

// Validate 校验 schema 满足 v1 契约：language/version/title，每个字段 key/name/desc 必填且类型受支持。
func (s RuntimeSchema) Validate() error {
	if s.Language == "" {
		return errors.New("language is required")
	}
	if s.Version <= 0 {
		return errors.New("version is required")
	}
	if s.Title.Key == "" || s.Title.Default == "" {
		return errors.New("title key and default are required")
	}
	seen := map[string]struct{}{}
	for _, field := range s.Fields {
		if field.Key == "" {
			return errors.New("field key is required")
		}
		if _, ok := seen[field.Key]; ok {
			return fmt.Errorf("field %s is duplicated", field.Key)
		}
		seen[field.Key] = struct{}{}
		if field.Name.Key == "" || field.Name.Default == "" {
			return fmt.Errorf("field %s name is required", field.Key)
		}
		if field.Desc.Key == "" || field.Desc.Default == "" {
			return fmt.Errorf("field %s desc is required", field.Key)
		}
		switch field.Type {
		case FieldTypeString, FieldTypeBoolean, FieldTypeNumber, FieldTypeStringArray:
		default:
			return fmt.Errorf("field %s type %s is unsupported", field.Key, field.Type)
		}
	}
	return nil
}

// DiagnosticSeverity 是诊断级别。
type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityInfo    DiagnosticSeverity = "info"
)

// Diagnostic 是配置校验/迁移的诊断信息。
type Diagnostic struct {
	Severity DiagnosticSeverity `json:"severity"`
	Field    string             `json:"field,omitempty"`
	Code     string             `json:"code"`
	Message  string             `json:"message"`
}

// HasErrorDiagnostic 报告诊断列表中是否含 error 级条目。
func HasErrorDiagnostic(items []Diagnostic) bool {
	for _, item := range items {
		if item.Severity == SeverityError {
			return true
		}
	}
	return false
}

// RuntimeConfigInput 是 Normalize/SuggestConfig 的原始输入。
type RuntimeConfigInput struct {
	ProjectRoot string
	CWD         string
	Env         map[string]string
	Config      map[string]any
}

// NormalizedRuntimeConfig 是 Normalize 的输出、BuildPlan 的唯一合法输入。
//
// 用独立类型防止未经 Normalize 的原始配置直接进入 BuildPlan（BuildPlan 不重复校验）。
// CWD 已解析为绝对路径，Config 已补默认值并通过类型校验。
type NormalizedRuntimeConfig struct {
	ProjectRoot string
	CWD         string
	Env         map[string]string
	Config      map[string]any
}

// RuntimeConfigSuggestion 是 SuggestConfig 给出的候选配置。
type RuntimeConfigSuggestion struct {
	Label      string            `json:"label"`
	CWD        string            `json:"cwd,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Config     map[string]any    `json:"config"`
	Confidence string            `json:"confidence"` // high | medium | low
	Reason     string            `json:"reason,omitempty"`
}

// CommandStep 是一个 argv 形式的命令步骤（不拼 shell）。
type CommandStep struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args,omitempty"`
}

// CommandSpec 描述由 process runner 拉起的进程。
//
// PreRun 非空时，启动层必须先同步执行 PreRun（如 go build），
// 成功后才 exec 主进程；PreRun 失败即视为启动失败，其 stderr 作为编译错误上报。
type CommandSpec struct {
	PreRun     *CommandStep `json:"pre_run,omitempty"`
	Executable string       `json:"executable"`
	Args       []string     `json:"args,omitempty"`
}

// DebugSpec 携带 debug_launch 的语义参数；调试适配器命令仍由 codedebug provider 构造
// （spec 分工：langruntime 不负责 DAP/adapter 生命周期）。
type DebugSpec struct {
	Provider    model.CodeDebugProvider `json:"provider"`
	Program     string                  `json:"program"` // 已解析为绝对路径
	Args        []string                `json:"args,omitempty"`
	StopOnEntry bool                    `json:"stop_on_entry"`
}

// AttachSpec 描述 attach 的语义参数；pid 由 codedebug manager 解析后填入。
type AttachSpec struct {
	Provider model.CodeDebugProvider `json:"provider"`
	Mode     string                  `json:"mode"` // pid
}

// AttachTarget 描述一次 attach 的目标。
type AttachTarget struct {
	PID int `json:"pid,omitempty"`
}

// ExecutionPlan 是 provider 的结构化输出；Preview 仅展示和审计，不是配置源。
type ExecutionPlan struct {
	Intent     BuildIntent       `json:"intent"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env,omitempty"`
	Command    *CommandSpec      `json:"command,omitempty"` // start_dev / start_normal
	Debug      *DebugSpec        `json:"debug,omitempty"`   // debug_launch
	Attach     *AttachSpec       `json:"attach,omitempty"`  // attach
	Preview    string            `json:"preview"`
}

// BuildPlanInput 是 BuildPlan 的输入；Config 必须先经 Normalize。
type BuildPlanInput struct {
	Intent BuildIntent
	Config NormalizedRuntimeConfig
	// ArtifactDir 是 build 产物的输出根目录（如 agent 数据目录下 run-bin/<deployment-id>）。
	// 仅 build+exec 策略的语言使用；为空时 provider 回退到不落产物的策略或报 diagnostic。
	ArtifactDir string
	Target      AttachTarget // 仅 attach intent 使用
	StopOnEntry bool         // 仅 debug_launch intent 使用
}

// Provider 是 Language Runtime Provider 契约。
type Provider interface {
	Language() model.ServiceLanguage
	Capabilities() Capabilities
	RuntimeSchema(context.Context) RuntimeSchema
	SuggestConfig(context.Context, RuntimeConfigInput) ([]RuntimeConfigSuggestion, error)
	Normalize(context.Context, RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error)
	BuildPlan(context.Context, BuildPlanInput) (ExecutionPlan, []Diagnostic, error)
}

// ResolveRuntimeCWD 把相对 cwd 解析为基于项目根的绝对路径。
func ResolveRuntimeCWD(projectRoot string, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return filepath.Clean(projectRoot)
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(projectRoot, cwd))
}

// ResolveRuntimePath 把相对路径解析为基于工作目录的绝对路径。
func ResolveRuntimePath(workDir string, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workDir, path))
}

// StringValue 从 any 中提取去空格字符串，非字符串返回空。
func StringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// StringSliceValue 从 any 中提取字符串切片（兼容 YAML/JSON 解出的 []any）。
func StringSliceValue(v any) []string {
	switch items := v.(type) {
	case []string:
		return append([]string{}, items...)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// CopyStringMap 复制 string map；空返回 nil。
func CopyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// PreviewCommand 生成给人看的命令预览（env 前缀按 key 排序保证稳定）。
func PreviewCommand(env map[string]string, command string, args ...string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1+len(args))
	for _, key := range keys {
		parts = append(parts, key+"="+env[key])
	}
	parts = append(parts, command)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
