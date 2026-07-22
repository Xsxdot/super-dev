// providers.go 实现各语言 DAP adapter 的命令构建。
//
// 职责：
//   - 为 Go/Python 生成开箱可用 adapter 命令
//   - 为 Node 生成打包 js-debug standalone adapter 命令
//   - 生成 DAP launch arguments
//
// 边界：
//   - 不检查命令是否存在
//   - 不启动 adapter 进程
package codedebug

import (
	"strconv"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// AdapterCommand 描述即将启动的 DAP adapter 进程。
type AdapterCommand struct {
	Provider model.CodeDebugProvider
	Name     string
	Source   AdapterCommandSource
	Args     []string
	Env      map[string]string
	WorkDir  string
	safeArgs []string
}

// Summary 返回不包含环境变量、绝对路径或用户自定义参数的 adapter 命令摘要。
//
// 返回：
//   - 只含 executable identity 与 provider 标记为安全的固定协议参数的摘要
//
// 注意：
//   - 摘要用于错误和日志上下文，不可替代实际 Args 启动进程。
func (c AdapterCommand) Summary() string {
	parts := []string{}
	if strings.TrimSpace(c.Name) != "" {
		parts = append(parts, adapterExecutableIdentity(c.Name))
	}
	summaryArgs := c.safeArgs
	if summaryArgs == nil {
		for _, arg := range c.Args {
			if safeAdapterSummaryArg(arg) {
				summaryArgs = append(summaryArgs, arg)
			}
		}
	}
	for _, arg := range summaryArgs {
		if strings.TrimSpace(arg) != "" {
			parts = append(parts, strings.TrimSpace(arg))
		}
	}
	summary := strings.Join(parts, " ")
	if len(summary) > 240 {
		return summary[:240] + "..."
	}
	return summary
}

func safeAdapterSummaryArg(arg string) bool {
	// 外部调用方可能只构造 AdapterCommand 而没有 package 内的 safeArgs；这里只允许
	// 固定协议关键字进入摘要，绝不猜测自由参数是否包含 token、路径或用户数据。
	switch strings.TrimSpace(arg) {
	case "dap", "-m", "debugpy.adapter", "--connection", "<port>":
		return true
	default:
		return false
	}
}

// Provider 定义语言调试 provider 需要提供的 adapter 与 launch 配置。
type Provider interface {
	AdapterCommand(LaunchConfig) (AdapterCommand, error)
	LaunchArguments(LaunchConfig) map[string]any
	// AttachCapability 返回 provider 对运行中进程的附加能力档位。
	AttachCapability() AttachMode
	// AttachArguments 构造 DAP attach 请求参数（仅 AttachModePID/Listen 有效）。
	AttachArguments(LaunchConfig, int) map[string]any
	// UsesReverseRequestChildSession 报告该 adapter 是否走 js-debug 那套
	// "root session 收 reverse request 再 spawn child session" 的两段式拓扑。
	// 仅 Node(js-debug) 为 true；Go/Python 等单会话 adapter 为 false。
	UsesReverseRequestChildSession() bool
}

// capturePausePolicy 允许不支持无显式线程暂停的 adapter 声明 capture 时直接下断点。
// 未实现该能力的 provider 沿用先暂停再设断点的稳定路径；js-debug 的 reverse child
// session 也天然属于直接下断点路径。
type capturePausePolicy interface {
	pauseBeforeCapture() bool
}

func shouldPauseBeforeCapture(provider Provider) bool {
	if policy, ok := provider.(capturePausePolicy); ok {
		return policy.pauseBeforeCapture()
	}
	return !provider.UsesReverseRequestChildSession()
}

// GoProvider 构建 Delve DAP adapter 配置。
type GoProvider struct{ Command string }

// NewGoProvider 创建 Go 代码调试 provider。
//
// 参数：
//   - command: 可选的打包/构造器默认 Delve executable
//
// 返回：
//   - 使用显式配置 > 构造器默认 > PATH `dlv` 合同的 GoProvider
//
// 注意：
//   - 省略 command 时保持现有 PATH fallback 行为。
func NewGoProvider(command ...string) GoProvider {
	defaultCommand := ""
	if len(command) > 0 {
		defaultCommand = strings.TrimSpace(command[0])
	}
	return GoProvider{Command: defaultCommand}
}

// AdapterCommand 返回 Delve DAP 启动命令。
func (p GoProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	executable, err := ResolveAdapterExecutable(AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderGo,
		ExplicitCommand: cfg.AdapterCommand,
		ProviderDefault: p.Command,
		PATHFallback:    "dlv",
	})
	if err != nil {
		return AdapterCommand{}, err
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderGo,
		Name:     executable.Name,
		Source:   executable.Source,
		Args:     []string{"dap", "--listen=127.0.0.1:" + strconv.Itoa(cfg.AdapterPort)},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
		safeArgs: []string{"dap"},
	}, nil
}

