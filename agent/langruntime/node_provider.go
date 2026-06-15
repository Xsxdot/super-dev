// node_provider.go 实现 Node 的 Language Runtime Provider。
//
// 职责：
//   - Node RuntimeSchema（program/program_args/node_args，v1 最小集）
//   - 扫描 package.json main 给配置建议
//   - 校验并归一化 Node 运行配置
//
// 边界：
//   - debugger-ready 策略 = signal：start_dev 普通启动，attach 时 codedebug 发 SIGUSR1
//   - js-debug adapter 命令由 codedebug 构造，这里只声明 provider 能力
package langruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xsxdot/super-dev/agent/model"
)

// NodeProvider 是 Node 的语言运行 provider，无状态。
type NodeProvider struct{}

// NewNodeProvider 创建 Node provider。
func NewNodeProvider() NodeProvider { return NodeProvider{} }

// Language 返回 node。
func (NodeProvider) Language() model.ServiceLanguage { return model.LanguageNode }

// Capabilities 声明 Node 的能力：debugger-ready 通过 SIGUSR1 惰性打开 inspector。
func (NodeProvider) Capabilities() Capabilities {
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
				Key:      "program",
				Name:     LocalizedText{Key: "runtime.node.program.name", Default: "Entry file", Values: map[string]string{"zh-CN": "入口文件"}},
				Desc:     LocalizedText{Key: "runtime.node.program.desc", Default: "JS entry to start and debug, e.g. src/index.js.", Values: map[string]string{"zh-CN": "要启动和调试的 JS 入口，例如 src/index.js。"}},
				Type:     FieldTypeString,
				Required: true,
				Group:    "basic",
				Order:    10,
			},
			{
				Key:   "program_args",
				Name:  LocalizedText{Key: "runtime.node.programArgs.name", Default: "Program arguments", Values: map[string]string{"zh-CN": "程序参数"}},
				Desc:  LocalizedText{Key: "runtime.node.programArgs.desc", Default: "Arguments passed to the application.", Values: map[string]string{"zh-CN": "传给业务程序的参数。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 20,
			},
			{
				Key:   "node_args",
				Name:  LocalizedText{Key: "runtime.node.nodeArgs.name", Default: "Node arguments", Values: map[string]string{"zh-CN": "Node 运行参数"}},
				Desc:  LocalizedText{Key: "runtime.node.nodeArgs.desc", Default: "Arguments passed to the node binary (before the entry file).", Values: map[string]string{"zh-CN": "传给 node 的参数（在入口文件之前）。"}},
				Type:  FieldTypeStringArray,
				Group: "advanced",
				Order: 30,
			},
		},
	}
}

// SuggestConfig 扫描 package.json main 字段，给出 Node 入口建议。
func (NodeProvider) SuggestConfig(_ context.Context, input RuntimeConfigInput) ([]RuntimeConfigSuggestion, error) {
	cwd := ResolveRuntimeCWD(input.ProjectRoot, input.CWD)
	main := readPackageJSONMain(filepath.Join(cwd, "package.json"))
	if main == "" {
		return []RuntimeConfigSuggestion{{
			Label:      "Node index.js",
			CWD:        input.CWD,
			Config:     map[string]any{"program": "index.js"},
			Confidence: "low",
			Reason:     "no package.json main found, defaulting to index.js",
		}}, nil
	}
	return []RuntimeConfigSuggestion{{
		Label:      "Node " + main,
		CWD:        input.CWD,
		Config:     map[string]any{"program": main},
		Confidence: "high",
		Reason:     "package.json main entry",
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
	return NormalizedRuntimeConfig{
		ProjectRoot: input.ProjectRoot,
		CWD:         ResolveRuntimeCWD(input.ProjectRoot, input.CWD),
		Env:         CopyStringMap(input.Env),
		Config:      config,
	}, nil, nil
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
