// go_provider.go 实现 Go 的 Language Runtime Provider。
//
// 职责：
//   - Go RuntimeSchema（program/program_args/build_flags，v1 最小集）
//   - 扫描 main package 给出配置建议（多入口标注歧义）
//   - 由同一份配置生成四个 intent 的执行计划
//
// 边界：
//   - debugger-ready 策略 = attach：start_dev / start_normal 先 build 出调试二进制再 exec；
//     调试保障来自 codedebug 的 pid-attach 链路
//   - dlv 适配器命令由 codedebug 的 GoProvider 构造，这里只给语义参数
package langruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
				Desc:  LocalizedText{Key: "runtime.go.buildFlags.desc", Default: "Flags passed to go build / dlv.", Values: map[string]string{"zh-CN": "传给 go build 或 dlv 的构建参数。"}},
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
	cwd, err := ResolveRuntimeCWDInsideProject(input.ProjectRoot, input.CWD)
	if err != nil {
		return NormalizedRuntimeConfig{}, []Diagnostic{runtimeCwdDiagnostic(err)}, nil
	}
	if _, err := ResolveRuntimePathInsideProject(input.ProjectRoot, cwd, StringValue(config["program"])); err != nil {
		return NormalizedRuntimeConfig{}, []Diagnostic{runtimeProgramDiagnostic("program", err)}, nil
	}
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         cwd,
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
}

// BuildPlan 由 normalized 配置生成执行计划；不重复校验。
func (GoProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	env := CopyStringMap(cfg.Env)
	goDebugger := &DebuggerSpec{Adapter: model.CodeDebugProviderGo, Readiness: ReadinessAttachPID}

	// 第二层逃生口：用户给了运行器（如 make/脚本），原样执行，不 go build。
	// debug-ready 仍是 attach-pid；若逃生口产物不可调试，由 codedebug attach 链路给出诊断。
	if step, ok := EscapeHatchCommand(cfg.Config); ok {
		switch input.Intent {
		case IntentStartDev, IntentStartNormal:
			return ExecutionPlan{
				Intent:     input.Intent,
				WorkingDir: cfg.CWD,
				Env:        env,
				Command:    &CommandSpec{Executable: step.Executable, Args: step.Args},
				Debugger:   goDebugger,
				Preview:    PreviewCommand(env, step.Executable, step.Args...),
			}, nil, nil
		}
	}

	program := StringValue(cfg.Config["program"])
	args := StringSliceValue(cfg.Config["program_args"])
	buildFlags := StringSliceValue(cfg.Config["build_flags"])

	switch input.Intent {
	case IntentStartDev, IntentStartNormal:
		// build+exec 策略：先 go build 出带完整调试信息的二进制（-gcflags=all=-N -l 关闭内联和优化，
		// 保证 attach 时源码断点可用），再 exec 产物。产物是普通进程，满足 DebugReadyByAttach。
		if strings.TrimSpace(input.ArtifactDir) == "" {
			return ExecutionPlan{}, []Diagnostic{{
				Severity: SeverityError, Code: "artifact_dir_required",
				Message: "Go start requires an artifact output dir for build+exec",
			}}, nil
		}
		artifact := filepath.Join(input.ArtifactDir, goArtifactName(program))
		buildArgs := []string{"build", "-gcflags", "all=-N -l"}
		buildArgs = append(buildArgs, buildFlags...)
		buildArgs = append(buildArgs, "-o", artifact, program)
		return ExecutionPlan{
			Intent:     input.Intent,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command: &CommandSpec{
				PreRun:     &CommandStep{Executable: "go", Args: buildArgs},
				Executable: artifact,
				Args:       args,
			},
			Debugger: goDebugger,
			Preview: PreviewCommand(env, "go", buildArgs...) + " && " +
				PreviewCommand(env, artifact, args...),
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
				Provider:    model.CodeDebugProviderGo,
				Program:     programPath,
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
			Debugger:   goDebugger,
			Preview:    "dlv dap attach <pid>",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: fmt.Sprintf("unsupported Go runtime intent %s", input.Intent),
		}}, nil
	}
}

// goArtifactName 从 program 包路径推导当前平台的产物文件名。
func goArtifactName(program string) string {
	return goArtifactNameForOS(program, runtime.GOOS)
}

// goArtifactNameForOS 从 program 包路径推导目标平台的产物文件名。
//
// Windows 的 go build 会为 -o 路径自动补 .exe；执行计划必须使用同一个实际文件名，
// 否则构建成功后会尝试启动不存在的无后缀路径。
func goArtifactNameForOS(program, goos string) string {
	base := filepath.Base(strings.TrimSpace(program))
	if base == "" || base == "." || base == "/" {
		base = "app"
	}
	if goos == "windows" && !strings.EqualFold(filepath.Ext(base), ".exe") {
		base += ".exe"
	}
	return base
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