// LaunchArguments 返回 Delve launch 请求参数。
func (GoProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"mode":        "debug",
		"program":     cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"env":         cfg.Env,
		"stopOnEntry": cfg.StopOnEntry,
	}
}

// AttachCapability 返回 Go 的附加档位：dlv 支持按 PID 本地附加；Windows 通过 tasklist
// 解析进程树后仍走同一 attach-pid 语义。
func (GoProvider) AttachCapability() AttachMode { return AttachModePID }

// AttachArguments 构造 dlv DAP attach 请求参数（本地按 PID 附加）。
func (GoProvider) AttachArguments(cfg LaunchConfig, processID int) map[string]any {
	return map[string]any{
		"mode":      "local",
		"processId": processID,
		"cwd":       cfg.WorkingDir,
	}
}

// UsesReverseRequestChildSession 报告 Go/dlv 是单会话拓扑，不需要子会话。
func (GoProvider) UsesReverseRequestChildSession() bool { return false }

// PythonProvider 构建 debugpy adapter 配置。
type PythonProvider struct{ Python string }

// NewPythonProvider 创建 Python 代码调试 provider。
//
// 参数：
//   - python: provider 默认 Python executable；空值表示只使用 PATH `python3` fallback
//
// 返回：
//   - 使用统一 executable 优先级并保留 debugpy 固定参数的 PythonProvider
func NewPythonProvider(python string) PythonProvider {
	return PythonProvider{Python: strings.TrimSpace(python)}
}

// AdapterCommand 返回 debugpy.adapter 启动命令。
func (p PythonProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	executable, err := ResolveAdapterExecutable(AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderPython,
		ExplicitCommand: cfg.AdapterCommand,
		ProviderDefault: p.Python,
		PATHFallback:    "python3",
	})
	if err != nil {
		return AdapterCommand{}, err
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderPython,
		Name:     executable.Name,
		Source:   executable.Source,
		Args:     []string{"-m", "debugpy.adapter", "--host", "127.0.0.1", "--port", strconv.Itoa(cfg.AdapterPort)},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
		safeArgs: []string{"-m", "debugpy.adapter"},
	}, nil
}

// LaunchArguments 返回 debugpy launch 请求参数。
func (PythonProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"program":     cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"env":         cfg.Env,
		"justMyCode":  true,
		"stopOnEntry": cfg.StopOnEntry,
	}
}

// AttachCapability 返回 Python 的附加档位：debugpy `--listen` 端口本身即完整 DAP 服务，
// 该 prearm-listen 策略不依赖 POSIX signal，Windows 与 Unix 同构。
// DAP 客户端直连该端口即可，无需另起 debugpy.adapter 进程（否则 adapter 与服务角色错位、
// attach 超时 "Timed out waiting for debug server to connect"）。
func (PythonProvider) AttachCapability() AttachMode { return AttachModeDirectDAP }

// AttachArguments 构造 debugpy attach 请求参数。
//
// 注意：
//   - 直连 `--listen` 端口时已经连到了 debugpy 的 DAP 服务本身，attach 不带 connect；
//     带 connect 会让 debugpy 以为还要再连一个远端 server，导致握手超时。
func (PythonProvider) AttachArguments(cfg LaunchConfig, _ int) map[string]any {
	return map[string]any{
		"cwd": cfg.WorkingDir,
	}
}

// UsesReverseRequestChildSession 报告 debugpy 是单会话拓扑，不需要子会话。
func (PythonProvider) UsesReverseRequestChildSession() bool { return false }

// NodeProvider 用打包的 @vscode/js-debug standalone DAP server 调试 Node。
type NodeProvider struct {
	ServerPath string
	Command    string
}

