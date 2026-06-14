// go_provider.go 实现 Go 的 Language Runtime Provider。
//
// 职责：
//   - Go RuntimeSchema（program/program_args/build_flags，v1 最小集）
//   - 扫描 main package 给出配置建议（多入口标注歧义）
//   - 由同一份配置生成四个 intent 的执行计划
//
// 边界：
//   - debugger-ready 策略 = attach：start_dev 与 start_normal 完全相同，
//     不在启动时附加任何调试预埋；调试保障来自 codedebug 的 pid-attach 链路
//   - dlv 适配器命令由 codedebug 的 GoProvider 构造，这里只给语义参数
package langruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// GoProvider 是 Go 的语言运行 provider，无状态。
type GoProvider struct{}

// NewGoProvider 创建 Go provider。
func NewGoProvider() GoProvider { return GoProvider{} }

// Language 返回 go。
func (GoProvider) Language() model.ServiceLanguage { return model.LanguageGo }

// Capabilities 声明 Go 的能力：debugger-ready 走惰性 attach，支持显式 debug_launch 与入口暂停。
func (GoProvider) Capabilities() Capabilities {
	return Capabilities{DebugReady: DebugReadyByAttach, DebugLaunch: true, StopOnEntry: true}
}

// RuntimeSchema 返回 Go 的配置 schema（v1 最小集）。
func (GoProvider) RuntimeSchema(context.Context) RuntimeSchema {
	return RuntimeSchema{
		Language: model.LanguageGo,
		Version:  1,
		Title:    LocalizedText{Key: "runtime.go.title", Default: "Go"},
		Fields: []RuntimeSchemaField{
			{
				Key:      "program",
				Name:     LocalizedText{Key: "runtime.go.program.name", Default: "Go entry package", Values: map[string]string{"zh-CN": "Go 入口包"}},
				Desc:     LocalizedText{Key: "runtime.go.program.desc", Default: "Main package to start and debug, for example ./cmd/server or .", Values: map[string]string{"zh-CN": "要启动和调试的 main package，例如 ./cmd/server 或 ."}},
				Type:     FieldTypeString,
				Required: true,
				Default:  ".",
				Group:    "basic",
				Order:    10,
			},
			{
				Key:   "program_args",
				Name:  LocalizedText{Key: "runtime.go.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.go.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 20,
			},
			{
				Key:   "build_flags",
				Name:  LocalizedText{Key: "runtime.go.buildFlags.name", Default: "Build flags", Values: map[string]string{"zh-CN": "构建参数"}},
				Desc:  LocalizedText{Key: "runtime.go.buildFlags.desc", Default: "Flags passed to go run / dlv.", Values: map[string]string{"zh-CN": "传给 go run 或 dlv 的构建参数。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 30,
			},
		},
	}
}

// SuggestConfig 扫描 cwd 下的 main package（. 与 ./cmd/*）给出候选；唯一入口 high，多入口 medium 标注歧义。
func (GoProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	cwd := ResolveRuntimeCWD(input.ProjectRoot, input.CWD)
	programs := discoverGoMainPackages(cwd)
	out := make([]RuntimeConfigSuggestion, 0, len(programs))
	for _, program := range programs {
		confidence := "medium"
		reason := "multiple main packages detected"
		if len(programs) == 1 {
			confidence = "high"
			reason = "unique main package detected"
		}
		out = append(out, RuntimeConfigSuggestion{
			Label:      "Go " + program,
			CWD:        input.CWD,
			Config:     map[string]any{"program": program},
			Confidence: confidence,
			Reason:     reason,
		})
	}
	if len(out) == 0 {
		out = append(out, RuntimeConfigSuggestion{
			Label:      "Go .",
			CWD:        input.CWD,
			Config:     map[string]any{"program": "."},
			Confidence: "low",
			Reason:     "no main package found, defaulting to module root",
		})
	}
	return out, nil
}

// Normalize 校验类型、补默认值、解析 cwd 为绝对路径。
func (GoProvider) Normalize(_ context.Context, input RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error) {
	config := map[string]any{}
	for key, value := range input.Config {
		config[key] = value
	}
	if value, ok := config["program"]; ok {
		if _, isString := value.(string); !isString {
			return NormalizedRuntimeConfig{}, []Diagnostic{{
				Severity: SeverityError, Field: "program", Code: "program_type_invalid",
				Message: "Go program must be a string",
			}}, nil
		}
	}
	// program 缺省为模块根：provider 能给确定默认值时在 Normalize 补齐（spec 字段要求）
	if StringValue(config["program"]) == "" {
		config["program"] = "."
	}
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         ResolveRuntimeCWD(input.ProjectRoot, input.CWD),
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
}

// BuildPlan 由 normalized 配置生成执行计划；不重复校验。
func (GoProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	program := StringValue(cfg.Config["program"])
	args := StringSliceValue(cfg.Config["program_args"])
	buildFlags := StringSliceValue(cfg.Config["build_flags"])
	env := CopyStringMap(cfg.Env)

	switch input.Intent {
	case IntentStartDev, IntentStartNormal:
		// attach 策略：两个 intent 的进程完全一致，debugger-ready 体现为"随时可 attach"
		commandArgs := append([]string{"run"}, buildFlags...)
		commandArgs = append(commandArgs, program)
		commandArgs = append(commandArgs, args...)
		return ExecutionPlan{
			Intent:     input.Intent,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "go", Args: commandArgs},
			Preview:    PreviewCommand(env, "go", commandArgs...),
		}, nil, nil
	case IntentDebugLaunch:
		return ExecutionPlan{
			Intent:     IntentDebugLaunch,
			WorkingDir: cfg.CWD,
			Env:        env,
			Debug: &DebugSpec{
				Provider:    model.CodeDebugProviderGo,
				Program:     ResolveRuntimePath(cfg.CWD, program),
				Args:        args,
				StopOnEntry: input.StopOnEntry,
			},
			Preview: PreviewCommand(env, "dlv", append([]string{"dap", "launch", program}, args...)...),
		}, nil, nil
	case IntentAttach:
		return ExecutionPlan{
			Intent:     IntentAttach,
			WorkingDir: cfg.CWD,
			Attach:     &AttachSpec{Provider: model.CodeDebugProviderGo, Mode: "pid"},
			Preview:    "dlv dap attach <pid>",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: fmt.Sprintf("unsupported Go runtime intent %s", input.Intent),
		}}, nil
	}
}

// discoverGoMainPackages 在 cwd 的 . 与 ./cmd/* 下定位 main package。
func discoverGoMainPackages(cwd string) []string {
	out := []string{}
	if hasGoMain(filepath.Join(cwd, "main.go")) {
		out = append(out, ".")
	}
	entries, err := os.ReadDir(filepath.Join(cwd, "cmd"))
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && hasGoMain(filepath.Join(cwd, "cmd", entry.Name(), "main.go")) {
				out = append(out, "./cmd/"+entry.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func hasGoMain(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "package main")
}
