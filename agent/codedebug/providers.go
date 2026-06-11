// providers.go 实现各语言 DAP adapter 的命令构建。
//
// 职责：
//   - 为 Go/Python 生成开箱可用 adapter 命令
//   - 为 Node 生成显式配置的实验 adapter 命令
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

// NodeProvider 构建实验态 Node DAP adapter 配置。
type NodeProvider struct{}

// NewNodeProvider 创建 Node 代码调试 provider。
func NewNodeProvider() NodeProvider { return NodeProvider{} }

// AdapterCommand 返回用户显式配置的 Node DAP adapter 启动命令。
func (NodeProvider) AdapterCommand(cfg LaunchConfig) (AdapterCommand, error) {
	if cfg.AdapterCommand == "" {
		return AdapterCommand{}, NewAdapterError(
			CodeAdapterUnavailable,
			AdapterCommand{Provider: model.CodeDebugProviderNode},
			ErrAdapterUnavailable,
		)
	}
	return AdapterCommand{
		Provider: model.CodeDebugProviderNode,
		Name:     cfg.AdapterCommand,
		Args:     cfg.AdapterArgs,
		Env:      cfg.Env,
	}, nil
}

// LaunchArguments 返回 Node DAP launch 请求参数。
func (NodeProvider) LaunchArguments(cfg LaunchConfig) map[string]any {
	return map[string]any{
		"program":     cfg.Program,
		"cwd":         cfg.WorkingDir,
		"args":        cfg.Args,
		"env":         cfg.Env,
		"stopOnEntry": cfg.StopOnEntry,
	}
}
