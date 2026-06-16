// native_provider.go 实现 Rust/C/C++ 的 Language Runtime Provider。
//
// 职责：
//   - 原生系 RuntimeSchema（program/build/build_args/program_args）
//   - 由配置生成 start_dev/start_normal/attach 执行计划，可选构建步作为 PreRun
//
// 边界：
//   - debugger-ready 策略 = attach：构建出带调试信息的二进制后是普通进程，
//     调试保障来自 codedebug 的 lldb pid-attach 链路
//   - lldb-dap 适配器命令由 codedebug 的 Native provider 构造，这里只给语义参数
//   - build 步是可选逃生口：用户给 cargo/make，provider 原样作为 PreRun，不推导编译命令
package langruntime

import (
	"context"
	"fmt"

	"github.com/xsxdot/super-dev/agent/model"
)

// NativeProvider 服务 Rust/C/C++，按 language 区分外显标题，运行/调试链路一致。
type NativeProvider struct {
	lang model.ServiceLanguage
}

// NewNativeProvider 创建原生 provider；language 取 Rust/Cpp。
func NewNativeProvider(language model.ServiceLanguage) NativeProvider {
	return NativeProvider{lang: language}
}

// Language 返回该实例服务的原生语言。
func (p NativeProvider) Language() model.ServiceLanguage { return p.lang }

// Capabilities 声明原生系能力：attach-pid，支持 debug_launch 与入口暂停。
func (NativeProvider) Capabilities() Capabilities {
	return Capabilities{DebugReady: DebugReadyByAttach, DebugLaunch: true, StopOnEntry: true}
}

