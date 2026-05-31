// Package template 管理流水线模板、版本 digest、变量渲染和 include 展开。
//
// 职责：
//   - 定义 Template / Input / Step 等模板侧模型
//   - 校验模板基本结构
//   - 为模板库和展开器提供纯函数能力
//
// 边界：
//   - 不执行流水线步骤
//   - 不直接访问 HTTP 请求或前端 DTO
package template

import (
	"errors"

	"github.com/superdev/agent/model"
)

// Input 描述模板变量，用于前端向导渲染。
type Input struct {
	Label       string   `json:"label" yaml:"label"`
	Type        string   `json:"type" yaml:"type"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Default     string   `json:"default,omitempty" yaml:"default,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
}

// Template 是一个已发布或草稿模板文件。
type Template struct {
	ID          string           `json:"id" yaml:"id"`
	Name        string           `json:"name" yaml:"name"`
	Description string           `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string           `json:"version" yaml:"version"`
	Inputs      map[string]Input `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Steps       []Step           `json:"steps" yaml:"steps"`
}

// Step 复用 model.Step，保持模板步骤与流水线步骤字段一致。
type Step = model.Step

// Validate 校验模板的最小必填字段。
//
// 参数：
//   - t: 待校验的模板
//
// 返回：
//   - 模板结构不满足最小要求时返回错误
//
// 注意：
//   - 插件私有字段不在这里校验，避免模板层耦合具体插件
func Validate(t Template) error {
	if t.ID == "" {
		return errors.New("id is required")
	}
	if t.Name == "" {
		return errors.New("name is required")
	}
	if t.Version == "" {
		return errors.New("version is required")
	}
	if len(t.Steps) == 0 {
		return errors.New("steps is required")
	}
	return nil
}
