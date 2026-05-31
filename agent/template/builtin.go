// Package template 中的 builtin.go 负责加载内置只读流水线模板。
//
// 职责：
//   - 从 embed FS 读取 builtin/*.yaml
//   - 解析并校验内置模板
//
// 边界：
//   - 不加载用户模板或项目模板
//   - 不发布版本或写入模板 Store
package template

import (
	"embed"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// LoadBuiltins loads bundled readonly templates.
//
// 返回：
//   - 以模板 ID 为 key 的内置模板表
//   - embed 读取、YAML 解析或模板校验失败时返回错误
func LoadBuiltins() (map[string]Template, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, err
	}
	out := map[string]Template{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := builtinFS.ReadFile("builtin/" + entry.Name())
		if err != nil {
			return nil, err
		}
		var tpl Template
		if err := yaml.Unmarshal(data, &tpl); err != nil {
			return nil, err
		}
		if err := Validate(tpl); err != nil {
			return nil, err
		}
		out[tpl.ID] = tpl
	}
	return out, nil
}