// RuntimeSchema 返回原生系配置 schema。
func (p NativeProvider) RuntimeSchema(context.Context) RuntimeSchema {
	title := "C/C++"
	if p.lang == model.LanguageRust {
		title = "Rust"
	}
	return RuntimeSchema{
		Language: p.lang,
		Version:  1,
		Title:    LocalizedText{Key: "runtime." + string(p.lang) + ".title", Default: title},
		Fields: []RuntimeSchemaField{
			{
				Key:      "program",
				Required: true,
				Type:     FieldTypeString,
				Group:    "basic",
				Order:    10,
				Name:     LocalizedText{Key: "runtime.native.program.name", Default: "Executable", Values: map[string]string{"zh-CN": "可执行文件"}},
				Desc:     LocalizedText{Key: "runtime.native.program.desc", Default: "Path to the built binary, e.g. target/debug/app.", Values: map[string]string{"zh-CN": "已构建二进制路径，例如 target/debug/app。"}},
			},
			{
				Key:   "build",
				Type:  FieldTypeString,
				Group: "advanced",
				Order: 20,
				Name:  LocalizedText{Key: "runtime.native.build.name", Default: "Build tool", Values: map[string]string{"zh-CN": "构建工具"}},
				Desc:  LocalizedText{Key: "runtime.native.build.desc", Default: "Optional build runner before exec, e.g. cargo or make.", Values: map[string]string{"zh-CN": "exec 前的可选构建器，例如 cargo 或 make。"}},
			},
			{
				Key:   "build_args",
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 30,
				Name:  LocalizedText{Key: "runtime.native.buildArgs.name", Default: "Build arguments", Values: map[string]string{"zh-CN": "构建参数"}},
				Desc:  LocalizedText{Key: "runtime.native.buildArgs.desc", Default: "Arguments for the build tool. Defaults to [build] for cargo.", Values: map[string]string{"zh-CN": "构建工具参数。cargo 默认 [build]。"}},
			},
			{
				Key:   "program_args",
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 40,
				Name:  LocalizedText{Key: "runtime.native.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.native.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
			},
		},
	}
}

// SuggestConfig 给原生系一个低置信占位建议；构建产物路径因项目而异，不强探测。
func (p NativeProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	cfg := map[string]any{"program": "target/debug/app"}
	if p.lang == model.LanguageRust {
		cfg["build"] = "cargo"
	}
	return []RuntimeConfigSuggestion{{
		Label:      string(p.lang) + " binary",
		CWD:        input.CWD,
		Config:     cfg,
		Confidence: "low",
		Reason:     "native build artifact path varies by project",
	}}, nil
}

// Normalize 校验 program/build 为字符串且 program 留在项目内，解析 cwd 为绝对路径。
func (NativeProvider) Normalize(_ context.Context, input RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error) {
	config := map[string]any{}
	for key, value := range input.Config {
		config[key] = value
	}
	for _, key := range []string{"program", "build"} {
		if value, ok := config[key]; ok {
			if _, isString := value.(string); !isString {
				return NormalizedRuntimeConfig{}, []Diagnostic{{
					Severity: SeverityError, Field: key, Code: key + "_type_invalid",
					Message: "native " + key + " must be a string",
				}}, nil
			}
		}
	}
	if StringValue(config["program"]) == "" {
		if _, ok := EscapeHatchCommand(config); !ok {
			return NormalizedRuntimeConfig{}, []Diagnostic{{
				Severity: SeverityError, Field: "program", Code: "native_program_required",
				Message: "native runtime needs a program (built binary) or a custom command",
			}}, nil
		}
	}
	cwd, err := ResolveRuntimeCWDInsideProject(input.ProjectRoot, input.CWD)
	if err != nil {
		return NormalizedRuntimeConfig{}, []Diagnostic{runtimeCwdDiagnostic(err)}, nil
	}
	if program := StringValue(config["program"]); program != "" {
		if _, err := ResolveRuntimePathInsideProject(input.ProjectRoot, cwd, program); err != nil {
			return NormalizedRuntimeConfig{}, []Diagnostic{runtimeProgramDiagnostic("program", err)}, nil
		}
	}
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         cwd,
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
}

// BuildPlan 生成执行计划：build（可选）作为 PreRun，exec 二进制；debug-ready = attach-pid。
func (NativeProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	env := CopyStringMap(cfg.Env)
	debugger := &DebuggerSpec{Adapter: model.CodeDebugProviderNative, Readiness: ReadinessAttachPID}

	if step, ok := EscapeHatchCommand(cfg.Config); ok {
		switch input.Intent {
		case IntentStartDev, IntentStartNormal:
			return ExecutionPlan{
				Intent:     input.Intent,
				WorkingDir: cfg.CWD,
				Env:        env,
				Command:    &CommandSpec{Executable: step.Executable, Args: step.Args},
				Debugger:   debugger,
				Preview:    PreviewCommand(env, step.Executable, step.Args...),
			}, nil, nil
		}
	}

	program := StringValue(cfg.Config["program"])
	programArgs := StringSliceValue(cfg.Config["program_args"])
	build := StringValue(cfg.Config["build"])
	buildArgs := StringSliceValue(cfg.Config["build_args"])

	switch input.Intent {
	case IntentStartDev, IntentStartNormal:
		programPath, err := ResolveRuntimePathInsideProject(cfg.ProjectRoot, cfg.CWD, program)
		if err != nil {
			return ExecutionPlan{}, []Diagnostic{runtimeProgramDiagnostic("program", err)}, nil
		}
		cmd := &CommandSpec{Executable: programPath, Args: programArgs}
		preview := PreviewCommand(env, programPath, programArgs...)
		if build != "" {
			// cargo 的惯用 dev 构建子命令就是 build；其他工具不猜默认参数，避免误执行。
			if len(buildArgs) == 0 && build == "cargo" {
				buildArgs = []string{"build"}
			}
			cmd.PreRun = &CommandStep{Executable: build, Args: buildArgs}
			preview = PreviewCommand(env, build, buildArgs...) + " && " + preview
		}
		return ExecutionPlan{
			Intent:     input.Intent,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    cmd,
			Debugger:   debugger,
			Preview:    preview,
		}, nil, nil
	case IntentDebugLaunch:
		programPath, err := ResolveRuntimePathInsideProject(cfg.ProjectRoot, cfg.CWD, program)
		if err != nil {
			return ExecutionPlan{}, []Diagnostic{runtimeProgramDiagnostic("program", err)}, nil
		}
		return ExecutionPlan{
			Intent:     IntentDebugLaunch,
			WorkingDir: cfg.CWD,
			Env:        env,
			Debug: &DebugSpec{
				Provider:    model.CodeDebugProviderNative,
				Program:     programPath,
				Args:        programArgs,
				StopOnEntry: input.StopOnEntry,
			},
			Debugger: debugger,
			Preview:  "lldb-dap launch " + programPath,
		}, nil, nil
	case IntentAttach:
		return ExecutionPlan{
			Intent:     IntentAttach,
			WorkingDir: cfg.CWD,
			Attach:     &AttachSpec{Provider: model.CodeDebugProviderNative, Mode: "pid"},
			Debugger:   debugger,
			Preview:    "lldb-dap attach <pid>",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: fmt.Sprintf("unsupported native runtime intent %s", input.Intent),
		}}, nil
	}
}
