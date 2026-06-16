// jvm_provider.go 实现 Java/Kotlin 的 Language Runtime Provider。
//
// 职责：
//   - JVM 系 RuntimeSchema（program 主类或 jar、classpath、vm_args、program_args）
//   - start_dev 注入 -agentlib:jdwp server=y,suspend=n 预埋 JDWP listen 端口
//
// 边界：
//   - debugger-ready 策略 = prearm：suspend=n 不阻塞业务启动；attach 时连 JDWP 端口
//   - java-debug/JDT LS 适配器启动由 codedebug provider 处理，这里只给语义参数与端口预埋
//   - Kotlin 与 Java 共用：编译产物都是 JVM 字节码，运行/调试链路一致，仅外显标题不同
package langruntime

import (
	"context"
	"fmt"
	"strconv"

	"github.com/xsxdot/super-dev/agent/model"
)

// JVMProvider 服务 Java/Kotlin；按 language 区分标题，JDWP 预埋逻辑一致。
type JVMProvider struct {
	lang model.ServiceLanguage
}

// NewJVMProvider 创建 JVM provider；language 取 Java/Kotlin。
func NewJVMProvider(language model.ServiceLanguage) JVMProvider {
	return JVMProvider{lang: language}
}

// Language 返回该实例服务的 JVM 语言。
func (p JVMProvider) Language() model.ServiceLanguage { return p.lang }

// Capabilities 声明 JVM 能力：prearm-listen，支持 debug_launch 与入口暂停。
func (JVMProvider) Capabilities() Capabilities {
	return Capabilities{DebugReady: DebugReadyByPrearm, DebugLaunch: true, StopOnEntry: true}
}

// RuntimeSchema 返回 JVM 系配置 schema。
func (p JVMProvider) RuntimeSchema(context.Context) RuntimeSchema {
	title := "Java"
	if p.lang == model.LanguageKotlin {
		title = "Kotlin"
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
				Name:     LocalizedText{Key: "runtime.jvm.program.name", Default: "Main class or jar", Values: map[string]string{"zh-CN": "主类或 jar"}},
				Desc:     LocalizedText{Key: "runtime.jvm.program.desc", Default: "Fully-qualified main class (com.app.Main) or a jar path.", Values: map[string]string{"zh-CN": "全限定主类（com.app.Main）或 jar 路径。"}},
			},
			{
				Key:   "classpath",
				Type:  FieldTypeString,
				Group: "basic",
				Order: 20,
				Name:  LocalizedText{Key: "runtime.jvm.classpath.name", Default: "Classpath", Values: map[string]string{"zh-CN": "类路径"}},
				Desc:  LocalizedText{Key: "runtime.jvm.classpath.desc", Default: "Value for -cp, e.g. build/classes or libs/*.", Values: map[string]string{"zh-CN": "-cp 的值，例如 build/classes 或 libs/*。"}},
			},
			{
				Key:   "vm_args",
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 30,
				Name:  LocalizedText{Key: "runtime.jvm.vmArgs.name", Default: "VM arguments", Values: map[string]string{"zh-CN": "VM 参数"}},
				Desc:  LocalizedText{Key: "runtime.jvm.vmArgs.desc", Default: "Arguments for the JVM (before main class).", Values: map[string]string{"zh-CN": "传给 JVM 的参数（在主类之前）。"}},
			},
			{
				Key:   "program_args",
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 40,
				Name:  LocalizedText{Key: "runtime.jvm.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.jvm.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
			},
		},
	}
}

// SuggestConfig 给 JVM 一个低置信占位建议；主类或 jar 需要用户确认。
func (p JVMProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	return []RuntimeConfigSuggestion{{
		Label:      string(p.lang) + " main class",
		CWD:        input.CWD,
		Config:     map[string]any{"classpath": "build/classes"},
		Confidence: "low",
		Reason:     "JVM main class must be confirmed by user",
	}}, nil
}

