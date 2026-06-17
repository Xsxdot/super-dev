// node_provider.go 实现 Node 的 Language Runtime Provider。
//
// 职责：
//   - Node RuntimeSchema（script/program/program_args/node_args，v1 最小集）
//   - 扫描 package.json scripts/main 给配置建议
//   - 校验并归一化 Node 运行配置
//
// 边界：
//   - debugger-ready 策略：Unix 走 SIGUSR1，Windows 走启动时 --inspect prearm
//   - js-debug adapter 命令由 codedebug 构造，这里只声明 provider 能力
package langruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// NodeProvider 是 Node 的语言运行 provider，无状态。
type NodeProvider struct{}

// NewNodeProvider 创建 Node provider。
func NewNodeProvider() NodeProvider { return NodeProvider{} }

// Language 返回 node。
func (NodeProvider) Language() model.ServiceLanguage { return model.LanguageNode }

// Capabilities 声明 Node 的能力：Unix 通过 SIGUSR1，Windows 通过 --inspect prearm。
func (NodeProvider) Capabilities() Capabilities {
	if runtime.GOOS == "windows" {
		return Capabilities{DebugReady: DebugReadyByPrearm, DebugLaunch: true, StopOnEntry: true}
	}
	return Capabilities{DebugReady: DebugReadyBySignal, DebugLaunch: true, StopOnEntry: true}
}

