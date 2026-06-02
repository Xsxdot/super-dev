// Package template 中的 preview.go 负责流水线模板 dry-run 解析。
//
// 职责：
//   - 从 YAML 字节或文件读取模板草稿
//   - 返回解析后的模板、digest 和校验错误
//   - 供 HTTP API 与 MCP 复用同一套模板校验规则
//
// 边界：
//   - 不写入 user/project 模板库
//   - 不发布版本
//   - 不修改项目 deployment 配置
package template

import (
	"os"

	"gopkg.in/yaml.v3"
)

// PreviewResult 是模板 dry-run 的结构化结果。
type PreviewResult struct {
	Template Template `json:"template"`
	Digest   string   `json:"digest,omitempty"`
	Errors   []string `json:"errors"`
}

// PreviewYAML 解析并校验模板 YAML，但不写入模板库。
//
// 参数：
//   - data: 模板 YAML 字节
//
// 返回：
//   - 模板、digest 和校验错误列表
//
// 注意：
//   - YAML 解析失败和结构校验失败都进入 Errors，方便调用方展示
func PreviewYAML(data []byte) PreviewResult {
	var tpl Template
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		return PreviewResult{Errors: []string{err.Error()}}
	}
	if err := Validate(tpl); err != nil {
		return PreviewResult{Template: tpl, Errors: []string{err.Error()}}
	}
	digest, err := Digest(tpl)
	if err != nil {
		return PreviewResult{Template: tpl, Errors: []string{err.Error()}}
	}
	return PreviewResult{Template: tpl, Digest: digest, Errors: []string{}}
}

// PreviewFile 读取模板 YAML 文件并执行 PreviewYAML。
//
// 参数：
//   - path: 模板 YAML 文件路径
//
// 返回：
//   - 模板、digest 和校验错误列表
func PreviewFile(path string) PreviewResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return PreviewResult{Errors: []string{err.Error()}}
	}
	return PreviewYAML(data)
}
