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
	Args     []string
	Env      map[string]string
	WorkDir  string
}

// Summary 返回不包含环境变量的 adapter 命令摘要。
func (c AdapterCommand) Summary() string {
	parts := []string{}
	if strings.TrimSpace(c.Name) != "" {
		parts = append(parts, strings.TrimSpace(c.Name))
	}
	for _, arg := range c.Args {
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

// Provider 定义语言调试 provider 需要提供的 adapter 与 launch 配置。
type Provider interface {
	AdapterCommand(LaunchConfig) (AdapterCommand, error)
	LaunchArguments(LaunchConfig) map[string]any
	// AttachCapability 返回 provider 对运行中进程的附加能力档位。
	AttachCapability() AttachMode
	// AttachArguments 构造 DAP attach 请求参数（仅 AttachModePID/Listen 有效）。
	AttachArguments(LaunchConfig, int) map[string]any
}

// GoProvider 构建 Delve DAP adapter 配置。
type GoProvider struct{}

// NewGoProvider 创建 Go 代码调试 provider。
func NewGoProvider() GoProvider { return GoProvider{} }

// AdapterCommand 返回 Delve DAP 启动命令。
func (GoProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderGo,
		Name:     "dlv",
		Args:     []string{"dap", "--listen=127.0.0.1:" + strconv.Itoa(cfg.AdapterPort)},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
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

// AttachCapability 返回 Go 的附加档位：dlv 支持按 PID 本地附加。
func (GoProvider) AttachCapability() AttachMode { return AttachModePID }

// AttachArguments 构造 dlv DAP attach 请求参数（本地按 PID 附加）。
func (GoProvider) AttachArguments(cfg LaunchConfig, processID int) map[string]any {
	return map[string]any{
		"mode":      "local",
		"processId": processID,
		"cwd":       cfg.WorkingDir,
	}
}

// PythonProvider 构建 debugpy adapter 配置。
type PythonProvider struct{ Python string }

// NewPythonProvider 创建 Python 代码调试 provider。
func NewPythonProvider(python string) PythonProvider {
	if python == "" {
		python = "python3"
	}
	return PythonProvider{Python: python}
}

// AdapterCommand 返回 debugpy.adapter 启动命令。
func (p PythonProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterPort == 0 {
		return AdapterCommand{}, ErrConfigInvalid
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderPython,
		Name:     p.Python,
		Args:     []string{"-m", "debugpy.adapter", "--host", "127.0.0.1", "--port", strconv.Itoa(cfg.AdapterPort)},
		WorkDir:  cfg.WorkingDir,
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

// AttachCapability 返回 Python 的附加档位：debugpy 进程已预埋 listen 端口后直连。
func (PythonProvider) AttachCapability() AttachMode { return AttachModeListen }

// AttachArguments 构造 debugpy attach 请求参数：connect 到进程自带的 --listen 端口。
func (PythonProvider) AttachArguments(cfg LaunchConfig, _ int) map[string]any {
	targetPort := cfg.TargetPort
	if targetPort == 0 {
		targetPort = cfg.AdapterPort
	}
	return map[string]any{
		"connect": map[string]any{"host": "127.0.0.1", "port": targetPort},
		"cwd":     cfg.WorkingDir,
	}
}

// NodeProvider 用打包的 @vscode/js-debug standalone DAP server 调试 Node。
type NodeProvider struct{ ServerPath string }

// NewNodeProvider 创建 Node 代码调试 provider。
//
// 参数：
//   - serverPath: 指向打包的 dapDebugServer.js；为空表示 js-debug 未落地
//
// 返回：
//   - NodeProvider 实例
//
// 注意：
//   - serverPath 为空时 AdapterCommand 会返回 adapter unavailable
func NewNodeProvider(serverPath string) NodeProvider { return NodeProvider{ServerPath: serverPath} }

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
	return AdapterCommand{
		Provider: model.CodeDebugProviderNode,
		Name:     "node",
		Args:     []string{p.ServerPath, strconv.Itoa(cfg.AdapterPort), "127.0.0.1"},
		Env:      copyEnv(cfg.Env),
		WorkDir:  cfg.WorkingDir,
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

// AttachCapability 返回 Node 的附加档位：js-debug 连接 SIGUSR1 打开的 inspector 端口。
func (NodeProvider) AttachCapability() AttachMode { return AttachModeListen }

// AttachArguments 构造 js-debug attach 参数：连接 Node inspector 端口。
func (NodeProvider) AttachArguments(cfg LaunchConfig, _ int) map[string]any {
	port := cfg.TargetPort
	if port == 0 {
		port = cfg.AdapterPort
	}
	return map[string]any{
		"type":    "pwa-node",
		"request": "attach",
		"port":    port,
		"cwd":     cfg.WorkingDir,
	}
}

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
