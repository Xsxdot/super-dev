// python_provider.go 实现 Python 的 Language Runtime Provider。
//
// 职责：
//   - Python RuntimeSchema（program/program_args/module）
//   - 探测 main.py / __main__.py 给配置建议
//   - 校验并归一化 Python 运行配置
//
// 边界：
//   - debugger-ready 策略 = prearm：start_dev 启动时即 python -m debugpy --listen
//   - debugpy adapter 命令由 codedebug 构造，这里只声明 provider 能力
package langruntime

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/xsxdot/super-dev/agent/model"
)

// PythonProvider 是 Python 的语言运行 provider，无状态。
type PythonProvider struct{}

// NewPythonProvider 创建 Python provider。
func NewPythonProvider() PythonProvider { return PythonProvider{} }

// Language 返回 python。
func (PythonProvider) Language() model.ServiceLanguage { return model.LanguagePython }

// Capabilities 声明 Python 的能力：debugger-ready 需要启动时预埋 debugpy listen。
func (PythonProvider) Capabilities() Capabilities {
	return Capabilities{DebugReady: DebugReadyByPrearm, DebugLaunch: true, StopOnEntry: true}
}

// RuntimeSchema 返回 Python 的配置 schema（v1 最小集）。
func (PythonProvider) RuntimeSchema(context.Context) RuntimeSchema {
	return RuntimeSchema{
		Language: model.LanguagePython,
		Version:  1,
		Title:    LocalizedText{Key: "runtime.python.title", Default: "Python"},
		Fields: []RuntimeSchemaField{
			{
				Key:   "program",
				Type:  FieldTypeString,
				Group: "basic",
				Order: 10,
				Name:  LocalizedText{Key: "runtime.python.program.name", Default: "Entry file", Values: map[string]string{"zh-CN": "入口文件"}},
				Desc:  LocalizedText{Key: "runtime.python.program.desc", Default: "Python entry to start and debug, e.g. main.py. Mutually exclusive with module.", Values: map[string]string{"zh-CN": "要启动和调试的入口，例如 main.py。与 module 二选一。"}},
			},
			{
				Key:   "module",
				Type:  FieldTypeString,
				Group: "basic",
				Order: 20,
				Name:  LocalizedText{Key: "runtime.python.module.name", Default: "Module (-m)", Values: map[string]string{"zh-CN": "模块（-m）"}},
				Desc:  LocalizedText{Key: "runtime.python.module.desc", Default: "Run as python -m <module>. Mutually exclusive with program.", Values: map[string]string{"zh-CN": "以 python -m <module> 启动。与 program 二选一。"}},
			},
			{
				Key:   "program_args",
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 30,
				Name:  LocalizedText{Key: "runtime.python.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.python.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
			},
		},
	}
}

// SuggestConfig 探测常见 Python 入口文件，给出配置建议。
func (PythonProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	cwd := ResolveRuntimeCWD(input.ProjectRoot, input.CWD)
	for _, name := range []string{"main.py", "__main__.py", "app.py"} {
		if fileExists(filepath.Join(cwd, name)) {
			return []RuntimeConfigSuggestion{{
				Label:      "Python " + name,
				CWD:        input.CWD,
				Config:     map[string]any{"program": name},
				Confidence: "high",
				Reason:     "found " + name,
			}}, nil
		}
	}
	return []RuntimeConfigSuggestion{{
		Label:      "Python main.py",
		CWD:        input.CWD,
		Config:     map[string]any{"program": "main.py"},
		Confidence: "low",
		Reason:     "no entry found, defaulting to main.py",
	}}, nil
}

// Normalize 校验 program/module 类型、解析 cwd 为绝对路径。
func (PythonProvider) Normalize(_ context.Context, input RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error) {
	config := map[string]any{}
	for key, value := range input.Config {
		config[key] = value
	}
	for _, key := range []string{"program", "module"} {
		if value, ok := config[key]; ok {
			if _, isString := value.(string); !isString {
				return NormalizedRuntimeConfig{}, []Diagnostic{{
					Severity: SeverityError, Field: key, Code: key + "_type_invalid",
					Message: "Python " + key + " must be a string",
				}}, nil
			}
		}
	}
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         ResolveRuntimeCWD(input.ProjectRoot, input.CWD),
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
}

// BuildPlan 由 normalized 配置生成 Python 执行计划；start_dev 预埋 debugpy listen。
func (PythonProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	env := CopyStringMap(cfg.Env)
	debugger := &DebuggerSpec{Adapter: model.CodeDebugProviderPython, Readiness: ReadinessPrearmListen}

	if step, ok := EscapeHatchCommand(cfg.Config); ok {
		switch input.Intent {
		case IntentStartDev, IntentStartNormal:
			// 逃生口原样执行；是否真能连上 debugpy 由 codedebug attach 阶段诊断。
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

	_, targetArgs := pythonTarget(cfg.Config)
	programArgs := StringSliceValue(cfg.Config["program_args"])

	switch input.Intent {
	case IntentStartDev:
		// prearm：dev 默认 debugpy --listen 常驻，不带 --wait-for-client，避免阻塞业务启动。
		if input.DebugPort <= 0 {
			return ExecutionPlan{}, []Diagnostic{{
				Severity: SeverityError, Code: "debug_port_required",
				Message: "Python prearm start_dev requires an allocated debug port",
			}}, nil
		}
		listen := "127.0.0.1:" + strconv.Itoa(input.DebugPort)
		args := append([]string{"-m", "debugpy", "--listen", listen}, targetArgs...)
		args = append(args, programArgs...)
		debugger.Port = input.DebugPort
		return ExecutionPlan{
			Intent:     IntentStartDev,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "python", Args: args},
			Debugger:   debugger,
			Preview:    PreviewCommand(env, "python", args...),
		}, nil, nil
	case IntentStartNormal:
		args := append([]string{}, targetArgs...)
		args = append(args, programArgs...)
		return ExecutionPlan{
			Intent:     IntentStartNormal,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "python", Args: args},
			Debugger:   debugger,
			Preview:    PreviewCommand(env, "python", args...),
		}, nil, nil
	case IntentDebugLaunch:
		previewArgs := append([]string{"-m", "debugpy"}, targetArgs...)
		return ExecutionPlan{
			Intent:     IntentDebugLaunch,
			WorkingDir: cfg.CWD,
			Env:        env,
			Debug: &DebugSpec{
				Provider:    model.CodeDebugProviderPython,
				Program:     ResolveRuntimePath(cfg.CWD, StringValue(cfg.Config["program"])),
				Args:        programArgs,
				StopOnEntry: input.StopOnEntry,
			},
			Debugger: debugger,
			Preview:  PreviewCommand(env, "python", previewArgs...),
		}, nil, nil
	case IntentAttach:
		return ExecutionPlan{
			Intent:     IntentAttach,
			WorkingDir: cfg.CWD,
			Attach:     &AttachSpec{Provider: model.CodeDebugProviderPython, Mode: "listen"},
			Debugger:   debugger,
			Preview:    "debugpy listen attach",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: "unsupported Python runtime intent " + string(input.Intent),
		}}, nil
	}
}

// pythonTarget 返回 python 的启动目标 argv：module 优先 -m <module>，否则 <program>。
func pythonTarget(config map[string]any) (target string, argv []string) {
	if module := StringValue(config["module"]); module != "" {
		return module, []string{"-m", module}
	}
	program := StringValue(config["program"])
	return program, []string{program}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