// NewNodeProvider 创建 Node 代码调试 provider。
//
// 参数：
//   - serverPath: 指向打包的 dapDebugServer.js；为空表示 js-debug 未落地
//   - command: 可选的打包/注入 Node executable；未提供时才回退 PATH 上的 node
//
// 返回：
//   - NodeProvider 实例
//
// 注意：
//   - serverPath 为空时 AdapterCommand 会返回 adapter unavailable
func NewNodeProvider(serverPath string, command ...string) NodeProvider {
	defaultCommand := ""
	if len(command) > 0 {
		defaultCommand = strings.TrimSpace(command[0])
	}
	return NodeProvider{ServerPath: serverPath, Command: defaultCommand}
}

// AdapterCommand 返回 js-debug standalone DAP server 启动命令。
func (p NodeProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if strings.TrimSpace(p.ServerPath) == "" {
		return AdapterCommand{}, NewAdapterError(
			CodeAdapterUnavailable,
			AdapterCommand{Provider: model.CodeDebugProviderNode},
			ErrAdapterUnavailable,
		)
	}
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	executable, err := ResolveAdapterExecutable(AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderNode,
		ExplicitCommand: cfg.AdapterCommand,
		ProviderDefault: p.Command,
		PATHFallback:    "node",
	})
	if err != nil {
		return AdapterCommand{}, err
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderNode,
		Name:     executable.Name,
		Source:   executable.Source,
		Args:     []string{p.ServerPath, strconv.Itoa(cfg.AdapterPort), "127.0.0.1"},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
		safeArgs: []string{adapterExecutableIdentity(p.ServerPath)},
	}, nil
}

// LaunchArguments 返回 Node DAP launch 请求参数。
func (NodeProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"type":        "pwa-node",
		"request":     "launch",
		"program":     cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"env":         cfg.Env,
		"stopOnEntry": cfg.StopOnEntry,
	}
}

// AttachCapability 返回 Node 的附加档位：js-debug 连接 Node inspector 端口。
// Unix attach 先 SIGUSR1 惰性打开 inspector；Windows 启动时预埋 --inspect，再直接连接。
func (NodeProvider) AttachCapability() AttachMode { return AttachModeListen }

// AttachArguments 构造 js-debug attach 参数：连接 Node inspector 端口。
func (NodeProvider) AttachArguments(cfg LaunchConfig, _ int) map[string]any {
	port := cfg.TargetPort
	if port == 0 {
		port = cfg.AdapterPort
	}
	return map[string]any{
		"type":                     "pwa-node",
		"request":                  "attach",
		"address":                  "127.0.0.1",
		"port":                     port,
		"cwd":                      cfg.WorkingDir,
		"rootPath":                 cfg.WorkingDir,
		"__workspaceFolder":        cfg.WorkingDir,
		"autoAttachChildProcesses": false,
		"attachExistingChildren":   false,
	}
}

// UsesReverseRequestChildSession 报告 js-debug 用 root -> child 两段式会话拓扑。
func (NodeProvider) UsesReverseRequestChildSession() bool { return true }

// NativeDebugProvider 用 lldb-dap 调试 Rust/C/C++（attach-pid，与 Go 同构；Windows
// 进程枚举走 tasklist/Win32_Process 近似进程树）。
//
// 注意：
//   - deployment 的 adapter_command 始终优先于 Path 构造器默认
//   - 两者都为空时才回退系统 PATH 上的 lldb-dap，为以后打包 CodeLLDB 留口子
type NativeDebugProvider struct{ Path string }

// NewNativeDebugProvider 创建原生调试 provider；path 非空时作为全局注入命令。
func NewNativeDebugProvider(path string) NativeDebugProvider {
	return NativeDebugProvider{Path: strings.TrimSpace(path)}
}

// AdapterCommand 返回 lldb-dap DAP server 启动命令。
func (p NativeDebugProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	executable, err := ResolveAdapterExecutable(AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderNative,
		ExplicitCommand: cfg.AdapterCommand,
		ProviderDefault: p.Path,
		PATHFallback:    "lldb-dap",
	})
	if err != nil {
		return AdapterCommand{}, err
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderNative,
		Name:     executable.Name,
		Source:   executable.Source,
		// lldb-dap 的 TCP server 形态用 --connection listen://host:port；
		// --port 不是 Xcode/LLVM 当前发布版支持的参数，使用它会让 adapter 直接退出。
		Args:     []string{"--connection", "listen://127.0.0.1:" + strconv.Itoa(cfg.AdapterPort)},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
		safeArgs: []string{"--connection"},
	}, nil
}