// Normalize 校验 JVM 字段类型并解析 cwd；program 为空且无逃生口时报错。
func (JVMProvider) Normalize(_ context.Context, input RuntimeConfigInput) (NormalizedRuntimeConfig, []Diagnostic, error) {
	config := map[string]any{}
	for key, value := range input.Config {
		config[key] = value
	}
	for _, key := range []string{"program", "classpath"} {
		if value, ok := config[key]; ok {
			if _, isString := value.(string); !isString {
				return NormalizedRuntimeConfig{}, []Diagnostic{{
					Severity: SeverityError, Field: key, Code: key + "_type_invalid",
					Message: "JVM " + key + " must be a string",
				}}, nil
			}
		}
	}
	if StringValue(config["program"]) == "" {
		if _, ok := EscapeHatchCommand(config); !ok {
			return NormalizedRuntimeConfig{}, []Diagnostic{{
				Severity: SeverityError, Field: "program", Code: "jvm_program_required",
				Message: "JVM runtime needs a main class/jar or a custom command",
			}}, nil
		}
	}
	cwd, err := ResolveRuntimeCWDInsideProject(input.ProjectRoot, input.CWD)
	if err != nil {
		return NormalizedRuntimeConfig{}, []Diagnostic{runtimeCwdDiagnostic(err)}, nil
	}
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         cwd,
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
}

// BuildPlan 生成执行计划；start_dev 在 vm_args 前注入 jdwp agentlib（prearm-listen）。
func (JVMProvider) BuildPlan(_ context.Context, input BuildPlanInput) (ExecutionPlan, []Diagnostic, error) {
	cfg := input.Config
	env := CopyStringMap(cfg.Env)
	debugger := &DebuggerSpec{Adapter: model.CodeDebugProviderJVM, Readiness: ReadinessPrearmListen}

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
	classpath := StringValue(cfg.Config["classpath"])
	vmArgs := StringSliceValue(cfg.Config["vm_args"])
	programArgs := StringSliceValue(cfg.Config["program_args"])

	// java 参数顺序不能随意调整：agent/vm args 必须在 -cp 与主类/jar 之前。
	buildJavaArgs := func(jdwp string) []string {
		args := []string{}
		if jdwp != "" {
			args = append(args, jdwp)
		}
		args = append(args, vmArgs...)
		if classpath != "" {
			args = append(args, "-cp", classpath)
		}
		args = append(args, program)
		args = append(args, programArgs...)
		return args
	}

	switch input.Intent {
	case IntentStartDev:
		// suspend=n 让 JVM 正常启动业务，同时常驻 JDWP listen，attach 时连该端口。
		if input.DebugPort <= 0 {
			return ExecutionPlan{}, []Diagnostic{{
				Severity: SeverityError, Code: "debug_port_required",
				Message: "JVM prearm start_dev requires an allocated debug port",
			}}, nil
		}
		jdwp := "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:" + strconv.Itoa(input.DebugPort)
		args := buildJavaArgs(jdwp)
		debugger.Port = input.DebugPort
		return ExecutionPlan{
			Intent:     IntentStartDev,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "java", Args: args},
			Debugger:   debugger,
			Preview:    PreviewCommand(env, "java", args...),
		}, nil, nil
	case IntentStartNormal:
		args := buildJavaArgs("")
		return ExecutionPlan{
			Intent:     IntentStartNormal,
			WorkingDir: cfg.CWD,
			Env:        env,
			Command:    &CommandSpec{Executable: "java", Args: args},
			Debugger:   debugger,
			Preview:    PreviewCommand(env, "java", args...),
		}, nil, nil
	case IntentDebugLaunch:
		return ExecutionPlan{
			Intent:     IntentDebugLaunch,
			WorkingDir: cfg.CWD,
			Env:        env,
			Debug: &DebugSpec{
				Provider:    model.CodeDebugProviderJVM,
				Program:     program,
				Args:        programArgs,
				StopOnEntry: input.StopOnEntry,
			},
			Debugger: debugger,
			Preview:  PreviewCommand(env, "java", buildJavaArgs("")...),
		}, nil, nil
	case IntentAttach:
		return ExecutionPlan{
			Intent:     IntentAttach,
			WorkingDir: cfg.CWD,
			Attach:     &AttachSpec{Provider: model.CodeDebugProviderJVM, Mode: "listen"},
			Debugger:   debugger,
			Preview:    "java-debug attach jdwp",
		}, nil, nil
	default:
		return ExecutionPlan{}, []Diagnostic{{
			Severity: SeverityError, Code: "intent_unsupported",
			Message: fmt.Sprintf("unsupported JVM runtime intent %s", input.Intent),
		}}, nil
	}
}
