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
	"fmt"

	"github.com/superdev/agent/model"
)

const (
	// CategoryBuild 表示用于构建阶段的模板。
	CategoryBuild = "build"
	// CategoryDeploy 表示用于部署阶段的模板。
	CategoryDeploy = "deploy"
	// CategoryCleanup 表示用于清理阶段的模板。
	CategoryCleanup = "cleanup"
	// CategoryGeneral 表示可在任意阶段使用的通用模板。
	CategoryGeneral = "general"
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
	Category    string           `json:"category,omitempty" yaml:"category,omitempty"`
	Version     string           `json:"version" yaml:"version"`
	Inputs      map[string]Input `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Steps       []Step           `json:"steps" yaml:"steps"`
}

// Step 复用 model.Step，保持模板步骤与流水线步骤字段一致。
type Step = model.Step

// CategoryOrDefault 返回模板分类，未声明时按通用模板处理。
//
// 参数：
//   - category: 模板 YAML 中声明的分类
//
// 返回：
//   - 合法分类，空值兜底为 general
//
// 注意：
//   - 该兜底用于兼容旧的用户模板，不会修改原始 YAML 文件
func CategoryOrDefault(category string) string {
	if category == "" {
		return CategoryGeneral
	}
	return category
}

// ValidCategory 判断模板分类是否属于已知集合。
//
// 参数：
//   - category: 待校验的模板分类
//
// 返回：
//   - true 表示属于 build/deploy/cleanup/general 之一
//
// 注意：
//   - 空分类由调用方决定是否兜底；该函数只判断显式分类是否合法
func ValidCategory(category string) bool {
	switch category {
	case CategoryBuild, CategoryDeploy, CategoryCleanup, CategoryGeneral:
		return true
	default:
		return false
	}
}

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
	if t.Category != "" && !ValidCategory(t.Category) {
		return fmt.Errorf("category must be one of %s, %s, %s, %s", CategoryBuild, CategoryDeploy, CategoryCleanup, CategoryGeneral)
	}
	if len(t.Steps) == 0 {
		return errors.New("steps is required")
	}
	return nil
}
