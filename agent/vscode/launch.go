// Package vscode 提供对 VS Code 工作区配置文件的解析功能。
//
// 职责：
//   - 解析 <rootPath>/.vscode/launch.json，提取可运行的启动配置
//   - 将 VS Code 特有字段（program/cwd/runtimeExecutable 等）转换为统一的 LaunchConfig 结构
//
// 边界：
//   - 仅支持 type=go 和 type=node 两种启动类型的命令构造
//   - 不执行任何进程，只负责配置解析
//   - request=attach 的条目直接跳过（不可主动启动）
package vscode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LaunchConfig 表示从 launch.json 中解析出的单条启动配置。
type LaunchConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env,omitempty"`
}

// launchFile 对应 launch.json 的完整结构。
type launchFile struct {
	Configurations []launchEntry `json:"configurations"`
}

// launchEntry 对应 configurations 数组中的单个条目。
type launchEntry struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Request           string            `json:"request"`
	Program           string            `json:"program"`
	Args              []string          `json:"args"`
	Cwd               string            `json:"cwd"`
	Env               map[string]string `json:"env"`
	RuntimeExecutable string            `json:"runtimeExecutable"`
	RuntimeArgs       []string          `json:"runtimeArgs"`
}

// ParseLaunch 解析 <rootPath>/.vscode/launch.json 并返回所有可启动的配置列表。
//
// 参数：
//   - rootPath: 项目根目录，用于替换 ${workspaceFolder} 占位符
//
// 返回：
//   - []LaunchConfig: 解析后的启动配置列表（跳过 request=attach 的条目）
//   - error: 文件读取或 JSON 解析错误；文件不存在时返回 nil, nil
//
// 注意：
//   - type=go 根据 program/cwd/args 构建 go run 命令
//   - type=node 根据 runtimeExecutable/runtimeArgs 构建命令
//   - 其他 type 不跳过，Command 字段置为空字符串
func ParseLaunch(rootPath string) ([]LaunchConfig, error) {
	launchPath := filepath.Join(rootPath, ".vscode", "launch.json")

	data, err := os.ReadFile(launchPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 文件不存在不视为错误，正常返回
			return nil, nil
		}
		return nil, err
	}

	var lf launchFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}

	var configs []LaunchConfig
	for _, entry := range lf.Configurations {
		// attach 类型无法主动启动，跳过
		if entry.Request == "attach" {
			continue
		}

		cfg := LaunchConfig{
			Name:    entry.Name,
			Command: buildCommand(rootPath, entry),
			WorkDir: resolveWorkDir(rootPath, entry.Cwd),
			Env:     entry.Env,
		}

		configs = append(configs, cfg)
	}

	return configs, nil
}

// resolveWorkDir 将 cwd 字段中的 ${workspaceFolder} 替换为实际根路径。
func resolveWorkDir(rootPath, cwd string) string {
	return strings.ReplaceAll(cwd, "${workspaceFolder}", rootPath)
}

// buildCommand 根据条目 type 构建对应的启动命令字符串。
func buildCommand(rootPath string, entry launchEntry) string {
	switch entry.Type {
	case "go":
		return buildGoCommand(rootPath, entry)
	case "node":
		return buildNodeCommand(entry)
	default:
		return ""
	}
}

// buildGoCommand 构建 go run 命令。
//
// 逻辑：
//  1. 将 program 中的 ${workspaceFolder} 替换为 rootPath，得到绝对路径
//  2. 计算相对路径 rel：
//     - 若 program == workDir（或 workDir 为空且 program == rootPath），则 rel = "."
//     - 否则尝试 filepath.Rel(workDir, program)，失败则使用绝对路径
//  3. 将 args 中的 ${workspaceFolder} 替换后逐个追加到命令末尾
func buildGoCommand(rootPath string, entry launchEntry) string {
	program := strings.ReplaceAll(entry.Program, "${workspaceFolder}", rootPath)
	workDir := resolveWorkDir(rootPath, entry.Cwd)

	var rel string
	// 判断 program 是否等于 workDir（即在根目录启动）
	effectiveWorkDir := workDir
	if effectiveWorkDir == "" {
		effectiveWorkDir = rootPath
	}

	if program == effectiveWorkDir {
		rel = "."
	} else {
		r, err := filepath.Rel(effectiveWorkDir, program)
		if err != nil {
			// 无法计算相对路径时使用绝对路径
			rel = program
		} else {
			rel = r
		}
	}

	cmd := "go run " + rel

	// 追加 args，替换 ${workspaceFolder}
	for _, arg := range entry.Args {
		resolved := strings.ReplaceAll(arg, "${workspaceFolder}", rootPath)
		cmd += " " + resolved
	}

	return cmd
}

// buildNodeCommand 构建 node 运行命令。
//
// 格式：<runtimeExecutable> <runtimeArgs...>
// 若 runtimeExecutable 为空，返回空字符串。
func buildNodeCommand(entry launchEntry) string {
	if entry.RuntimeExecutable == "" {
		return ""
	}
	parts := []string{entry.RuntimeExecutable}
	parts = append(parts, entry.RuntimeArgs...)
	return strings.Join(parts, " ")
}