// LaunchArguments 返回 lldb-dap launch 请求参数。
func (NativeDebugProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"program":     cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"stopOnEntry": cfg.StopOnEntry,
	}
}

// AttachCapability 返回原生系附加档位：lldb 按 PID 本地附加。
func (NativeDebugProvider) AttachCapability() AttachMode { return AttachModePID }

// AttachArguments 构造 lldb-dap attach 参数（本地按 PID）。
func (NativeDebugProvider) AttachArguments(cfg LaunchConfig, processID int) map[string]any {
	args := map[string]any{
		"pid": processID,
		"cwd": cfg.WorkingDir,
	}
	if strings.TrimSpace(cfg.Program) != "" {
		// lldb-dap 的 attach 配置中 program 可帮助 adapter 在 attach 前解析符号与绑定断点。
		args["program"] = cfg.Program
	}
	return args
}

// UsesReverseRequestChildSession 报告 lldb-dap 是单会话拓扑。
func (NativeDebugProvider) UsesReverseRequestChildSession() bool { return false }

// JVMDebugProvider 用 Java debug adapter 调试 JVM（attach 连 JDWP 端口）。
//
// 注意：
//   - 当前官方 java-debug 是 JDT LS plugin，不是可直接 java -cp 启动的 standalone adapter
//   - wrapper 解析遵循显式配置 > 构造器默认 > PATH `jvm-dap-wrapper`
//   - wrapper 需启动 DAP server 并监听传入端口，候选名不代表机器上已满足该能力
type JVMDebugProvider struct{ Command string }

const jvmDAPWrapperPATHFallback = "jvm-dap-wrapper"

// NewJVMDebugProvider 创建 JVM 调试 provider；command 作为 provider 默认，显式配置仍优先。
func NewJVMDebugProvider(command string) JVMDebugProvider {
	return JVMDebugProvider{Command: strings.TrimSpace(command)}
}

// AdapterCommand 启动外部 JVM DAP server wrapper。
func (p JVMDebugProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	executable, err := ResolveAdapterExecutable(AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderJVM,
		ExplicitCommand: cfg.AdapterCommand,
		ProviderDefault: p.Command,
		PATHFallback:    jvmDAPWrapperPATHFallback,
	})
	if err != nil {
		return AdapterCommand{}, err
	}
	args := append([]string{}, cfg.AdapterArgs...)
	// java-debug 现行交付形态依赖 JDT LS 启动 DAP server；这里把端口交给用户 wrapper，
	// 由 wrapper 执行 vscode.java.startDebugSession 等宿主逻辑后监听该端口。
	args = append(args, strconv.Itoa(cfg.AdapterPort))
	return AdapterCommand{
		Provider: model.CodeDebugProviderJVM,
		Name:     executable.Name,
		Source:   executable.Source,
		Args:     args,
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
		// AdapterArgs 属于 deployment 自定义输入，可能携带 token；错误摘要只暴露端口合同。
		safeArgs: []string{"<port>"},
	}, nil
}

// LaunchArguments 返回 JVM DAP launch 请求参数。
func (JVMDebugProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"mainClass":   cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"stopOnEntry": cfg.StopOnEntry,
	}
}

// AttachCapability 返回 JVM 附加档位：java-debug/JDT LS DAP server 连接 JDWP 端口。
func (JVMDebugProvider) AttachCapability() AttachMode { return AttachModeListen }

func (JVMDebugProvider) pauseBeforeCapture() bool { return false }

// AttachArguments 构造 java-debug attach 参数：连 JDWP server 端口。
func (JVMDebugProvider) AttachArguments(cfg LaunchConfig, _ int) map[string]any {
	port := cfg.TargetPort
	if port == 0 {
		port = cfg.AdapterPort
	}
	return map[string]any{
		"request":  "attach",
		"hostName": "127.0.0.1",
		"port":     port,
		"timeout":  10000,
		"cwd":      cfg.WorkingDir,
		// Kotlin DAP 以 projectRoot 解析源码与 classpath；Java DAP 会忽略该扩展字段。
		// 同一 JVM provider 因此可以诚实连接两种 adapter，而无需伪造 Kotlin 的 Java 源码。
		"projectRoot": cfg.WorkingDir,
	}
}

// UsesReverseRequestChildSession 报告 JVM adapter 是单会话拓扑。
func (JVMDebugProvider) UsesReverseRequestChildSession() bool { return false }

func copyEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