// RuntimeSchema 返回 Node 的配置 schema（v1 最小集）。
func (NodeProvider) RuntimeSchema(context.Context) RuntimeSchema {
	return RuntimeSchema{
		Language: model.LanguageNode,
		Version:  1,
		Title:    LocalizedText{Key: "runtime.node.title", Default: "Node.js"},
		Fields: []RuntimeSchemaField{
			{
				Key:     "package_manager",
				Name:    LocalizedText{Key: "runtime.node.packageManager.name", Default: "Package manager", Values: map[string]string{"zh-CN": "包管理器"}},
				Desc:    LocalizedText{Key: "runtime.node.packageManager.desc", Default: "pnpm, npm or yarn; used to run the script.", Values: map[string]string{"zh-CN": "pnpm、npm 或 yarn，用于运行 script。"}},
				Type:    FieldTypeString,
				Default: "pnpm",
				Group:   "basic",
				Order:   10,
			},
			{
				Key:   "script",
				Name:  LocalizedText{Key: "runtime.node.script.name", Default: "Script", Values: map[string]string{"zh-CN": "脚本"}},
				Desc:  LocalizedText{Key: "runtime.node.script.desc", Default: "package.json script to run, e.g. dev.", Values: map[string]string{"zh-CN": "要运行的 package.json script，例如 dev。"}},
				Type:  FieldTypeString,
				Group: "basic",
				Order: 20,
			},
			{
				Key:   "program",
				Name:  LocalizedText{Key: "runtime.node.program.name", Default: "Entry file", Values: map[string]string{"zh-CN": "入口文件"}},
				Desc:  LocalizedText{Key: "runtime.node.program.desc", Default: "Run a JS file directly instead of a script, e.g. src/index.js.", Values: map[string]string{"zh-CN": "直接运行 JS 文件而非 script，例如 src/index.js。"}},
				Type:  FieldTypeString,
				Group: "advanced",
				Order: 30,
			},
			{
				Key:   "program_args",
				Name:  LocalizedText{Key: "runtime.node.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.node.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 40,
			},
			{
				Key:   "node_args",
				Name:  LocalizedText{Key: "runtime.node.nodeArgs.name", Default: "Node arguments", Values: map[string]string{"zh-CN": "Node 运行参数"}},
				Desc:  LocalizedText{Key: "runtime.node.nodeArgs.desc", Default: "Arguments passed to the node binary (before the entry file).", Values: map[string]string{"zh-CN": "传给 node 的参数（在入口文件之前）。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 50,
			},
		},
	}
}

// SuggestConfig 扫描 package.json scripts/main 字段，给出 Node 入口建议。
func (NodeProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	cwd := ResolveRuntimeCWD(input.ProjectRoot, input.CWD)
	pkgPath := filepath.Join(cwd, "package.json")
	scripts := readPackageJSONScripts(pkgPath)
	if script := pickDefaultScript(scripts); script != "" {
		pm := detectPackageManager(cwd)
		return []RuntimeConfigSuggestion{{
			Label:      "Node " + pm + " run " + script,
			CWD:        input.CWD,
			Config:     map[string]any{"package_manager": pm, "script": script},
			Confidence: "high",
			Reason:     "from package.json scripts",
		}}, nil
	}
	main := readPackageJSONMain(pkgPath)
	if main == "" {
		return []RuntimeConfigSuggestion{{
			Label:      "Node index.js",
			CWD:        input.CWD,
			Config:     map[string]any{"program": "index.js"},
			Confidence: "low",
			Reason:     "no package.json scripts or main found, defaulting to index.js",
		}}, nil
	}
	return []RuntimeConfigSuggestion{{
		Label:      "Node " + main,
		CWD:        input.CWD,
		Config:     map[string]any{"program": main},
		Confidence: "medium",
		Reason:     "package.json main entry (no scripts)",
	}}, nil
}

// Normalize 校验类型、解析 cwd 为绝对路径。
func (NodeProvider) Normalize(_ context.Context, input RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error) {
	config := map[string]any{}
	for key, value := range input.Config {
		config[key] = value
	}
	if value, ok := config["program"]; ok {
		if _, isString := value.(string); !isString {
			return NormalizedRuntimeConfig{}, []Diagnostic{{
				Severity: SeverityError, Field: "program", Code: "program_type_invalid",
				Message: "Node program must be a string",
			}}, nil
		}
	}
	_, hasEscape := EscapeHatchCommand(config)
	_, hasScript := config["script"]
	_, hasProgram := config["program"]
	if !hasEscape && !hasScript && !hasProgram {
		return NormalizedRuntimeConfig{}, []Diagnostic{{
			Severity: SeverityError, Field: "script", Code: "node_entry_required",
			Message: "Node runtime needs a script, a program entry, or a custom command",
		}}, nil
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

// BuildPlan 由 normalized 配置生成 Node 执行计划；Unix 通过 SIGUSR1，Windows 通过 --inspect prearm。
func (NodeProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	env := CopyStringMap(cfg.Env)
	debugger := nodeDebuggerSpec(runtime.GOOS)

	if step, ok := EscapeHatchCommand(cfg.Config); ok {
		switch input.Intent {
		case IntentStartDev, IntentStartNormal:
			commandEnv := nodePrearmEnv(runtime.GOOS, env)
			return ExecutionPlan{
				Intent:     input.Intent,
				WorkingDir: cfg.CWD,
				Env:        commandEnv,
				Command:    &CommandSpec{Executable: step.Executable, Args: step.Args},
				Debugger:   debugger,
				Preview:    PreviewCommand(commandEnv, step.Executable, step.Args...),
			}, nil, nil
		}
	}

	program := StringValue(cfg.Config["program"])
	nodeArgs := StringSliceValue(cfg.Config["node_args"])
	programArgs := StringSliceValue(cfg.Config["program_args"])
	script := StringValue(cfg.Config["script"])
	packageManager := StringValue(cfg.Config["package_manager"])
	if packageManager == "" {
		packageManager = "pnpm"
	}

	switch input.Intent {
	case IntentStartDev, IntentStartNormal:
		if script != "" {
			// 第一层 · script 主路径：<pm> run <script>，真正的 node 是子进程，
			// Windows 无 SIGUSR1，通过 NODE_OPTIONS 让子 node 启动即打开 inspector。
			commandEnv := nodePrearmEnv(runtime.GOOS, env)
			args := []string{"run", script}
			return ExecutionPlan{
				Intent:     input.Intent,
				WorkingDir: cfg.CWD,
				Env:        commandEnv,
				Command:    &CommandSpec{Executable: packageManager, Args: args},
				Debugger:   debugger,
				Preview:    PreviewCommand(commandEnv, packageManager, args...),
			}, nil, nil
		}
		// node 参数必须位于入口文件之前，否则会被业务程序当成普通参数。
		args := nodePrearmArgs(runtime.GOOS, nodeArgs)
		args = append(args, program)
		args = append(args, programArgs...)
		return ExecutionPlan{
			Intent:     input.Intent,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "node", Args: args},
			Debugger:   debugger,
			Preview:    PreviewCommand(env, "node", args...),
		}, nil, nil
	case IntentDebugLaunch:
		if program == "" {
			return ExecutionPlan{}, []Diagnostic{{
				Severity: SeverityError, Code: "debug_launch_needs_program",
				Message: "debug launch needs a JS program entry; attach a running script instead, or set program",
			}}, nil
		}
		previewArgs := append([]string{}, nodeArgs...)
		previewArgs = append(previewArgs, program)
		programPath, err := ResolveRuntimePathInsideProject(cfg.ProjectRoot, cfg.CWD, program)
		if err != nil {
			return ExecutionPlan{}, []Diagnostic{runtimeProgramDiagnostic("program", err)}, nil
		}
		return ExecutionPlan{
			Intent:     IntentDebugLaunch,
			WorkingDir: cfg.CWD,
			Env:        env,
			Debug: &DebugSpec{
				Provider:    model.CodeDebugProviderNode,
				Program:     programPath,
				Args:        programArgs,
				StopOnEntry: input.StopOnEntry,
			},
			Debugger: debugger,
			Preview:  PreviewCommand(env, "node", previewArgs...),
		}, nil, nil
	case IntentAttach:
		return ExecutionPlan{
			Intent:     IntentAttach,
			WorkingDir: cfg.CWD,
			Attach:     &AttachSpec{Provider: model.CodeDebugProviderNode, Mode: "pid"},
			Debugger:   debugger,
			Preview:    "node --inspect (via SIGUSR1) attach",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: "unsupported Node runtime intent " + string(input.Intent),
		}}, nil
	}
}

func nodeDebuggerSpec(goos string) *DebuggerSpec {
	if goos == "windows" {
		return &DebuggerSpec{Adapter: model.CodeDebugProviderNode, Readiness: ReadinessPrearmListen}
	}
	return &DebuggerSpec{Adapter: model.CodeDebugProviderNode, Readiness: ReadinessSignalAttach, Signal: "SIGUSR1"}
}

func nodePrearmArgs(goos string, nodeArgs []string) []string {
	args := append([]string{}, nodeArgs...)
	if goos != "windows" || hasNodeInspectFlag(args) {
		return args
	}
	// Windows 没有 SIGUSR1，直启 node 时必须在入口文件前预埋 inspector。
	return append([]string{"--inspect=0"}, args...)
}

func nodePrearmEnv(goos string, env map[string]string) map[string]string {
	out := CopyStringMap(env)
	if goos != "windows" {
		return out
	}
	if hasNodeInspectFlag(strings.Fields(out["NODE_OPTIONS"])) {
		return out
	}
	if strings.TrimSpace(out["NODE_OPTIONS"]) == "" {
		out["NODE_OPTIONS"] = "--inspect=0"
		return out
	}
	out["NODE_OPTIONS"] = strings.TrimSpace(out["NODE_OPTIONS"]) + " --inspect=0"
	return out
}

func hasNodeInspectFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--inspect" || arg == "--inspect-brk" ||
			strings.HasPrefix(arg, "--inspect=") || strings.HasPrefix(arg, "--inspect-brk=") {
			return true
		}
	}
	return false
}

func readPackageJSONMain(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var pkg struct {
		Main string `json:"main"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Main
}

// readPackageJSONScripts 读取 package.json 的 scripts 名列表，保序无所谓（我们按优先级挑）。
func readPackageJSONScripts(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	return pkg.Scripts
}

// pickDefaultScript 按 dev > start > serve > 任意 的优先级挑一个默认 script。
func pickDefaultScript(scripts map[string]string) string {
	for _, name := range []string{"dev", "start", "serve"} {
		if _, ok := scripts[name]; ok {
			return name
		}
	}
	for name := range scripts {
		return name
	}
	return ""
}

// detectPackageManager 按 lockfile 推断包管理器，缺省 pnpm。
func detectPackageManager(cwd string) string {
	if _, err := os.Stat(filepath.Join(cwd, "yarn.lock")); err == nil {
		return "yarn"
	}
	if _, err := os.Stat(filepath.Join(cwd, "package-lock.json")); err == nil {
		return "npm"
	}
	return "pnpm"
}
